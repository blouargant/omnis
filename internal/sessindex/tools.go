// tools.go — the `sessions` tool group (search_sessions / read_session /
// list_sessions / report_sessions), mounted on the session_search agent.
//
// search_sessions is always available: it uses the semantic index when one
// resolves and falls back to a direct scan otherwise, so the agent works the same
// with or without an embedder (the additive contract the `docs` group keeps with
// list_docs/grep_docs).
package sessindex

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"

	"github.com/blouargant/omnis/core/adk"
)

// Tool names.
const (
	SearchToolName = "search_sessions"
	ReadToolName   = "read_session"
	ListToolName   = "list_sessions"
	ReportToolName = "report_sessions"
)

// Mode reports how a search was answered.
type Mode string

const (
	// ModeSemantic — answered from the vector index.
	ModeSemantic Mode = "semantic"
	// ModeScan — answered by reading the conversation files directly, because no
	// embedder is configured (permanent) or the index is still cold (transient).
	ModeScan Mode = "scan"
)

// Result is one search result, enriched with the session's live metadata.
type Result struct {
	SessionID  string    `json:"session_id"`
	Title      string    `json:"title,omitempty"`
	Collection string    `json:"collection,omitempty"`
	Archived   bool      `json:"archived,omitempty"`
	TurnIndex  int       `json:"turn_index"`
	At         time.Time `json:"at"`
	Snippet    string    `json:"snippet"`
	Score      float32   `json:"score"`
}

// Enrich resolves each hit's session metadata (title, collection, archived) from
// the conversation file — live, rather than from the index, so a renamed, moved,
// or archived session never shows stale metadata. Hits whose session has since
// been deleted are dropped.
func Enrich(hits []Hit) []Result {
	out := make([]Result, 0, len(hits))
	for _, h := range hits {
		c, _, err := loadConv(h.SessionID)
		if err != nil || !c.searchable() {
			continue // deleted or hidden since it was indexed
		}
		out = append(out, Result{
			SessionID:  h.SessionID,
			Title:      c.Title,
			Collection: c.Collection,
			Archived:   c.Archived,
			TurnIndex:  h.TurnIndex,
			At:         h.At,
			Snippet:    h.Snippet,
			Score:      h.Score,
		})
	}
	return out
}

// SearchOrScan is the single entry point every caller (the tool, the HTTP route)
// uses: semantic when the index is usable, a direct scan otherwise. It reports
// which path answered so the caller can warn the user about a slow scan.
func SearchOrScan(ctx context.Context, idx *Index, query string, k int, excludeArchived bool) ([]Result, Mode, ScanStats, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, ModeScan, ScanStats{}, fmt.Errorf("query is required")
	}
	if k <= 0 {
		k = 10
	}
	// A cold index (never built, or invalidated by an embedder change) answers
	// via scan rather than returning nothing; the caller kicks the build.
	if idx == nil || idx.Len() == 0 {
		hits, stats, err := Scan(ctx, query, ScanOpts{K: k, ExcludeArchived: excludeArchived})
		if err != nil {
			return nil, ModeScan, stats, err
		}
		return Enrich(hits), ModeScan, stats, nil
	}
	hits, err := idx.Search(ctx, query, k)
	if err != nil {
		return nil, ModeSemantic, ScanStats{}, err
	}
	res := Enrich(hits)
	if excludeArchived {
		kept := res[:0]
		for _, r := range res {
			if !r.Archived {
				kept = append(kept, r)
			}
		}
		res = kept
	}
	return res, ModeSemantic, ScanStats{}, nil
}

// Deps wires the tool group to the process-wide index. Index is a thunk (not a
// resolved value) so the index — and the embedder behind it — is only opened when
// a tool is actually called, not at every squad build.
type Deps struct {
	Index func() *Index
}

func (d Deps) index() *Index {
	if d.Index == nil {
		return nil
	}
	return d.Index()
}

type searchIn struct {
	Query           string `json:"query"`
	K               int    `json:"k,omitempty"`
	ExcludeArchived bool   `json:"exclude_archived,omitempty"`
}
type searchOut struct {
	Mode    Mode     `json:"mode"`
	Results []Result `json:"results"`
}

type readIn struct {
	SessionID string `json:"session_id"`
	FromTurn  int    `json:"from_turn,omitempty"`
	Turns     int    `json:"turns,omitempty"`
}
type readTurn struct {
	Index     int       `json:"index"`
	At        time.Time `json:"at"`
	User      string    `json:"user"`
	Assistant string    `json:"assistant"`
}
type readOut struct {
	SessionID  string     `json:"session_id"`
	Title      string     `json:"title,omitempty"`
	TotalTurns int        `json:"total_turns"`
	Turns      []readTurn `json:"turns"`
}

type listIn struct {
	Limit int `json:"limit,omitempty"`
}
type listSession struct {
	SessionID  string    `json:"session_id"`
	Title      string    `json:"title,omitempty"`
	Collection string    `json:"collection,omitempty"`
	Archived   bool      `json:"archived,omitempty"`
	Turns      int       `json:"turns"`
	LastAt     time.Time `json:"last_at"`
}
type listOut struct {
	Sessions []listSession `json:"sessions"`
}

// ReportedSession is one session the agent judged relevant, with its reason.
type ReportedSession struct {
	SessionID string `json:"session_id"`
	Reason    string `json:"reason"`
}
type reportIn struct {
	Sessions []ReportedSession `json:"sessions"`
}
type reportOut struct {
	OK    bool `json:"ok"`
	Count int  `json:"count"`
}

// NewTools builds the `sessions` tool group.
func NewTools(d Deps) []tool.Tool {
	var out []tool.Tool

	if search, err := functiontool.New(functiontool.Config{
		Name: SearchToolName,
		Description: "Search PAST CHAT SESSIONS (including archived ones) for a topic, and return the " +
			"sessions most likely to contain it — each with its `session_id`, `title`, the matching " +
			"`turn_index`, the date, and a `snippet` of the matching exchange. Only what the user saw is " +
			"searched (their requests and the assistant's replies), never tool calls. Ranked by meaning " +
			"when a semantic index is available (`mode: semantic`) and by literal term match otherwise " +
			"(`mode: scan`). Arguments: `query` (string, required); `k` (int, optional, default 10); " +
			"`exclude_archived` (bool, optional). Run several differently-worded queries when the first " +
			"is inconclusive — the user's words are rarely the words used in the session.",
	}, func(tc adk.ToolContext, in searchIn) (searchOut, error) {
		res, mode, _, err := SearchOrScan(tc, d.index(), in.Query, in.K, in.ExcludeArchived)
		if err != nil {
			return searchOut{}, err
		}
		return searchOut{Mode: mode, Results: res}, nil
	}); err == nil {
		out = append(out, search)
	}

	if read, err := functiontool.New(functiontool.Config{
		Name: ReadToolName,
		Description: "Read the actual exchanges of one past session so you can VERIFY a search hit and quote " +
			"it verbatim. Returns the user/assistant text of a slice of its turns. Arguments: `session_id` " +
			"(string, required); `from_turn` (int, optional, default 0); `turns` (int, optional, default 6, " +
			"max 30).",
	}, func(_ adk.ToolContext, in readIn) (readOut, error) {
		id := strings.TrimSpace(in.SessionID)
		if id == "" {
			return readOut{}, fmt.Errorf("session_id is required")
		}
		c, _, err := loadConv(id)
		if err != nil {
			return readOut{}, err
		}
		if !c.searchable() {
			return readOut{}, fmt.Errorf("session %q not found", id)
		}
		from := in.FromTurn
		if from < 0 {
			from = 0
		}
		if from > len(c.Turns) {
			from = len(c.Turns)
		}
		n := in.Turns
		if n <= 0 {
			n = 6
		}
		if n > 30 {
			n = 30
		}
		end := from + n
		if end > len(c.Turns) {
			end = len(c.Turns)
		}
		turns := make([]readTurn, 0, end-from)
		for i := from; i < end; i++ {
			turns = append(turns, readTurn{
				Index:     i,
				At:        c.Turns[i].At,
				User:      c.Turns[i].UserText,
				Assistant: c.Turns[i].AssistantText,
			})
		}
		return readOut{SessionID: id, Title: c.Title, TotalTurns: len(c.Turns), Turns: turns}, nil
	}); err == nil {
		out = append(out, read)
	}

	if list, err := functiontool.New(functiontool.Config{
		Name: ListToolName,
		Description: "List the most recently used past sessions (id, title, collection, turn count, date). " +
			"Use it to answer questions about recent activity, or to orient yourself before searching. " +
			"Arguments: `limit` (int, optional, default 20, max 100).",
	}, func(_ adk.ToolContext, in listIn) (listOut, error) {
		limit := in.Limit
		if limit <= 0 {
			limit = 20
		}
		if limit > 100 {
			limit = 100
		}
		var all []listSession
		for _, id := range listSessionIDs() {
			c, _, err := loadConv(id)
			if err != nil || !c.searchable() {
				continue
			}
			all = append(all, listSession{
				SessionID:  id,
				Title:      c.Title,
				Collection: c.Collection,
				Archived:   c.Archived,
				Turns:      len(c.Turns),
				LastAt:     c.LastAt(),
			})
		}
		sort.Slice(all, func(a, b int) bool { return all[a].LastAt.After(all[b].LastAt) })
		if len(all) > limit {
			all = all[:limit]
		}
		return listOut{Sessions: all}, nil
	}); err == nil {
		out = append(out, list)
	}

	if report, err := functiontool.New(functiontool.Config{
		Name: ReportToolName,
		Description: "Report the sessions that actually answer the user's question, in relevance order, each " +
			"with a one-line reason. Call this ONCE, as your final tool call, before writing your answer — " +
			"the user interface renders the reported sessions as the clickable result list, so a session you " +
			"do not report here is not shown. Report only sessions you verified; report none if none match. " +
			"Arguments: `sessions` (array of {`session_id`, `reason`}, required).",
	}, func(ctx adk.ToolContext, in reportIn) (reportOut, error) {
		n := 0
		for _, s := range in.Sessions {
			if strings.TrimSpace(s.SessionID) != "" {
				n++
			}
		}
		// The report IS the deliverable: the web UI builds its result list from
		// this tool call and DISCARDS any prose the model writes afterwards. So
		// end the run the instant it reports — SkipSummarization makes this
		// function-response event final (session.Event.IsFinalResponse), stopping
		// the ADK flow loop, which otherwise only halts when the model voluntarily
		// returns a tool-call-free response. Without this the model made one more
		// model call to generate a summary nobody reads; on a gateway with
		// generation-throughput variance that trailing call added up to ~2 minutes
		// to a search whose answer was already ready. Same host-side guarantee the
		// routing tools use (see agent/routing.go). Mirror the "call this ONCE, as
		// your final tool call" instruction in code.
		ctx.Actions().SkipSummarization = true
		return reportOut{OK: true, Count: n}, nil
	}); err == nil {
		out = append(out, report)
	}

	return out
}

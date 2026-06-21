package sessions

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/blouargant/yoke/agent"
	"github.com/blouargant/yoke/internal/paths"
)

// logsDir returns the per-user logs directory ($YOKE_HOME/logs). Resolved
// at each call so tests can redirect via t.Setenv("YOKE_HOME", ...).
func logsDir() string { return paths.LogsDir() }

// convLocks serialises read-modify-write access to each session's conversation
// file. Every mutator below loads the file, edits it, and writes it back;
// without a per-session lock two goroutines (a user turn, a background mailbox
// push, the idle curator's harvest-flag write, the idle indexer, async title
// generation) can interleave their load/save — losing each other's updates or,
// worse, reading a half-written file and treating it as corrupt. Keyed by
// sessionID so unrelated sessions never contend.
var convLocks sync.Map // sessionID -> *sync.Mutex

func convLock(sessionID string) *sync.Mutex {
	m, _ := convLocks.LoadOrStore(sessionID, &sync.Mutex{})
	return m.(*sync.Mutex)
}

// ConversationTurn is one user→assistant exchange persisted to disk.
// TokenUsage records the token counts one agent contributed to a turn, plus the
// per-million prices in effect for that agent's model at the time the turn ran.
// Freezing the prices here is what keeps a past turn's budget stable: changing an
// agent's model (or editing a model's price) in models.json must not retroactively
// rewrite the cost of turns that were already billed at the old rate.
//
// Prompt is the TOTAL prompt size and already includes CacheRead + CacheCreate
// (adapters normalise it that way). The billed cost therefore splits the prompt
// into three rates:
//
//	fresh = Prompt - CacheRead - CacheCreate  (billed at InPricePerM)
//	cost  = fresh*InPricePerM + CacheRead*CacheReadPricePerM
//	         + CacheCreate*CacheCreatePricePerM + Output*OutPricePerM   (/1e6)
//
// A cache price of 0 means "no distinct cache rate" and falls back to InPricePerM;
// the input/output prices are omitted (zero) for legacy turns and fall back to a
// default rate. Cache-read is typically ~0.1× input, cache-creation ~1.25×.
type TokenUsage struct {
	Prompt int64 `json:"prompt"`
	Output int64 `json:"output"`
	// CacheRead / CacheCreate are the prompt-cache token counts (a subset of
	// Prompt): tokens served from cache, and tokens written to cache.
	CacheRead   int64 `json:"cache_read,omitempty"`
	CacheCreate int64 `json:"cache_create,omitempty"`
	// InPricePerM / OutPricePerM are the input/output price per million tokens
	// for this agent's model, captured at turn time (omitted when unknown).
	InPricePerM  float64 `json:"in_price_per_m,omitempty"`
	OutPricePerM float64 `json:"out_price_per_m,omitempty"`
	// CacheReadPricePerM / CacheCreatePricePerM are the prompt-cache read/write
	// prices per million tokens, frozen at turn time (0 ⇒ fall back to input).
	CacheReadPricePerM   float64 `json:"cache_read_price_per_m,omitempty"`
	CacheCreatePricePerM float64 `json:"cache_create_price_per_m,omitempty"`
}

// CostUSD prices one agent's usage in dollars. The input/output prices fall back
// to defInPerM/defOutPerM when this turn carries no frozen rate (legacy turns);
// the cache prices fall back to the (resolved) input rate. Prompt is the total
// prompt and includes the cache tokens, so the fresh (full-rate) input tokens are
// Prompt − CacheRead − CacheCreate. Mirrors the web UI's usageCostUSD and the
// TUI's totalCostDollars. This is the single source of truth for budget math.
func (u TokenUsage) CostUSD(defInPerM, defOutPerM float64) float64 {
	inP, outP := u.InPricePerM, u.OutPricePerM
	if inP <= 0 {
		inP = defInPerM
	}
	if outP <= 0 {
		outP = defOutPerM
	}
	cacheReadP, cacheCreateP := u.CacheReadPricePerM, u.CacheCreatePricePerM
	if cacheReadP <= 0 {
		cacheReadP = inP
	}
	if cacheCreateP <= 0 {
		cacheCreateP = inP
	}
	fresh := u.Prompt - u.CacheRead - u.CacheCreate
	if fresh < 0 {
		fresh = 0
	}
	return (float64(fresh)*inP +
		float64(u.CacheRead)*cacheReadP +
		float64(u.CacheCreate)*cacheCreateP +
		float64(u.Output)*outP) / 1_000_000
}

type ConversationTurn struct {
	UserText      string    `json:"user_text"`
	AssistantText string    `json:"assistant_text"`
	At            time.Time `json:"at"`
	// Usage is the per-agent token breakdown for this turn (agent name →
	// counts), captured from the same data that drives the live `turn_usage`
	// SSE events. Persisting it lets the web UI's per-agent cost breakdown
	// survive a server restart / page reload. Omitted (nil) for legacy turns
	// and turns where no usage was captured.
	Usage map[string]TokenUsage `json:"usage,omitempty"`
}

// ConversationFile is the on-disk format for a session's history.
// Legacy files used a plain JSON array; those are read transparently.
type ConversationFile struct {
	Title     string             `json:"title,omitempty"`
	Squad     string             `json:"squad,omitempty"`
	Harvested bool               `json:"harvested,omitempty"`
	Archived  bool               `json:"archived,omitempty"`
	Turns     []ConversationTurn `json:"turns"`
}

// ConversationPath returns the on-disk path for a session's conversation file.
func ConversationPath(sessionID string) string {
	return filepath.Join(logsDir(), fmt.Sprintf("conversation_%s.json", sessionID))
}

// LoadConversationFile reads a session's conversation file, transparently
// migrating legacy plain-array files into the current envelope shape.
// A missing file is not an error and returns an empty ConversationFile.
func LoadConversationFile(sessionID string) (*ConversationFile, error) {
	data, err := os.ReadFile(ConversationPath(sessionID))
	if err != nil {
		if os.IsNotExist(err) {
			return &ConversationFile{}, nil
		}
		return nil, err
	}
	// An empty (0-byte) file is a fresh/never-written session, not corruption —
	// treat it as empty rather than letting json.Unmarshal fail on it.
	if len(data) == 0 {
		return &ConversationFile{}, nil
	}
	// Transparently migrate legacy plain-array format.
	if len(data) > 0 && data[0] == '[' {
		var turns []ConversationTurn
		if err := json.Unmarshal(data, &turns); err != nil {
			return nil, err
		}
		return &ConversationFile{Turns: turns}, nil
	}
	var f ConversationFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, err
	}
	return &f, nil
}

// SaveConversationFile writes f to disk under the session's conversation path.
// The write is atomic: it lands in a temp file in the same directory which is
// fsync'd and then renamed over the target. os.Rename is atomic on POSIX, so a
// concurrent reader — or a server killed mid-write (e.g. a restart) — never
// observes a truncated/partial conversation file. That partial-file state is
// what used to make the next load fail and silently reset the whole history to
// a single turn.
func SaveConversationFile(sessionID string, f *ConversationFile) error {
	dir := logsDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "conversation_*.json.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = tmp.Close(); _ = os.Remove(tmpName) }
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Chmod(tmpName, 0644); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, ConversationPath(sessionID)); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}

// loadForWrite loads a session's conversation file for a read-modify-write
// mutation. Unlike LoadConversationFile it never lets a bad file destroy data:
//
//   - a missing or empty file yields a fresh ConversationFile (normal new session);
//   - an unreadable file (genuine I/O error) returns the error so the caller
//     aborts the write and the existing bytes are left untouched;
//   - a syntactically corrupt file (only reachable for files written by an old
//     non-atomic build, since writes are now atomic) is quarantined to
//     conversation_<id>.json.corrupt-<ts> so its bytes are preserved for manual
//     recovery, and a fresh file is started — the session keeps working instead
//     of every future write re-failing on the same bad file.
//
// This replaces the previous "any load error → start from an empty file"
// behaviour, which silently discarded the entire history on a single transient
// or partial read.
func loadForWrite(sessionID string) (*ConversationFile, error) {
	path := ConversationPath(sessionID)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &ConversationFile{}, nil
		}
		return nil, err
	}
	if len(data) == 0 {
		return &ConversationFile{}, nil
	}
	var jerr error
	if data[0] == '[' {
		var turns []ConversationTurn
		if jerr = json.Unmarshal(data, &turns); jerr == nil {
			return &ConversationFile{Turns: turns}, nil
		}
	} else {
		var f ConversationFile
		if jerr = json.Unmarshal(data, &f); jerr == nil {
			return &f, nil
		}
	}
	// Corrupt JSON. Preserve the original bytes before starting fresh so a
	// single bad file neither destroys the history nor permanently bricks the
	// session.
	quarantine := fmt.Sprintf("%s.corrupt-%d", path, time.Now().UnixNano())
	if rerr := os.Rename(path, quarantine); rerr != nil {
		return nil, fmt.Errorf("conversation %s is corrupt (%v) and could not be quarantined: %w", sessionID, jerr, rerr)
	}
	log.Printf("history: conversation %s was corrupt (%v); preserved original at %s and started fresh", sessionID, jerr, quarantine)
	return &ConversationFile{}, nil
}

// mutateConversation serialises a load-modify-save against a session's
// conversation file under its per-session lock, writing the result atomically.
// On a load error it returns without writing, so a transient failure never
// clobbers existing turns.
func mutateConversation(sessionID string, fn func(*ConversationFile)) error {
	mu := convLock(sessionID)
	mu.Lock()
	defer mu.Unlock()

	f, err := loadForWrite(sessionID)
	if err != nil {
		return err
	}
	fn(f)
	return SaveConversationFile(sessionID, f)
}

// LoadConversationTurns returns just the turn list for a session.
func LoadConversationTurns(sessionID string) ([]ConversationTurn, error) {
	f, err := LoadConversationFile(sessionID)
	if err != nil {
		return nil, err
	}
	return f.Turns, nil
}

// AppendConversationTurn appends one user→assistant exchange and clears
// the Harvested flag so a fresh idle scan re-evaluates the session.
func AppendConversationTurn(sessionID, userText, assistantText string) error {
	return AppendConversationTurnWithUsage(sessionID, userText, assistantText, nil)
}

// AppendConversationTurnWithUsage is AppendConversationTurn plus the per-agent
// token usage captured during the turn (agent name → counts). A nil/empty map
// behaves exactly like AppendConversationTurn.
func AppendConversationTurnWithUsage(sessionID, userText, assistantText string, usage map[string]TokenUsage) error {
	if len(usage) == 0 {
		usage = nil // keep the on-disk field omitted when there's nothing to store
	}
	return mutateConversation(sessionID, func(f *ConversationFile) {
		f.Turns = append(f.Turns, ConversationTurn{
			UserText:      userText,
			AssistantText: assistantText,
			At:            time.Now(),
			Usage:         usage,
		})
		f.Harvested = false // new activity resets the harvest flag
	})
}

// SetConversationHarvested persists the Harvested flag to disk without
// touching the conversation turns. Called by the idle harvester.
func SetConversationHarvested(sessionID string, v bool) error {
	return mutateConversation(sessionID, func(f *ConversationFile) { f.Harvested = v })
}

// SetConversationArchived persists the Archived flag to disk without touching
// the conversation turns. Called when a session is archived or unarchived.
func SetConversationArchived(sessionID string, v bool) error {
	return mutateConversation(sessionID, func(f *ConversationFile) { f.Archived = v })
}

// SetConversationSquad persists the squad name to disk without touching the
// conversation turns. Called when a new session is first created so the
// choice survives a server restart.
func SetConversationSquad(sessionID, squad string) error {
	return mutateConversation(sessionID, func(f *ConversationFile) { f.Squad = squad })
}

// SetConversationTitle persists the session title without touching turns.
func SetConversationTitle(sessionID, title string) error {
	return mutateConversation(sessionID, func(f *ConversationFile) { f.Title = title })
}

// DeleteConversationFile removes the on-disk file for a session.
// A missing file is not an error.
func DeleteConversationFile(sessionID string) {
	if err := os.Remove(ConversationPath(sessionID)); err != nil && !os.IsNotExist(err) {
		log.Printf("history: failed to delete conversation %s: %v", sessionID, err)
	}
}

// DeleteSessionLogs removes all per-session log files produced by the agent
// runtime: tasks, todo, memory, statelog, and mailbox JSONL files. The
// conversation file is deleted separately by DeleteConversationFile via
// Registry.Delete.
func DeleteSessionLogs(userID, sessionID string) {
	suffix := agent.SessionSuffix(userID, sessionID)
	for _, name := range []string{
		fmt.Sprintf("agent_tasks_%s.json", suffix),
		fmt.Sprintf("agent_todo_%s.json", suffix),
		fmt.Sprintf("agent_memory_%s.md", suffix),
		fmt.Sprintf("agent_statelog_%s.json", suffix),
	} {
		_ = os.Remove(filepath.Join(logsDir(), name))
	}
	// Delete per-session mailbox files: $YOKE_HOME/mailboxes/<suffix>:*.jsonl
	matches, _ := filepath.Glob(filepath.Join(paths.MailboxesDir(), suffix+":*.jsonl"))
	for _, f := range matches {
		_ = os.Remove(f)
	}
}

// LoadPersistedSessions scans logs/ for conversation_*.json files and returns
// a SessionMeta for each, so the sidebar populates after a process restart.
func LoadPersistedSessions() []*SessionMeta {
	entries, err := os.ReadDir(logsDir())
	if err != nil {
		return nil
	}
	var out []*SessionMeta
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "conversation_") || !strings.HasSuffix(name, ".json") {
			continue
		}
		id := strings.TrimSuffix(strings.TrimPrefix(name, "conversation_"), ".json")
		f, err := LoadConversationFile(id)
		if err != nil || f == nil || len(f.Turns) == 0 {
			continue
		}
		out = append(out, &SessionMeta{
			ID:         id,
			Title:      f.Title,
			Squad:      f.Squad,
			Harvested:  f.Harvested,
			Archived:   f.Archived,
			UserID:     DefaultUserID,
			CreatedAt:  f.Turns[0].At,
			LastUsedAt: f.Turns[len(f.Turns)-1].At,
			Turns:      len(f.Turns),
		})
	}
	return out
}

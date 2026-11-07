package agent

import (
	"fmt"
	"path"
	"sort"
	"strings"
	"sync"

	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"
	"google.golang.org/adk/v2/tool/toolutils"
	"google.golang.org/genai"

	"github.com/blouargant/omnis/core/adk"
)

// Deferred tools (agent.json `deferred_tools`).
//
// Every declared tool's schema is re-sent on every model call. An agent that
// holds tools it rarely uses pays for them on every step: the Coding squad's
// coder declared 51 tools (~9.4k tokens) and, over six benchmark trials, used
// nine of them. A deferred tool stays CALLABLE — it is registered in the
// request's dispatch map — but its declaration is left out until the session
// activates it, either through tool_search or by calling it by name (the
// tool_search description lists every deferred name). Once active it is
// declared on every later call of that session. Idea taken from Pi's
// tool_search; see CLAUDE.md "Deferred tools".

// toolSearchName is the tool that finds and loads deferred tools.
const toolSearchName = "tool_search"

// toolSearchMaxResults bounds how many tools one search loads.
const toolSearchMaxResults = 12

// deferredSet holds one agent's deferred tools and, per session, which of
// them are active.
type deferredSet struct {
	tools []*deferredTool // sorted by name

	mu     sync.Mutex
	active map[string]map[string]bool // session id → tool name → active
}

func (s *deferredSet) isActive(sessionID, name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.active[sessionID][name]
}

func (s *deferredSet) activate(sessionID string, names ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active == nil {
		s.active = map[string]map[string]bool{}
	}
	m := s.active[sessionID]
	if m == nil {
		m = map[string]bool{}
		s.active[sessionID] = m
	}
	for _, n := range names {
		m[n] = true
	}
}

// deferredTool wraps a tool whose declaration is withheld until activated.
type deferredTool struct {
	inner runnableTool
	set   *deferredSet
}

func (t *deferredTool) Name() string        { return t.inner.Name() }
func (t *deferredTool) Description() string { return t.inner.Description() }
func (t *deferredTool) IsLongRunning() bool { return t.inner.IsLongRunning() }

func (t *deferredTool) Declaration() *genai.FunctionDeclaration { return t.inner.Declaration() }

// ProcessRequest always registers the tool for dispatch (so a call by name
// works and activates it), but adds its declaration only when active. It
// registers THIS wrapper, whose Run activates before delegating.
func (t *deferredTool) ProcessRequest(ctx adk.ToolContext, req *model.LLMRequest) error {
	if t.set.isActive(ctx.SessionID(), t.Name()) {
		return toolutils.PackTool(req, t)
	}
	if req.Tools == nil {
		req.Tools = map[string]any{}
	}
	if _, dup := req.Tools[t.Name()]; dup {
		return fmt.Errorf("duplicate tool: %q", t.Name())
	}
	req.Tools[t.Name()] = t
	return nil
}

func (t *deferredTool) Run(ctx adk.ToolContext, args any) (map[string]any, error) {
	t.set.activate(ctx.SessionID(), t.Name())
	return t.inner.Run(ctx, args)
}

// matchesDeferred reports whether name matches one of the patterns (tool
// names or path.Match globs such as "lsp_*").
func matchesDeferred(patterns []string, name string) bool {
	for _, p := range patterns {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if p == name {
			return true
		}
		if ok, err := path.Match(p, name); err == nil && ok {
			return true
		}
	}
	return false
}

// applyDeferredTools wraps the tools matching patterns and appends a
// tool_search tool for them. With no pattern, or none matching a deferrable
// tool, the list is returned unchanged. A tool that cannot be wrapped (it is
// not a plain runnable function tool) stays declared.
func applyDeferredTools(patterns []string, tools []tool.Tool) []tool.Tool {
	if len(patterns) == 0 {
		return tools
	}
	set := &deferredSet{}
	out := make([]tool.Tool, 0, len(tools)+1)
	for _, t := range tools {
		rt, ok := t.(runnableTool)
		if !ok || t.Name() == toolSearchName || !matchesDeferred(patterns, t.Name()) {
			out = append(out, t)
			continue
		}
		dt := &deferredTool{inner: rt, set: set}
		set.tools = append(set.tools, dt)
		out = append(out, dt)
	}
	if len(set.tools) == 0 {
		return tools
	}
	sort.Slice(set.tools, func(i, j int) bool { return set.tools[i].Name() < set.tools[j].Name() })
	if ts := newToolSearchTool(set); ts != nil {
		out = append(out, ts)
	}
	return out
}

type toolSearchIn struct {
	Query string `json:"query" jsonschema:"Keywords or exact tool names, e.g. \"rename symbol\" or \"lsp_rename lsp_references\""`
}

type toolSearchHit struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type toolSearchOut struct {
	Loaded []toolSearchHit `json:"loaded"`
	Note   string          `json:"note"`
}

func newToolSearchTool(set *deferredSet) tool.Tool {
	names := make([]string, len(set.tools))
	for i, t := range set.tools {
		names[i] = t.Name()
	}
	desc := "Loads tools that are not declared by default to keep requests small. " +
		"Search by keywords or exact names; the matching tools become available for the rest of the session and you call them directly afterwards. " +
		"Deferred tools: " + strings.Join(names, ", ") + "."
	t, err := functiontool.New(functiontool.Config{Name: toolSearchName, Description: desc},
		func(ctx adk.ToolContext, in toolSearchIn) (toolSearchOut, error) {
			hits := searchDeferred(set.tools, in.Query)
			if len(hits) == 0 {
				return toolSearchOut{Note: "No deferred tool matches. Deferred tools: " + strings.Join(names, ", ") + "."}, nil
			}
			out := toolSearchOut{Note: "Loaded. Call these tools directly from now on."}
			loaded := make([]string, 0, len(hits))
			for _, h := range hits {
				out.Loaded = append(out.Loaded, toolSearchHit{Name: h.Name(), Description: h.Description()})
				loaded = append(loaded, h.Name())
			}
			set.activate(ctx.SessionID(), loaded...)
			return out, nil
		})
	if err != nil {
		return nil
	}
	return t
}

// searchDeferred ranks tools against query: an exact name wins outright;
// otherwise each query term scores 3 in the name and 1 in the description.
func searchDeferred(tools []*deferredTool, query string) []*deferredTool {
	terms := strings.FieldsFunc(strings.ToLower(query), func(r rune) bool {
		return r == ' ' || r == ',' || r == '\t' || r == '\n'
	})
	if len(terms) == 0 {
		return nil
	}
	var exact []*deferredTool
	for _, t := range tools {
		for _, term := range terms {
			if strings.ToLower(t.Name()) == term {
				exact = append(exact, t)
				break
			}
		}
	}
	if len(exact) > 0 {
		return exact
	}
	type scored struct {
		t *deferredTool
		s int
	}
	var ranked []scored
	for _, t := range tools {
		name, desc := strings.ToLower(t.Name()), strings.ToLower(t.Description())
		s := 0
		for _, term := range terms {
			if strings.Contains(name, term) {
				s += 3
			} else if strings.Contains(desc, term) {
				s++
			}
		}
		if s > 0 {
			ranked = append(ranked, scored{t, s})
		}
	}
	sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].s > ranked[j].s })
	if len(ranked) > toolSearchMaxResults {
		ranked = ranked[:toolSearchMaxResults]
	}
	out := make([]*deferredTool, len(ranked))
	for i, r := range ranked {
		out[i] = r.t
	}
	return out
}

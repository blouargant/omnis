package agent

import (
	"sort"
	"sync"
)

// promptReach records, for one generation, which agents each overridable
// prompt text reaches (GET /api/prompts; the GEPA tool tests every agent a
// shared text reaches). It records whether or not the text is overridden.
type promptReach struct {
	mu sync.Mutex
	m  map[string]map[string]bool
}

func newPromptReach() *promptReach { return &promptReach{m: map[string]map[string]bool{}} }

func (r *promptReach) add(id, agent string) {
	if r == nil || id == "" || agent == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.m[id] == nil {
		r.m[id] = map[string]bool{}
	}
	r.m[id][agent] = true
}

// snapshot returns id → sorted agent names.
func (r *promptReach) snapshot() map[string][]string {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string][]string, len(r.m))
	for id, set := range r.m {
		names := make([]string, 0, len(set))
		for n := range set {
			names = append(names, n)
		}
		sort.Strings(names)
		out[id] = names
	}
	return out
}

// usePrompt returns the text for id (override or def) and records that agent
// receives it.
func usePrompt(runtime RuntimeSettings, id, agent, def string) string {
	runtime.reach.add(id, agent)
	return promptText(runtime, id, def)
}

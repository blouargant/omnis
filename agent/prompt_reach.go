package agent

import (
	"sort"
	"strings"
	"sync"

	"github.com/blouargant/omnis/core/agentkit"
	"github.com/blouargant/omnis/internal/collmem"
	"github.com/blouargant/omnis/internal/skills"
)

// promptReach records, for one generation, which agents each overridable
// prompt text reaches (GET /api/prompts; the GEPA tool tests every agent a
// shared text reaches). It records whether or not the text is overridden.
type promptReach struct {
	mu       sync.Mutex
	m        map[string]map[string]bool
	defaults map[string]string // tool.<Name> → the tool's own description
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

// blockPart is usePrompt for a part written without its trailing newlines
// (the caller adds them): an override's own trailing newlines are dropped so a
// block keeps its spacing whatever the override ends with.
func blockPart(runtime RuntimeSettings, id, agent, def string) string {
	return strings.TrimRight(usePrompt(runtime, id, agent, def), "\n")
}

// setDefault records the default text of id (first one wins): the tools'
// descriptions are only known when the tools are built.
func (r *promptReach) setDefault(id, text string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.defaults == nil {
		r.defaults = map[string]string{}
	}
	if _, ok := r.defaults[id]; !ok {
		r.defaults[id] = text
	}
}

func (r *promptReach) defaultsSnapshot() map[string]string {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]string, len(r.defaults))
	for k, v := range r.defaults {
		out[k] = v
	}
	return out
}

// PromptEntry is one overridable text of a generation (GET /api/prompts).
type PromptEntry struct {
	ID       string   `json:"id"`
	Default  string   `json:"default"`
	Override string   `json:"override,omitempty"`
	Agents   []string `json:"agents"`
}

// fixedPromptDefaults are the default texts of PromptIDs.
func fixedPromptDefaults() map[string]string {
	return map[string]string{
		"contract.root":            agentkit.SystemPrompt,
		"contract.subagent":        agentkit.SubAgentSystemPrompt,
		"block.subagents":          defaultSubAgentRules,
		"block.handoff":            defaultHandoffRule,
		"block.language":           defaultLanguageReply,
		"block.language.delegates": defaultLanguageDelegates,
		"block.language.evidence":  defaultLanguageEvidence,
		"block.choice":             defaultChoiceRule,
		"block.choice.handoff":     defaultChoiceHandoff,
		"block.steering":           defaultSteeringRule,
		"skills.rules":             skills.DefaultInstruction,
		"memory.rules":             collmem.Rules,
		"memory.reminder":          memoryTurnReminder,
	}
}

// Prompts lists every fixed prompt id, plus tool.<Name> for each overridable
// tool held by an agent of this generation, with its default text, its
// override if any and the agents it reaches. Sorted by id.
func (inst *Instance) Prompts() []PromptEntry {
	if inst == nil {
		return nil
	}
	defaults := fixedPromptDefaults()
	for id, text := range inst.promptDefaults {
		defaults[id] = text
	}
	ids := make([]string, 0, len(defaults))
	for id := range defaults {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]PromptEntry, 0, len(ids))
	for _, id := range ids {
		agents := inst.PromptReach[id]
		if agents == nil {
			agents = []string{}
		}
		out = append(out, PromptEntry{ID: id, Default: defaults[id], Override: inst.Settings.PromptOverrides[id], Agents: agents})
	}
	return out
}

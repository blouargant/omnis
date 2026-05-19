package filter

import (
	"encoding/json"
	"slices"
)

// Filter represents a declarative JSON filter for a command.
type Filter struct {
	Name        string       `json:"name"`
	Version     int          `json:"version"`
	Description string       `json:"description"`
	Match       Match        `json:"match"`
	Inject      *Inject      `json:"inject,omitempty"`
	Streams     []string     `json:"streams,omitempty"` // "stdout", "stderr"; defaults to ["stdout"]
	Pipeline    Pipeline     `json:"pipeline"`
	OnError     string       `json:"on_error"` // "passthrough", "empty", "template"
	Tests       []FilterTest `json:"tests,omitempty"`
}

// FilterTest defines an inline test case for a filter.
type FilterTest struct {
	Name     string `json:"name"`
	Input    string `json:"input"`
	Expected string `json:"expected"`
}

// HasStream returns true if the filter includes the given stream name.
// When Streams is empty (default), only "stdout" is included.
func (f *Filter) HasStream(name string) bool {
	if len(f.Streams) == 0 {
		return name == "stdout"
	}
	return slices.Contains(f.Streams, name)
}

// Match defines which command a filter applies to.
type Match struct {
	Command      string   `json:"command"`
	Subcommand   string   `json:"subcommand,omitempty"`
	ExcludeFlags []string `json:"exclude_flags,omitempty"`
	RequireFlags []string `json:"require_flags,omitempty"`
}

// Inject defines args to inject before execution.
type Inject struct {
	Args          []string          `json:"args,omitempty"`
	Defaults      map[string]string `json:"defaults,omitempty"`
	SkipIfPresent []string          `json:"skip_if_present,omitempty"`
}

// Action represents a single step in a filter pipeline. The JSON form is a
// flat object: the "action" key names the action, and every other key
// becomes an entry in Params.
type Action struct {
	ActionName string
	Params     map[string]any
}

// UnmarshalJSON parses a flat object {"action": "...", ...} into an
// Action, extracting "action" as ActionName and putting the remaining
// keys in Params.
func (a *Action) UnmarshalJSON(data []byte) error {
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if v, ok := raw["action"]; ok {
		if s, ok := v.(string); ok {
			a.ActionName = s
		}
		delete(raw, "action")
	}
	a.Params = raw
	return nil
}

// MarshalJSON emits the flat {"action": "...", ...} form so a filter can
// be round-tripped through the editor without losing parameters.
func (a Action) MarshalJSON() ([]byte, error) {
	out := make(map[string]any, len(a.Params)+1)
	for k, v := range a.Params {
		out[k] = v
	}
	out["action"] = a.ActionName
	return json.Marshal(out)
}

// Pipeline is an ordered sequence of actions.
type Pipeline []Action

// ActionResult is the data passed between pipeline actions.
type ActionResult struct {
	Lines    []string
	Metadata map[string]any
}

// ActionFunc is the signature for built-in action implementations.
type ActionFunc func(input ActionResult, params map[string]any) (ActionResult, error)

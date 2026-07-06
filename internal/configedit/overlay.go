package configedit

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/blouargant/omnis/internal/paths"
)

// This file is the write side of the layered config model: instead of forking a
// whole config file into the user layer (which then shadows package updates), a
// save persists only the delta between the desired effective config and the
// merge of every layer BELOW the write target. Combined with the read-side
// deep-merge, an untouched field keeps evolving with package updates.

// MergeGeneric deep-merges plain JSON objects low→high with no section-specific
// collection rules: nested objects recurse, scalars take the higher layer, plain
// arrays are replaced by the higher layer. Used for per-agent registry entries
// (registry/agents/<name>/agent.json) so a user overlay that changes one field
// keeps evolving with package updates to the agent's other fields.
func MergeGeneric(layers []map[string]any) map[string]any {
	return MergeSection("", layers)
}

// DiffGeneric is the write side of MergeGeneric: the minimal overlay such that
// MergeGeneric([base, DiffGeneric(base, desired)]) == desired (for
// additive/modifying edits — field removal inside an object is not
// representable, see diffValue).
func DiffGeneric(base, desired map[string]any) map[string]any {
	return DiffSection("", base, desired)
}

// layerRank orders the config layers low→high: system < user < local. Unknown
// layers are treated as user.
func layerRank(layer string) int {
	switch layer {
	case "system":
		return 0
	case "user":
		return 1
	case "local":
		return 2
	default:
		return 1
	}
}

func parseFileMap(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if len(data) == 0 {
		return nil, nil
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// BaseBelowLayer merges every existing layer of filename STRICTLY BELOW
// writeLayer (low→high). It is the baseline a write to writeLayer diffs against,
// so persisting the overlay + this base reproduces the desired config while
// leaving package-shipped items in the lower layers to evolve independently.
func BaseBelowLayer(filename, writeLayer string) (map[string]any, error) {
	r := layerRank(writeLayer)
	var below []map[string]any
	for _, p := range paths.ConfigLayers(filename) { // low→high, existing
		if layerRank(paths.Layer(p)) >= r {
			continue
		}
		m, err := parseFileMap(p)
		if err != nil {
			return nil, err
		}
		if m != nil {
			below = append(below, m)
		}
	}
	return MergeSection(filename, below), nil
}

// OverlayBytes computes the minimal overlay to persist `desired` for filename at
// writePath: it diffs desired against the merge of all layers below writePath's
// layer and serialises the result (always a valid JSON object — "{}\n" when
// nothing differs from the base). This is what a delta-aware save writes.
func OverlayBytes(filename string, desired map[string]any, writePath string) ([]byte, error) {
	base, err := BaseBelowLayer(filename, paths.Layer(writePath))
	if err != nil {
		return nil, err
	}
	overlay := DiffSection(filename, base, desired)
	if overlay == nil {
		overlay = map[string]any{}
	}
	out, err := json.MarshalIndent(overlay, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// agentEntryBaseBelow merges the agent.json of `name` across every registry
// layer strictly below writeLayer (low→high, generic merge). registrySearchDirs
// is high→low precedence (paths.AgentsRegistrySearchDirs()).
func agentEntryBaseBelow(name, writeLayer string, registrySearchDirs []string) (map[string]any, error) {
	r := layerRank(writeLayer)
	var below []map[string]any
	for i := len(registrySearchDirs) - 1; i >= 0; i-- {
		dir := registrySearchDirs[i]
		if layerRank(paths.Layer(dir)) >= r {
			continue
		}
		m, err := parseFileMap(filepath.Join(dir, name, "agent.json"))
		if err != nil {
			return nil, err
		}
		if m != nil {
			below = append(below, m)
		}
	}
	return MergeGeneric(below), nil
}

// AgentEntryOverlayBytes computes the minimal per-agent registry overlay to
// persist `desired` (one agent's agent.json fields) at writeLayer, diffing
// against the merge of that agent's entries in every registry layer below it.
// So editing one field of a package-shipped agent writes only that field, and
// package updates to the agent's other fields keep flowing through.
// registrySearchDirs is high→low (paths.AgentsRegistrySearchDirs()).
func AgentEntryOverlayBytes(name, writeLayer string, desired map[string]any, registrySearchDirs []string) ([]byte, error) {
	base, err := agentEntryBaseBelow(name, writeLayer, registrySearchDirs)
	if err != nil {
		return nil, err
	}
	overlay := DiffGeneric(base, desired)
	if overlay == nil {
		overlay = map[string]any{}
	}
	out, err := json.MarshalIndent(overlay, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

package configedit

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/blouargant/omnis/internal/paths"
)

// ReadAgentsConfig reads the top-level agents.json (the `agents` names list +
// `squads`) as the MERGED effective view — every layer deep-merged so callers
// see the full wired set (package agents/squads + user overlay), not just the
// highest-precedence file. This is essential for the settings tools' pointer
// edits: diffing an unmerged view against the layer base would tombstone
// package-shipped squads the overlay never saw. Returns an empty map when
// agents.json exists in no layer.
func ReadAgentsConfig() (parsed map[string]any, readPath, layer string, err error) {
	p, _ := ReadPath("agent")
	readPath = p
	layer = paths.Layer(p)
	merged, _, merr := LoadMergedSection("agents.json")
	if merr != nil {
		return nil, readPath, layer, fmt.Errorf("agents.json is not valid JSON: %w", merr)
	}
	if merged == nil {
		return map[string]any{}, readPath, layer, nil
	}
	return merged, readPath, layer, nil
}

// ReadAgentEntry reads the MERGED registry/agents/<name>/agent.json for the
// named agent — every layer deep-merged (low→high, MergeGeneric), so callers see
// the full effective entry (package fields + user overrides) rather than only
// the highest-precedence file. It returns the merged entry, the layer of the
// highest-precedence file (for display), and that path. Returns os.ErrNotExist
// when no definition is found in any layer.
func ReadAgentEntry(name string) (entry map[string]any, layer, path string, err error) {
	dirs := paths.AgentsRegistrySearchDirs() // high→low
	var layersLowHigh []map[string]any
	for i := len(dirs) - 1; i >= 0; i-- {
		p := filepath.Join(dirs[i], name, "agent.json")
		data, rerr := os.ReadFile(p)
		if rerr != nil {
			continue
		}
		var m map[string]any
		if uerr := json.Unmarshal(data, &m); uerr != nil {
			return nil, "", p, fmt.Errorf("%s is not valid JSON: %w", p, uerr)
		}
		layersLowHigh = append(layersLowHigh, m)
	}
	// Highest-precedence path + layer for display (first high→low that exists).
	for _, dir := range dirs {
		p := filepath.Join(dir, name, "agent.json")
		if st, serr := os.Stat(p); serr == nil && !st.IsDir() {
			path = p
			layer = paths.Layer(dir)
			break
		}
	}
	if len(layersLowHigh) == 0 {
		return nil, "", "", os.ErrNotExist
	}
	return MergeGeneric(layersLowHigh), layer, path, nil
}

// AgentSkills extracts the declared skills list from a parsed agent entry.
func AgentSkills(entry map[string]any) []string {
	var out []string
	if raw, ok := entry["skills"].([]any); ok {
		for _, s := range raw {
			if sn, ok := s.(string); ok && sn != "" {
				out = append(out, sn)
			}
		}
	}
	return out
}

// WriteAgentEntry writes a per-agent agent.json into the layer-appropriate
// registry directory (AgentTargetLayer, considering the agent's source layer and
// whether its declared skills are local-only). An "instruction" string key, if
// present, is peeled off and written to instruction.md alongside (mirroring the
// web-UI editor's fan-out). Returns the agent.json path and the resolved layer.
func WriteAgentEntry(name string, entry map[string]any) (path, layer string, err error) {
	if name == "" {
		return "", "", fmt.Errorf("agent name is empty")
	}
	// Peel instruction off so it never lands inside agent.json.
	var instruction string
	if instr, ok := entry["instruction"].(string); ok {
		instruction = instr
	}
	clean := make(map[string]any, len(entry))
	for k, v := range entry {
		if k == "instruction" {
			continue
		}
		clean[k] = v
	}

	layer = AgentTargetLayer(name, AgentSkills(clean))
	dir := filepath.Join(paths.AgentsRegistryWriteDirForLayer(layer), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", fmt.Errorf("mkdir registry agent: %w", err)
	}

	// Persist only the delta against the merge of this agent's lower registry
	// layers, so a per-user edit stays minimal and package updates to the
	// agent's other fields keep flowing through.
	out, merr := AgentEntryOverlayBytes(name, layer, clean, paths.AgentsRegistrySearchDirs())
	if merr != nil {
		return "", "", fmt.Errorf("marshal agent: %w", merr)
	}
	path = filepath.Join(dir, "agent.json")
	if werr := AtomicWriteFile(path, out); werr != nil {
		return "", "", fmt.Errorf("write agent: %w", werr)
	}
	if instruction != "" {
		if werr := AtomicWriteFile(filepath.Join(dir, "instruction.md"), []byte(instruction)); werr != nil {
			return "", "", fmt.Errorf("write instruction: %w", werr)
		}
	}
	return path, layer, nil
}

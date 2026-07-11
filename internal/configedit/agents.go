package configedit

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
	// Keep real config fields; drop the instruction (saved separately) and any
	// empty scalar/null value (no override intent), so an unchanged or
	// restored-to-system agent produces an empty overlay and forks no shadow.
	clean := make(map[string]any, len(entry))
	for k, v := range entry {
		if k == "instruction" || isEmptyOverlayValue(v) {
			continue
		}
		clean[k] = v
	}

	layer = AgentTargetLayer(name, AgentSkills(clean))
	dir := filepath.Join(paths.AgentsRegistryWriteDirForLayer(layer), name)

	// Persist only the delta against the merge of this agent's lower registry
	// layers. An empty delta (agent unchanged / restored to defaults) writes
	// nothing and REMOVES any pre-existing overlay so the shipped definition is
	// used instead of a shadow copy that would shadow package updates.
	out, merr := AgentEntryOverlayBytes(name, layer, clean, paths.AgentsRegistrySearchDirs())
	if merr != nil {
		return "", "", fmt.Errorf("marshal agent: %w", merr)
	}
	writeAgent := strings.TrimSpace(string(out)) != "{}"

	// The instruction is an override only when it differs from the merged base
	// from the layers below the write layer; equal ⇒ remove any override.
	base := agentInstructionBaseBelow(name, layer)
	writeInstr := instruction != "" && strings.TrimRight(instruction, "\n") != base

	path = filepath.Join(dir, "agent.json")
	instrPath := filepath.Join(dir, "instruction.md")

	if writeAgent || writeInstr {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", "", fmt.Errorf("mkdir registry agent: %w", err)
		}
	}
	if writeAgent {
		if werr := AtomicWriteFile(path, out); werr != nil {
			return "", "", fmt.Errorf("write agent: %w", werr)
		}
	} else {
		_ = os.Remove(path) // restore-to-system: drop the overlay
	}
	if writeInstr {
		if werr := AtomicWriteFile(instrPath, []byte(instruction)); werr != nil {
			return "", "", fmt.Errorf("write instruction: %w", werr)
		}
	} else {
		_ = os.Remove(instrPath) // restore-to-system: drop the shadow
	}
	// Drop a now-empty override dir so the user layer carries nothing for a
	// package agent left at (or restored to) its shipped defaults.
	if !writeAgent && !writeInstr {
		_ = os.Remove(dir) // succeeds only when the dir is empty
	}
	return path, layer, nil
}

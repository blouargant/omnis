package registrymeta

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"

	"github.com/blouargant/omnis/agent"
	"github.com/blouargant/omnis/internal/paths"
)

// AgentInfo bundles one installed agent's on-disk record with layer
// metadata that's useful to UI clients. The Entry field carries the
// full agent.json payload so callers can read every field without
// re-parsing the file.
type AgentInfo struct {
	Entry agent.AgentEntry
	// Source is the layer the agent was found in ("local"/"user"/"system").
	Source string
	// Dir is the absolute path to the agent's directory (parent of
	// agent.json). Used to side-load instruction.md and to delete.
	Dir string
}

// Name returns the agent's name as it appears in agent.json, falling
// back to the directory name when the JSON omits it.
func (a AgentInfo) Name() string {
	if a.Entry.Name != "" {
		return a.Entry.Name
	}
	return filepath.Base(a.Dir)
}

// IsBuiltin reports whether the agent is one of the shipped builtins.
func (a AgentInfo) IsBuiltin() bool {
	return a.Entry.BuiltIn != nil && *a.Entry.BuiltIn
}

// InstructionPath returns the absolute path to the agent's
// instruction.md. The file may or may not exist; callers should stat
// before reading.
func (a AgentInfo) InstructionPath() string {
	return filepath.Join(a.Dir, "instruction.md")
}

// ListAgents scans every directory in precedence order and returns the
// merged AgentInfo records. First occurrence by directory name wins,
// matching the config-chain layering used elsewhere.
//
// Builtin agents sort before user-added ones; within each group the
// sort is alphabetical by name. A missing directory is not an error.
func ListAgents(dirs ...string) ([]AgentInfo, error) {
	seen := make(map[string]bool)
	var out []AgentInfo
	for _, registryDir := range dirs {
		entries, err := os.ReadDir(registryDir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		for _, e := range entries {
			if !e.IsDir() || seen[e.Name()] {
				continue
			}
			info, err := readAgentDir(filepath.Join(registryDir, e.Name()))
			if err != nil {
				continue
			}
			seen[e.Name()] = true
			info.Source = paths.Layer(registryDir)
			out = append(out, *info)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		bi, bj := out[i].IsBuiltin(), out[j].IsBuiltin()
		if bi != bj {
			return bi
		}
		return out[i].Name() < out[j].Name()
	})
	return out, nil
}

// ReadAgent reads <dir>/agent.json and returns an AgentInfo. The Source
// field is filled by paths.Layer(dir's parent). A missing file is an
// error.
func ReadAgent(dir string) (*AgentInfo, error) {
	info, err := readAgentDir(dir)
	if err != nil {
		return nil, err
	}
	info.Source = paths.Layer(filepath.Dir(dir))
	return info, nil
}

func readAgentDir(dir string) (*AgentInfo, error) {
	data, err := os.ReadFile(filepath.Join(dir, "agent.json"))
	if err != nil {
		return nil, err
	}
	var entry agent.AgentEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return nil, err
	}
	if entry.Name == "" {
		entry.Name = filepath.Base(dir)
	}
	return &AgentInfo{Entry: entry, Dir: dir}, nil
}

// WriteAgentEntry serialises entry as JSON and writes it to
// <dir>/agent.json (creating the directory if needed). Pretty-printed
// with two-space indent to match the on-disk convention.
func WriteAgentEntry(dir string, entry agent.AgentEntry) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(filepath.Join(dir, "agent.json"), data, 0o644)
}

// FindAgentDir returns the absolute path of <name>'s agent directory in
// the highest-precedence layer that contains it. Empty string when the
// agent is not installed anywhere.
func FindAgentDir(name string) string {
	for _, dir := range paths.AgentsRegistrySearchDirs() {
		candidate := filepath.Join(dir, name)
		if _, err := os.Stat(filepath.Join(candidate, "agent.json")); err == nil {
			return candidate
		}
	}
	return ""
}

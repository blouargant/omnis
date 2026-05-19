package registries

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// AgentManifestFile is the filename that marks an agent directory in a
// remote registry. Each agent lives in its own subdirectory with this file
// plus optional instruction.md and supporting resources.
const AgentManifestFile = "agent.json"

// agentManifest is the minimal subset of registry/agents/<name>/agent.json
// that the browser needs to surface description / builtin / tags.
type agentManifest struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Builtin     bool     `json:"builtin"`
	Tags        []string `json:"tags"`
}

// InstalledAgent is one agent currently present in the local agents registry.
type InstalledAgent struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Builtin     bool     `json:"builtin,omitempty"`
	Tags        []string `json:"tags,omitempty"`
}

// ListInstalledAgents scans the agents registry directory and returns one
// entry per installed agent, reading metadata from agent.json.
func ListInstalledAgents(agentsRegistryDir string) ([]InstalledAgent, error) {
	entries, err := os.ReadDir(agentsRegistryDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []InstalledAgent{}, nil
		}
		return nil, err
	}
	out := make([]InstalledAgent, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		data, err := os.ReadFile(filepath.Join(agentsRegistryDir, name, AgentManifestFile))
		if err != nil {
			continue
		}
		var m agentManifest
		if err := json.Unmarshal(data, &m); err != nil {
			continue
		}
		display := m.Name
		if display == "" {
			display = name
		}
		out = append(out, InstalledAgent{
			Name:        display,
			Description: m.Description,
			Builtin:     m.Builtin,
			Tags:        m.Tags,
		})
	}
	return out, nil
}

// BrowseAgents discovers all agent.json files in a remote registry. Each
// returned AgentInfo is annotated with Installed=true when a directory of
// the same name already exists under agentsRegistryDir.
func BrowseAgents(ref RepoRef, token, agentsRegistryDir string) ([]AgentInfo, error) {
	entries, err := ref.TreeEntries(token)
	if err != nil {
		return nil, err
	}

	var agents []AgentInfo
	suffix := "/" + AgentManifestFile
	for _, e := range entries {
		if e.Path == "__truncated__" {
			agents = append(agents, AgentInfo{Name: "__truncated__", DirPath: "__truncated__"})
			continue
		}
		if e.Type != "blob" || !strings.HasSuffix(e.Path, suffix) {
			continue
		}
		dirPath := strings.TrimSuffix(e.Path, suffix)
		if dirPath == "" {
			continue
		}

		slash := strings.LastIndex(dirPath, "/")
		var group, leafDir string
		if slash >= 0 {
			group, leafDir = dirPath[:slash], dirPath[slash+1:]
		} else {
			leafDir = dirPath
		}

		ag := AgentInfo{
			Name:    leafDir,
			DirPath: dirPath,
			Group:   group,
		}

		if agentsRegistryDir != "" {
			if _, err := os.Stat(filepath.Join(agentsRegistryDir, leafDir, AgentManifestFile)); err == nil {
				ag.Installed = true
			}
		}

		rawBody, status, err := ref.RawFile(e.Path, token)
		if err == nil && status == 200 {
			var m agentManifest
			if err := json.Unmarshal(rawBody, &m); err == nil {
				if m.Name != "" {
					ag.Name = m.Name
					if agentsRegistryDir != "" {
						if _, err := os.Stat(filepath.Join(agentsRegistryDir, m.Name, AgentManifestFile)); err == nil {
							ag.Installed = true
						}
					}
				}
				ag.Description = m.Description
				ag.Builtin = m.Builtin
				ag.Tags = m.Tags
			}
		}

		agents = append(agents, ag)
	}

	if agents == nil {
		agents = []AgentInfo{}
	}
	return agents, nil
}

// FetchAgentJSON returns the agent.json content at dirPath inside the registry.
func FetchAgentJSON(ref RepoRef, token, dirPath string) ([]byte, error) {
	rawBody, status, err := ref.RawFile(dirPath+"/"+AgentManifestFile, token)
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, fmt.Errorf("HTTP %d fetching %s", status, AgentManifestFile)
	}
	return rawBody, nil
}

// InstallAgent downloads the files of a remote agent at dirPath (relative to
// the registry root) and writes them under agentsRegistryDir/<agentName>.
// The agent name is taken from the agent.json `name` field when present,
// otherwise from the leaf of dirPath. Subdirectories are preserved.
// Returns the resolved agent name.
func InstallAgent(ref RepoRef, token, dirPath, agentsRegistryDir string) (string, error) {
	entries, err := ref.TreeEntries(token)
	if err != nil {
		return "", err
	}
	files := collectDirFiles(entries, dirPath)
	if len(files) == 0 {
		return "", fmt.Errorf("no files found in agent directory")
	}

	leafDir := dirPath
	if i := strings.LastIndex(dirPath, "/"); i >= 0 {
		leafDir = dirPath[i+1:]
	}
	agentName := leafDir

	for _, f := range files {
		if f.Name != AgentManifestFile {
			continue
		}
		rawBody, status, err := ref.RawFile(f.RelPath, token)
		if err == nil && status == 200 {
			var m agentManifest
			if err := json.Unmarshal(rawBody, &m); err == nil && m.Name != "" {
				agentName = m.Name
			}
		}
		break
	}

	// Reuse the skill name validation regex: same kebab/snake-case rules apply.
	if !SkillNameRe.MatchString(agentName) {
		return "", fmt.Errorf("agent name %q is not valid", agentName)
	}

	if err := os.MkdirAll(agentsRegistryDir, 0o755); err != nil {
		return "", err
	}
	agentDir := filepath.Join(agentsRegistryDir, agentName)
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		return "", err
	}

	for _, f := range files {
		rawBody, status, err := ref.RawFile(f.RelPath, token)
		if err != nil {
			return "", fmt.Errorf("download %s: %w", f.Name, err)
		}
		if status != 200 {
			return "", fmt.Errorf("download %s: HTTP %d", f.Name, status)
		}
		if int64(len(rawBody)) > MaxFileSize {
			return "", fmt.Errorf("file %s exceeds per-file size limit", f.Name)
		}
		dest := filepath.Join(agentDir, filepath.FromSlash(f.Name))
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(dest, rawBody, 0o644); err != nil {
			return "", err
		}
	}
	return agentName, nil
}

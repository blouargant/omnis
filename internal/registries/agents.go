package registries

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/blouargant/yoke/internal/claudeformat"
)

// AgentManifestFile is the filename that marks an agent directory in a
// remote registry. Each agent lives in its own subdirectory with this file
// plus optional instruction.md and supporting resources.
const AgentManifestFile = "agent.json"

// FormatClaude is the Format value set on AgentInfo entries that originate
// from Claude Code markdown files rather than native yoke agent.json files.
const FormatClaude = "claude"

// agentManifest is the minimal subset of registry/agents/<name>/agent.json
// that the browser needs to surface in the remote-listing cards.
type agentManifest struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Builtin     bool     `json:"builtin"`
	Tags        []string `json:"tags"`
	Tools       []string `json:"tools"`
	Model       string   `json:"model"`
	Skills      []string `json:"skills"`
	MCPServers  []string `json:"mcp_servers"`
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

// BrowseAgents discovers all agent.json and Claude Code .md files in a remote
// registry. Each returned AgentInfo is annotated with Installed=true when an
// agent of the same name already exists under agentsRegistryDir.
// AgentInfo.Format is set to FormatClaude for .md entries.
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
		if e.Type != "blob" {
			continue
		}

		// --- native yoke format: .../agent.json ---
		if strings.HasSuffix(e.Path, suffix) {
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

			ag := AgentInfo{Name: leafDir, DirPath: dirPath, Group: group}
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
					ag.Tools = m.Tools
					ag.Model = m.Model
					ag.Skills = m.Skills
					ag.MCPServers = m.MCPServers
				}
			}
			agents = append(agents, ag)
			continue
		}

		// --- Claude Code markdown format: <name>.md (not instruction.md) ---
		if strings.HasSuffix(e.Path, ".md") && !strings.HasSuffix(e.Path, "/instruction.md") &&
			e.Path != "instruction.md" {
			// Only fetch the file if the name (without .md) looks like a valid agent name.
			base := filepath.Base(e.Path)
			nameCandidate := strings.TrimSuffix(base, ".md")
			if !SkillNameRe.MatchString(nameCandidate) {
				continue
			}

			rawBody, status, err := ref.RawFile(e.Path, token)
			if err != nil || status != 200 {
				continue
			}
			def, err := claudeformat.ParseMarkdown(rawBody)
			if err != nil {
				continue
			}

			slash := strings.LastIndex(e.Path, "/")
			var group string
			if slash >= 0 {
				group = e.Path[:slash]
			}

			ag := AgentInfo{
				Name:        def.Name,
				DirPath:     e.Path, // single-file path (ends with .md)
				Group:       group,
				Description: def.Description,
				Format:      FormatClaude,
				Tools:       def.Tools,
				Model:       def.Model,
				Skills:      def.Skills,
				MCPServers:  def.MCPServers,
			}
			if agentsRegistryDir != "" {
				if _, err := os.Stat(filepath.Join(agentsRegistryDir, def.Name, AgentManifestFile)); err == nil {
					ag.Installed = true
				}
			}
			agents = append(agents, ag)
		}
	}

	if agents == nil {
		agents = []AgentInfo{}
	}
	return agents, nil
}

// FetchAgentJSON returns the agent definition content at dirPath inside the registry.
// For native yoke format, dirPath is a directory and agent.json is appended.
// For Claude Code format, dirPath already points to the .md file itself.
func FetchAgentJSON(ref RepoRef, token, dirPath string) ([]byte, error) {
	if strings.HasSuffix(dirPath, ".md") {
		rawBody, status, err := ref.RawFile(dirPath, token)
		if err != nil {
			return nil, err
		}
		if status != 200 {
			return nil, fmt.Errorf("HTTP %d fetching %s", status, dirPath)
		}
		return rawBody, nil
	}
	rawBody, status, err := ref.RawFile(dirPath+"/"+AgentManifestFile, token)
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, fmt.Errorf("HTTP %d fetching %s", status, AgentManifestFile)
	}
	return rawBody, nil
}

// InstallAgent downloads and installs a remote agent. dirPath is relative to
// the registry root. Two cases are handled:
//
//   - Native yoke format: dirPath is a directory containing agent.json (and
//     optionally instruction.md and other files). All files are written under
//     agentsRegistryDir/<agentName>/.
//
//   - Claude Code markdown format: dirPath ends with ".md". The single file
//     is fetched, parsed, and converted to agent.json + instruction.md.
//
// Returns the resolved agent name.
func InstallAgent(ref RepoRef, token, dirPath, agentsRegistryDir string) (string, error) {
	if strings.HasSuffix(dirPath, ".md") {
		return installClaudeFormatAgent(ref, token, dirPath, agentsRegistryDir)
	}
	return installNativeAgent(ref, token, dirPath, agentsRegistryDir)
}

// installClaudeFormatAgent installs a single Claude Code markdown file.
func installClaudeFormatAgent(ref RepoRef, token, filePath, agentsRegistryDir string) (string, error) {
	rawBody, status, err := ref.RawFile(filePath, token)
	if err != nil {
		return "", fmt.Errorf("download %s: %w", filePath, err)
	}
	if status != 200 {
		return "", fmt.Errorf("download %s: HTTP %d", filePath, status)
	}
	if int64(len(rawBody)) > MaxFileSize {
		return "", fmt.Errorf("file %s exceeds per-file size limit", filePath)
	}

	def, err := claudeformat.ParseMarkdown(rawBody)
	if err != nil {
		return "", fmt.Errorf("parse Claude Code agent: %w", err)
	}
	if !SkillNameRe.MatchString(def.Name) {
		return "", fmt.Errorf("agent name %q is not valid", def.Name)
	}

	if err := claudeformat.InstallAgent(def, agentsRegistryDir); err != nil {
		return "", err
	}
	return def.Name, nil
}

// installNativeAgent installs a directory-based yoke-native agent.
func installNativeAgent(ref RepoRef, token, dirPath, agentsRegistryDir string) (string, error) {
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

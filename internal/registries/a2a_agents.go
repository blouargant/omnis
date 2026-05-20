package registries

import (
	"encoding/json"
	"fmt"
	"strings"
)

// A2AManifestFile is the filename marking an A2A agent directory in a remote registry.
const A2AManifestFile = "a2a.json"

// a2aManifest is the display-only subset of a remote a2a.json file.
type a2aManifest struct {
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	URL         string `json:"url,omitempty"`
}

// BrowseA2AAgents discovers all a2a.json files in a remote registry tree.
// Each returned A2AAgentInfo has Installed=true when the agent name is already
// present in installedNames.
func BrowseA2AAgents(ref RepoRef, token string, installedNames map[string]bool) ([]A2AAgentInfo, error) {
	entries, err := ref.TreeEntries(token)
	if err != nil {
		return nil, err
	}

	var agents []A2AAgentInfo
	suffix := "/" + A2AManifestFile

	for _, e := range entries {
		if e.Path == "__truncated__" {
			agents = append(agents, A2AAgentInfo{Name: "__truncated__", DirPath: "__truncated__"})
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

		agent := A2AAgentInfo{Name: leafDir, DirPath: dirPath, Group: group}

		rawBody, status, err := ref.RawFile(e.Path, token)
		if err == nil && status == 200 {
			var m a2aManifest
			if err := json.Unmarshal(rawBody, &m); err == nil {
				if m.Name != "" {
					agent.Name = m.Name
				}
				agent.Description = m.Description
				agent.URL = m.URL
			}
		}

		if installedNames != nil && installedNames[agent.Name] {
			agent.Installed = true
		}

		agents = append(agents, agent)
	}

	if agents == nil {
		agents = []A2AAgentInfo{}
	}
	return agents, nil
}

// FetchA2AAgentJSON returns the a2a.json content at dirPath inside the registry.
func FetchA2AAgentJSON(ref RepoRef, token, dirPath string) ([]byte, error) {
	rawBody, status, err := ref.RawFile(dirPath+"/"+A2AManifestFile, token)
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, fmt.Errorf("HTTP %d fetching %s", status, A2AManifestFile)
	}
	return rawBody, nil
}

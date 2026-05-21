package registries

import (
	"encoding/json"
	"fmt"
	"strings"
)

// SquadManifestFile is the filename that marks a squad directory in a remote registry.
const SquadManifestFile = "squad.json"

// squadManifest is the on-disk shape of a remote squad definition.
// It mirrors agent.SquadEntry so it can be round-tripped without importing the agent package.
type squadManifest struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Leader      string   `json:"leader"`
	Members     []string `json:"members"`
}

// BrowseSquads discovers all squad.json files in a remote registry tree.
// Each squad whose name matches an entry in installedNames is annotated with Installed=true.
func BrowseSquads(ref RepoRef, token string, installedNames map[string]bool) ([]SquadInfo, error) {
	entries, err := ref.TreeEntries(token)
	if err != nil {
		return nil, err
	}

	suffix := "/" + SquadManifestFile
	var squads []SquadInfo

	for _, e := range entries {
		if e.Path == "__truncated__" {
			squads = append(squads, SquadInfo{Name: "__truncated__", DirPath: "__truncated__"})
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

		sq := SquadInfo{Name: leafDir, DirPath: dirPath, Group: group}
		rawBody, status, err := ref.RawFile(e.Path, token)
		if err == nil && status == 200 {
			var m squadManifest
			if err := json.Unmarshal(rawBody, &m); err == nil {
				if m.Name != "" {
					sq.Name = m.Name
				}
				sq.Description = m.Description
				sq.Leader = m.Leader
				sq.Members = m.Members
			}
		}
		if installedNames[sq.Name] {
			sq.Installed = true
		}
		squads = append(squads, sq)
	}

	if squads == nil {
		squads = []SquadInfo{}
	}
	return squads, nil
}

// FetchSquadJSON returns the squad.json content at dirPath inside the registry.
func FetchSquadJSON(ref RepoRef, token, dirPath string) ([]byte, error) {
	rawBody, status, err := ref.RawFile(dirPath+"/"+SquadManifestFile, token)
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, fmt.Errorf("HTTP %d fetching %s", status, SquadManifestFile)
	}
	return rawBody, nil
}

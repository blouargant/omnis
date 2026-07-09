package registries

import (
	"encoding/json"
	"fmt"
	"path"
	"strings"

	"github.com/blouargant/omnis/internal/claudeformat"
)

// MCPManifestFile is the preferred JSON filename for an MCP server manifest in a remote registry.
// Any *.json file in a directory is considered as a fallback, but only files that
// actually declare an MCP server (a stdio `command` or an http `url`) are listed
// — see BrowseMCPTools.
const MCPManifestFile = "mcp.json"

// MCPMarkdownFile is the markdown filename for an MCP server manifest.
// When present in a directory it takes priority over MCPManifestFile and any other *.json file.
// The file uses YAML frontmatter (name, description, command, args, env, type, url, skills)
// and a markdown body that is served as the tool's documentation.
const MCPMarkdownFile = "mcp.md"

// mcpManifest is the subset of a remote manifest file read while browsing.
// Command/URL are read only to validate that the file actually declares an MCP
// server (a stdio command or an http url) — the full Server fields are passed
// through verbatim to mcp_config.json when installing.
type mcpManifest struct {
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	Type        string `json:"type,omitempty"` // "stdio" | "http"
	Command     string `json:"command,omitempty"`
	URL         string `json:"url,omitempty"`
}

// BrowseMCPTools discovers MCP server directories in a remote registry tree.
// A directory is recognized as an MCP server when it contains a manifest that
// declares a transport — mcp.md (YAML frontmatter) or a *.json with a stdio
// `command` or http `url` (mcp.json preferred when several json files exist).
// Files that don't declare a server (plugin/group metadata, package.json, …) or
// that belong to another kind's directory are skipped, so a multi-kind registry
// doesn't list bogus MCP entries. MCPToolInfo.DirPath is the full path to the
// chosen manifest file so the install/fetch routes can download it without
// guessing the filename.
func BrowseMCPTools(ref RepoRef, token string, installedNames map[string]bool) ([]MCPToolInfo, error) {
	entries, err := ref.TreeEntries(token)
	if err != nil {
		return nil, err
	}

	// Pass 1: for each directory, pick the best manifest file and note README presence.
	// Priority: mcp.md > mcp.json > any other *.json; first-seen wins within a tier.
	dirBest := map[string]string{}  // dirPath -> chosen filePath
	readmeDirs := map[string]bool{} // dirPath -> has README.md
	for _, e := range entries {
		if e.Type != "blob" {
			continue
		}
		// In a multi-kind registry the permissive "any *.json" match would
		// otherwise pick up agent.json / squad.json / a2a.json / permissions.json
		// or files under another kind's directory — skip those.
		if belongsToForeignKind(e.Path, KindMCP) {
			continue
		}
		dir := path.Dir(e.Path)
		if dir == "." || dir == "" {
			continue
		}
		base := path.Base(e.Path)
		if base == "README.md" {
			readmeDirs[dir] = true
			continue
		}
		// mcp.md has the highest priority — always overrides any json entry.
		if base == MCPMarkdownFile {
			dirBest[dir] = e.Path
			continue
		}
		if !strings.HasSuffix(e.Path, ".json") {
			continue
		}
		existing, seen := dirBest[dir]
		if !seen {
			dirBest[dir] = e.Path
		} else if path.Base(existing) != MCPMarkdownFile {
			// existing is a .json entry; mcp.json beats other .json names
			if base == MCPManifestFile {
				dirBest[dir] = e.Path
			}
		}
		// if existing is mcp.md, do not override
	}

	// Pass 2: emit results in tree order.
	seenDirs := map[string]bool{}
	var tools []MCPToolInfo

	for _, e := range entries {
		if e.Path == "__truncated__" {
			tools = append(tools, MCPToolInfo{Name: "__truncated__", DirPath: "__truncated__"})
			continue
		}
		if e.Type != "blob" {
			continue
		}
		if belongsToForeignKind(e.Path, KindMCP) {
			continue
		}
		isMd := path.Base(e.Path) == MCPMarkdownFile
		if !isMd && !strings.HasSuffix(e.Path, ".json") {
			continue
		}
		dir := path.Dir(e.Path)
		if dir == "." || dir == "" || seenDirs[dir] {
			continue
		}
		if dirBest[dir] != e.Path {
			continue // not the chosen file for this directory
		}
		seenDirs[dir] = true

		slash := strings.LastIndex(dir, "/")
		var group, leafDir string
		if slash >= 0 {
			group, leafDir = dir[:slash], dir[slash+1:]
		} else {
			leafDir = dir
		}

		// DirPath is the full path to the manifest file so the server can fetch it directly.
		tool := MCPToolInfo{Name: leafDir, DirPath: e.Path, Group: group, HasReadme: readmeDirs[dir]}

		// Confirm the file actually declares an MCP server (a stdio `command` or
		// an http `url`) before listing it. The "any *.json" match would
		// otherwise surface arbitrary JSON — plugin/group metadata,
		// package.json, tsconfig.json, … — as bogus MCP servers.
		rawBody, status, fetchErr := ref.RawFile(e.Path, token)
		if fetchErr != nil || status != 200 {
			continue // can't verify it's a manifest — don't list an unverified entry
		}
		isMCP := false
		if isMd {
			def, parseErr := claudeformat.ParseMCPMarkdown(rawBody)
			if parseErr == nil && (def.Command != "" || def.URL != "") {
				isMCP = true
				if def.Name != "" {
					tool.Name = def.Name
				}
				tool.Description = def.Description
				if def.Type != "" {
					tool.Type = def.Type
				}
				if def.Body != "" {
					tool.HasReadme = true
				}
			}
		} else {
			var m mcpManifest
			if jsonErr := json.Unmarshal(rawBody, &m); jsonErr == nil && (m.Command != "" || m.URL != "") {
				isMCP = true
				if m.Name != "" {
					tool.Name = m.Name
				}
				tool.Description = m.Description
				tool.Type = m.Type
			}
		}
		if !isMCP {
			continue
		}

		if installedNames != nil && installedNames[tool.Name] {
			tool.Installed = true
		}

		tools = append(tools, tool)
	}

	if tools == nil {
		tools = []MCPToolInfo{}
	}
	return tools, nil
}

// FetchMCPManifest returns the raw manifest content at filePath inside the registry.
// filePath may be:
//   - a full path to a JSON or markdown file (fetched directly)
//   - a bare directory path (appends mcp.json for backward compatibility)
func FetchMCPManifest(ref RepoRef, token, filePath string) ([]byte, error) {
	target := filePath
	if !strings.HasSuffix(filePath, ".json") && !strings.HasSuffix(filePath, ".md") {
		target = filePath + "/" + MCPManifestFile
	}
	rawBody, status, err := ref.RawFile(target, token)
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, fmt.Errorf("HTTP %d fetching %s", status, target)
	}
	return rawBody, nil
}

// FetchMCPToolJSON is an alias of FetchMCPManifest kept for backward compatibility.
func FetchMCPToolJSON(ref RepoRef, token, filePath string) ([]byte, error) {
	return FetchMCPManifest(ref, token, filePath)
}

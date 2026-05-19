package registries

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// GitHubRef points at a GitHub repository tree.
type GitHubRef struct {
	Owner  string
	Repo   string
	Branch string
	Path   string // subdirectory within the repo that serves as the root
}

var ghTreeRe = regexp.MustCompile(`^https?://github\.com/([^/]+)/([^/]+)/tree/([^/]+)/?(.*)$`)
var ghRepoRe = regexp.MustCompile(`^https?://github\.com/([^/]+)/([^/]+)/?$`)

func parseGitHubRef(rawURL string) (GitHubRef, error) {
	rawURL = strings.TrimRight(rawURL, "/")
	if m := ghTreeRe.FindStringSubmatch(rawURL); m != nil {
		return GitHubRef{
			Owner:  m[1],
			Repo:   m[2],
			Branch: m[3],
			Path:   strings.Trim(m[4], "/"),
		}, nil
	}
	if m := ghRepoRe.FindStringSubmatch(rawURL); m != nil {
		return GitHubRef{Owner: m[1], Repo: m[2], Branch: "main"}, nil
	}
	return GitHubRef{}, fmt.Errorf("expected GitHub URL: https://github.com/{owner}/{repo}[/tree/{branch}/{path}]")
}

func (g GitHubRef) ProviderName() string { return ProviderGitHub }

func (g GitHubRef) AutoName() string {
	n := g.Owner + "/" + g.Repo
	if g.Path != "" {
		n += "/" + g.Path
	}
	return n
}

func (g GitHubRef) TreeEntries(token string) ([]TreeEntry, error) {
	treeURL := fmt.Sprintf(
		"https://api.github.com/repos/%s/%s/git/trees/%s?recursive=1",
		g.Owner, g.Repo, g.Branch,
	)
	body, err := ghAPIGet(treeURL, token)
	if err != nil {
		return nil, err
	}

	var treeResp struct {
		Tree []struct {
			Path string `json:"path"`
			Type string `json:"type"`
		} `json:"tree"`
		Truncated bool `json:"truncated"`
	}
	if err := json.Unmarshal(body, &treeResp); err != nil {
		return nil, fmt.Errorf("parse git tree: %w", err)
	}

	rootPrefix := g.Path
	if rootPrefix != "" {
		rootPrefix += "/"
	}

	var entries []TreeEntry
	for _, item := range treeResp.Tree {
		if rootPrefix != "" && !strings.HasPrefix(item.Path, rootPrefix) {
			continue
		}
		rel := strings.TrimPrefix(item.Path, rootPrefix)
		entries = append(entries, TreeEntry{Path: rel, Type: item.Type})
	}

	if treeResp.Truncated {
		entries = append(entries, TreeEntry{Path: "__truncated__"})
	}
	return entries, nil
}

func (g GitHubRef) RawFile(relPath, token string) ([]byte, int, error) {
	p := g.Path
	if relPath != "" {
		if p != "" {
			p = p + "/" + relPath
		} else {
			p = relPath
		}
	}
	rawURL := fmt.Sprintf(
		"https://raw.githubusercontent.com/%s/%s/%s/%s",
		g.Owner, g.Repo, g.Branch, p,
	)
	return rawGet(rawURL, ghHeaders(token))
}

func (g GitHubRef) DirFiles(dirPath, token string) ([]InstallableFile, error) {
	p := g.Path
	if dirPath != "" {
		if p != "" {
			p = p + "/" + dirPath
		} else {
			p = dirPath
		}
	}
	contentsURL := fmt.Sprintf(
		"https://api.github.com/repos/%s/%s/contents/%s?ref=%s",
		g.Owner, g.Repo, p, g.Branch,
	)
	body, err := ghAPIGet(contentsURL, token)
	if err != nil {
		return nil, err
	}
	var entries []struct {
		Name string `json:"name"`
		Type string `json:"type"`
	}
	if err := json.Unmarshal(body, &entries); err != nil {
		return nil, fmt.Errorf("parse skill directory: %w", err)
	}
	var files []InstallableFile
	for _, e := range entries {
		if e.Type != "file" {
			continue
		}
		rel := e.Name
		if dirPath != "" {
			rel = dirPath + "/" + e.Name
		}
		files = append(files, InstallableFile{Name: e.Name, RelPath: rel})
	}
	return files, nil
}

func ghHeaders(token string) map[string]string {
	h := map[string]string{
		"Accept":               "application/vnd.github+json",
		"X-GitHub-Api-Version": "2022-11-28",
	}
	if token != "" {
		h["Authorization"] = "Bearer " + token
	}
	return h
}

func ghAPIGet(rawURL, token string) ([]byte, error) {
	body, status, err := rawGet(rawURL, ghHeaders(token))
	if err != nil {
		return nil, err
	}
	switch status {
	case 200:
		return body, nil
	case 403, 429:
		return nil, fmt.Errorf("GitHub API rate limit exceeded (HTTP %d) — add a GitHub token for this registry", status)
	case 404:
		return nil, fmt.Errorf("not found on GitHub (check the URL and branch name)")
	default:
		msg := strings.TrimSpace(string(body))
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return nil, fmt.Errorf("GitHub API error HTTP %d: %s", status, msg)
	}
}

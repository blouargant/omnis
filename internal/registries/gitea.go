package registries

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// GiteaRef points at a Gitea repository tree.
type GiteaRef struct {
	Host   string
	Owner  string
	Repo   string
	Branch string
	Path   string
}

var giteaTreeRe = regexp.MustCompile(`^https?://([^/]+)/([^/]+)/([^/]+)/src/branch/([^/]+)/?(.*)$`)
var giteaRepoRe = regexp.MustCompile(`^https?://([^/]+)/([^/]+)/([^/]+)/?$`)

func parseGiteaRef(rawURL string) (GiteaRef, error) {
	rawURL = strings.TrimRight(rawURL, "/")
	if m := giteaTreeRe.FindStringSubmatch(rawURL); m != nil {
		return GiteaRef{
			Host:   m[1],
			Owner:  m[2],
			Repo:   m[3],
			Branch: m[4],
			Path:   strings.Trim(m[5], "/"),
		}, nil
	}
	if m := giteaRepoRe.FindStringSubmatch(rawURL); m != nil {
		return GiteaRef{Host: m[1], Owner: m[2], Repo: m[3], Branch: "main"}, nil
	}
	return GiteaRef{}, fmt.Errorf("expected Gitea URL: https://gitea.example.com/{owner}/{repo}[/src/branch/{branch}/{path}]")
}

func (g GiteaRef) giteaHeaders(token string) map[string]string {
	h := map[string]string{}
	if token != "" {
		h["Authorization"] = "token " + token
	}
	return h
}

func (g GiteaRef) ProviderName() string { return ProviderGitea }

func (g GiteaRef) AutoName() string {
	n := g.Owner + "/" + g.Repo
	if g.Path != "" {
		n += "/" + g.Path
	}
	return n
}

func (g GiteaRef) TreeEntries(token string) ([]TreeEntry, error) {
	branchURL := fmt.Sprintf(
		"https://%s/api/v1/repos/%s/%s/branches/%s",
		g.Host, g.Owner, g.Repo, url.PathEscape(g.Branch),
	)
	body, status, err := rawGet(branchURL, g.giteaHeaders(token))
	if err != nil {
		return nil, err
	}
	if status == 404 {
		return nil, fmt.Errorf("Gitea: branch not found")
	}
	if status == 401 {
		return nil, fmt.Errorf("Gitea: unauthorized")
	}
	if status != 200 {
		return nil, fmt.Errorf("Gitea API error HTTP %d fetching branch", status)
	}
	var branchResp struct {
		Commit struct {
			ID string `json:"id"`
		} `json:"commit"`
	}
	if err := json.Unmarshal(body, &branchResp); err != nil {
		return nil, fmt.Errorf("parse Gitea branch: %w", err)
	}
	sha := branchResp.Commit.ID

	treeURL := fmt.Sprintf(
		"https://%s/api/v1/repos/%s/%s/git/trees/%s?recursive=1",
		g.Host, g.Owner, g.Repo, sha,
	)
	body, status, err = rawGet(treeURL, g.giteaHeaders(token))
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, fmt.Errorf("Gitea API error HTTP %d fetching tree", status)
	}
	var treeResp struct {
		Tree []struct {
			Path string `json:"path"`
			Type string `json:"type"`
		} `json:"tree"`
	}
	if err := json.Unmarshal(body, &treeResp); err != nil {
		return nil, fmt.Errorf("parse Gitea tree: %w", err)
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
	return entries, nil
}

func (g GiteaRef) RawFile(relPath, token string) ([]byte, int, error) {
	fullPath := relPath
	if g.Path != "" {
		fullPath = g.Path + "/" + relPath
	}
	rawURL := fmt.Sprintf(
		"https://%s/api/v1/repos/%s/%s/raw/%s?ref=%s",
		g.Host, g.Owner, g.Repo, fullPath, url.QueryEscape(g.Branch),
	)
	return rawGet(rawURL, g.giteaHeaders(token))
}

func (g GiteaRef) DirFiles(dirPath, token string) ([]InstallableFile, error) {
	fullDir := dirPath
	if g.Path != "" {
		fullDir = g.Path + "/" + dirPath
	}
	contentsURL := fmt.Sprintf(
		"https://%s/api/v1/repos/%s/%s/contents/%s?ref=%s",
		g.Host, g.Owner, g.Repo, fullDir, url.QueryEscape(g.Branch),
	)
	body, status, err := rawGet(contentsURL, g.giteaHeaders(token))
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, fmt.Errorf("Gitea API error HTTP %d", status)
	}
	var items []struct {
		Name string `json:"name"`
		Type string `json:"type"`
	}
	if err := json.Unmarshal(body, &items); err != nil {
		return nil, fmt.Errorf("parse Gitea dir: %w", err)
	}
	var files []InstallableFile
	for _, item := range items {
		if item.Type != "file" {
			continue
		}
		rel := item.Name
		if dirPath != "" {
			rel = dirPath + "/" + item.Name
		}
		files = append(files, InstallableFile{Name: item.Name, RelPath: rel})
	}
	return files, nil
}

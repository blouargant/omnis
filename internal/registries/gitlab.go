package registries

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// GitLabRef points at a GitLab repository tree.
type GitLabRef struct {
	Host        string
	ProjectPath string
	Branch      string
	Path        string
}

var glTreeRe = regexp.MustCompile(`^https?://([^/]+)/(.+?)/-/tree/([^/]+)/?(.*)$`)
var glRepoRe = regexp.MustCompile(`^https?://([^/]+)/(.+)$`)

func parseGitLabRef(rawURL string) (GitLabRef, error) {
	rawURL = strings.TrimRight(rawURL, "/")
	if m := glTreeRe.FindStringSubmatch(rawURL); m != nil {
		return GitLabRef{
			Host:        m[1],
			ProjectPath: m[2],
			Branch:      m[3],
			Path:        strings.Trim(m[4], "/"),
		}, nil
	}
	if m := glRepoRe.FindStringSubmatch(rawURL); m != nil {
		return GitLabRef{Host: m[1], ProjectPath: m[2], Branch: "main"}, nil
	}
	return GitLabRef{}, fmt.Errorf("expected GitLab URL: https://gitlab.com/{project/path}[/-/tree/{branch}/{path}]")
}

func (g GitLabRef) glHeaders(token string) map[string]string {
	h := map[string]string{}
	if token != "" {
		h["PRIVATE-TOKEN"] = token
	}
	return h
}

func (g GitLabRef) apiBase() string {
	return fmt.Sprintf("https://%s/api/v4", g.Host)
}

func (g GitLabRef) projectID() string {
	return url.PathEscape(g.ProjectPath)
}

func (g GitLabRef) ProviderName() string { return ProviderGitLab }

func (g GitLabRef) AutoName() string {
	if g.Path != "" {
		return g.ProjectPath + "/" + g.Path
	}
	return g.ProjectPath
}

func (g GitLabRef) TreeEntries(token string) ([]TreeEntry, error) {
	rootPrefix := g.Path
	if rootPrefix != "" {
		rootPrefix += "/"
	}

	var entries []TreeEntry
	for page := 1; page <= 20; page++ {
		apiURL := fmt.Sprintf(
			"%s/projects/%s/repository/tree?recursive=true&ref=%s&per_page=100&page=%d",
			g.apiBase(), g.projectID(), url.QueryEscape(g.Branch), page,
		)
		if g.Path != "" {
			apiURL += "&path=" + url.QueryEscape(g.Path)
		}
		body, status, err := rawGet(apiURL, g.glHeaders(token))
		if err != nil {
			return nil, err
		}
		if status == 401 {
			return nil, fmt.Errorf("GitLab: unauthorized (check token)")
		}
		if status == 404 {
			return nil, fmt.Errorf("GitLab: repository not found")
		}
		if status != 200 {
			msg := strings.TrimSpace(string(body))
			if len(msg) > 300 {
				msg = msg[:300]
			}
			return nil, fmt.Errorf("GitLab API error HTTP %d: %s", status, msg)
		}

		var items []struct {
			Name string `json:"name"`
			Type string `json:"type"`
			Path string `json:"path"`
		}
		if err := json.Unmarshal(body, &items); err != nil {
			return nil, fmt.Errorf("parse GitLab tree: %w", err)
		}

		for _, item := range items {
			rel := strings.TrimPrefix(item.Path, rootPrefix)
			entries = append(entries, TreeEntry{Path: rel, Type: item.Type})
		}

		if len(items) < 100 {
			break
		}
	}
	return entries, nil
}

func (g GitLabRef) RawFile(relPath, token string) ([]byte, int, error) {
	fullPath := relPath
	if g.Path != "" {
		fullPath = g.Path + "/" + relPath
	}
	encoded := url.PathEscape(fullPath)
	apiURL := fmt.Sprintf(
		"%s/projects/%s/repository/files/%s/raw?ref=%s",
		g.apiBase(), g.projectID(), encoded, url.QueryEscape(g.Branch),
	)
	return rawGet(apiURL, g.glHeaders(token))
}

func (g GitLabRef) DirFiles(dirPath, token string) ([]InstallableFile, error) {
	fullDir := dirPath
	if g.Path != "" {
		fullDir = g.Path + "/" + dirPath
	}
	apiURL := fmt.Sprintf(
		"%s/projects/%s/repository/tree?path=%s&ref=%s&per_page=100",
		g.apiBase(), g.projectID(), url.QueryEscape(fullDir), url.QueryEscape(g.Branch),
	)
	body, status, err := rawGet(apiURL, g.glHeaders(token))
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, fmt.Errorf("GitLab API error HTTP %d", status)
	}
	var items []struct {
		Name string `json:"name"`
		Type string `json:"type"`
	}
	if err := json.Unmarshal(body, &items); err != nil {
		return nil, fmt.Errorf("parse GitLab dir: %w", err)
	}
	var files []InstallableFile
	for _, item := range items {
		if item.Type != "blob" {
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

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
)

// ── Provider constants ────────────────────────────────────────────────────────

const (
	ProviderGitHub = "github"
	ProviderGitLab = "gitlab"
	ProviderGitea  = "gitea"
)

// remoteRegistry is a user-configured remote skill source.
type remoteRegistry struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	URL      string `json:"url"`
	Provider string `json:"provider,omitempty"`
	Token    string `json:"token,omitempty"` // PAT; stored server-side, never sent to browser
}

// remoteSkillInfo is a skill entry returned when browsing a remote registry.
type remoteSkillInfo struct {
	Name        string   `json:"name"`
	DirPath     string   `json:"dir_path"`        // path relative to registry root, e.g. "engineering/diagnose"
	Group       string   `json:"group,omitempty"` // intermediate dirs before the skill dir, e.g. "engineering"
	Description string   `json:"description,omitempty"`
	Author      string   `json:"author,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Installed   bool     `json:"installed"`
}

// ── Generic provider interface ────────────────────────────────────────────────

type repoRef interface {
	providerName() string
	autoName() string
	treeEntries(token string) ([]treeEntry, error)
	rawFile(relPath, token string) ([]byte, int, error)
	dirFiles(dirPath, token string) ([]installableFile, error)
}

type treeEntry struct {
	Path string // relative to registry root
	Type string // "blob" or "tree"
}

type installableFile struct {
	Name    string // filename only
	RelPath string // path relative to registry root (dirPath + "/" + Name)
}

// ── Generic HTTP helper ───────────────────────────────────────────────────────

// rawGet performs a generic HTTP GET. headers map is applied as-is.
func rawGet(rawURL string, headers map[string]string) ([]byte, int, error) {
	req, err := http.NewRequest("GET", rawURL, nil)
	if err != nil {
		return nil, 0, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	return body, resp.StatusCode, nil
}

// ── GitHub provider ───────────────────────────────────────────────────────────

type githubRef struct {
	Owner  string
	Repo   string
	Branch string
	Path   string // subdirectory within the repo that serves as the root
}

var ghTreeRe = regexp.MustCompile(`^https?://github\.com/([^/]+)/([^/]+)/tree/([^/]+)/?(.*)$`)
var ghRepoRe = regexp.MustCompile(`^https?://github\.com/([^/]+)/([^/]+)/?$`)

func parseGitHubRef(rawURL string) (githubRef, error) {
	rawURL = strings.TrimRight(rawURL, "/")
	if m := ghTreeRe.FindStringSubmatch(rawURL); m != nil {
		return githubRef{
			Owner:  m[1],
			Repo:   m[2],
			Branch: m[3],
			Path:   strings.Trim(m[4], "/"),
		}, nil
	}
	if m := ghRepoRe.FindStringSubmatch(rawURL); m != nil {
		return githubRef{Owner: m[1], Repo: m[2], Branch: "main"}, nil
	}
	return githubRef{}, fmt.Errorf("expected GitHub URL: https://github.com/{owner}/{repo}[/tree/{branch}/{path}]")
}

func (g githubRef) providerName() string { return ProviderGitHub }

func (g githubRef) autoName() string {
	n := g.Owner + "/" + g.Repo
	if g.Path != "" {
		n += "/" + g.Path
	}
	return n
}

func (g githubRef) treeEntries(token string) ([]treeEntry, error) {
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
			Type string `json:"type"` // "blob" or "tree"
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

	var entries []treeEntry
	for _, item := range treeResp.Tree {
		if rootPrefix != "" && !strings.HasPrefix(item.Path, rootPrefix) {
			continue
		}
		rel := strings.TrimPrefix(item.Path, rootPrefix)
		entries = append(entries, treeEntry{Path: rel, Type: item.Type})
	}

	if treeResp.Truncated {
		entries = append(entries, treeEntry{Path: "__truncated__"})
	}
	return entries, nil
}

func (g githubRef) rawFile(relPath, token string) ([]byte, int, error) {
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

func (g githubRef) dirFiles(dirPath, token string) ([]installableFile, error) {
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
		Type string `json:"type"` // "file" | "dir"
	}
	if err := json.Unmarshal(body, &entries); err != nil {
		return nil, fmt.Errorf("parse skill directory: %w", err)
	}
	var files []installableFile
	for _, e := range entries {
		if e.Type != "file" {
			continue
		}
		rel := e.Name
		if dirPath != "" {
			rel = dirPath + "/" + e.Name
		}
		files = append(files, installableFile{Name: e.Name, RelPath: rel})
	}
	return files, nil
}

// ghHeaders returns the standard GitHub API request headers.
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

// ghAPIGet performs a GitHub API GET and returns a descriptive error for non-200 responses.
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

// ── GitLab provider ───────────────────────────────────────────────────────────

type gitlabRef struct {
	Host        string // e.g. "gitlab.com" or "gitlab.mycompany.com"
	ProjectPath string // e.g. "group/subgroup/repo"
	Branch      string
	Path        string // registry root (may be empty)
}

var glTreeRe = regexp.MustCompile(`^https?://([^/]+)/(.+?)/-/tree/([^/]+)/?(.*)$`)
var glRepoRe = regexp.MustCompile(`^https?://([^/]+)/(.+)$`)

func parseGitLabRef(rawURL string) (gitlabRef, error) {
	rawURL = strings.TrimRight(rawURL, "/")
	if m := glTreeRe.FindStringSubmatch(rawURL); m != nil {
		return gitlabRef{
			Host:        m[1],
			ProjectPath: m[2],
			Branch:      m[3],
			Path:        strings.Trim(m[4], "/"),
		}, nil
	}
	if m := glRepoRe.FindStringSubmatch(rawURL); m != nil {
		return gitlabRef{Host: m[1], ProjectPath: m[2], Branch: "main"}, nil
	}
	return gitlabRef{}, fmt.Errorf("expected GitLab URL: https://gitlab.com/{project/path}[/-/tree/{branch}/{path}]")
}

func (g gitlabRef) glHeaders(token string) map[string]string {
	h := map[string]string{}
	if token != "" {
		h["PRIVATE-TOKEN"] = token
	}
	return h
}

func (g gitlabRef) apiBase() string {
	return fmt.Sprintf("https://%s/api/v4", g.Host)
}

func (g gitlabRef) projectID() string {
	return url.PathEscape(g.ProjectPath)
}

func (g gitlabRef) providerName() string { return ProviderGitLab }

func (g gitlabRef) autoName() string {
	if g.Path != "" {
		return g.ProjectPath + "/" + g.Path
	}
	return g.ProjectPath
}

func (g gitlabRef) treeEntries(token string) ([]treeEntry, error) {
	rootPrefix := g.Path
	if rootPrefix != "" {
		rootPrefix += "/"
	}

	var entries []treeEntry
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
			Type string `json:"type"` // "blob" or "tree"
			Path string `json:"path"` // full repo path
		}
		if err := json.Unmarshal(body, &items); err != nil {
			return nil, fmt.Errorf("parse GitLab tree: %w", err)
		}

		for _, item := range items {
			rel := strings.TrimPrefix(item.Path, rootPrefix)
			entries = append(entries, treeEntry{Path: rel, Type: item.Type})
		}

		if len(items) < 100 {
			break
		}
	}
	return entries, nil
}

func (g gitlabRef) rawFile(relPath, token string) ([]byte, int, error) {
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

func (g gitlabRef) dirFiles(dirPath, token string) ([]installableFile, error) {
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
		Type string `json:"type"` // "blob" or "tree"
	}
	if err := json.Unmarshal(body, &items); err != nil {
		return nil, fmt.Errorf("parse GitLab dir: %w", err)
	}
	var files []installableFile
	for _, item := range items {
		if item.Type != "blob" {
			continue
		}
		rel := item.Name
		if dirPath != "" {
			rel = dirPath + "/" + item.Name
		}
		files = append(files, installableFile{Name: item.Name, RelPath: rel})
	}
	return files, nil
}

// ── Gitea provider ────────────────────────────────────────────────────────────

type giteaRef struct {
	Host   string
	Owner  string
	Repo   string
	Branch string
	Path   string
}

var giteaTreeRe = regexp.MustCompile(`^https?://([^/]+)/([^/]+)/([^/]+)/src/branch/([^/]+)/?(.*)$`)
var giteaRepoRe = regexp.MustCompile(`^https?://([^/]+)/([^/]+)/([^/]+)/?$`)

func parseGiteaRef(rawURL string) (giteaRef, error) {
	rawURL = strings.TrimRight(rawURL, "/")
	if m := giteaTreeRe.FindStringSubmatch(rawURL); m != nil {
		return giteaRef{
			Host:   m[1],
			Owner:  m[2],
			Repo:   m[3],
			Branch: m[4],
			Path:   strings.Trim(m[5], "/"),
		}, nil
	}
	if m := giteaRepoRe.FindStringSubmatch(rawURL); m != nil {
		return giteaRef{Host: m[1], Owner: m[2], Repo: m[3], Branch: "main"}, nil
	}
	return giteaRef{}, fmt.Errorf("expected Gitea URL: https://gitea.example.com/{owner}/{repo}[/src/branch/{branch}/{path}]")
}

func (g giteaRef) giteaHeaders(token string) map[string]string {
	h := map[string]string{}
	if token != "" {
		h["Authorization"] = "token " + token
	}
	return h
}

func (g giteaRef) providerName() string { return ProviderGitea }

func (g giteaRef) autoName() string {
	n := g.Owner + "/" + g.Repo
	if g.Path != "" {
		n += "/" + g.Path
	}
	return n
}

func (g giteaRef) treeEntries(token string) ([]treeEntry, error) {
	// Step 1: fetch branch SHA.
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

	// Step 2: recursive tree.
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
			Type string `json:"type"` // "blob" or "tree"
		} `json:"tree"`
	}
	if err := json.Unmarshal(body, &treeResp); err != nil {
		return nil, fmt.Errorf("parse Gitea tree: %w", err)
	}

	rootPrefix := g.Path
	if rootPrefix != "" {
		rootPrefix += "/"
	}

	var entries []treeEntry
	for _, item := range treeResp.Tree {
		if rootPrefix != "" && !strings.HasPrefix(item.Path, rootPrefix) {
			continue
		}
		rel := strings.TrimPrefix(item.Path, rootPrefix)
		entries = append(entries, treeEntry{Path: rel, Type: item.Type})
	}
	return entries, nil
}

func (g giteaRef) rawFile(relPath, token string) ([]byte, int, error) {
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

func (g giteaRef) dirFiles(dirPath, token string) ([]installableFile, error) {
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
		Type string `json:"type"` // "file" or "dir"
	}
	if err := json.Unmarshal(body, &items); err != nil {
		return nil, fmt.Errorf("parse Gitea dir: %w", err)
	}
	var files []installableFile
	for _, item := range items {
		if item.Type != "file" {
			continue
		}
		rel := item.Name
		if dirPath != "" {
			rel = dirPath + "/" + item.Name
		}
		files = append(files, installableFile{Name: item.Name, RelPath: rel})
	}
	return files, nil
}

// ── URL auto-detection ────────────────────────────────────────────────────────

// parseRepoRef parses a repository URL into a repoRef for the given provider.
// If provider is empty, the provider is auto-detected from the URL.
func parseRepoRef(rawURL, provider string) (repoRef, error) {
	switch provider {
	case ProviderGitHub:
		return parseGitHubRef(rawURL)
	case ProviderGitLab:
		return parseGitLabRef(rawURL)
	case ProviderGitea:
		return parseGiteaRef(rawURL)
	}
	// Auto-detect.
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}
	host := u.Hostname()
	if host == "github.com" {
		return parseGitHubRef(rawURL)
	}
	if host == "gitlab.com" || strings.Contains(u.Path, "/-/tree/") {
		return parseGitLabRef(rawURL)
	}
	if strings.Contains(u.Path, "/src/branch/") {
		return parseGiteaRef(rawURL)
	}
	return nil, fmt.Errorf("cannot auto-detect provider for %q — set provider to github, gitlab, or gitea", rawURL)
}

// ── Remote registry config persistence ───────────────────────────────────────

func loadRemoteRegistries(path string) ([]remoteRegistry, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return []remoteRegistry{}, nil
	}
	if err != nil {
		return nil, err
	}
	var list []remoteRegistry
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, err
	}
	return list, nil
}

func saveRemoteRegistries(path string, list []remoteRegistry) error {
	if list == nil {
		list = []remoteRegistry{}
	}
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return atomicWriteFile(path, data)
}

// newRemoteID generates a short random hex ID.
func newRemoteID() string {
	b := make([]byte, 4)
	f, _ := os.Open("/dev/urandom")
	if f != nil {
		_, _ = io.ReadFull(f, b)
		f.Close()
	}
	return fmt.Sprintf("%08x", b)
}

// ── Browse ────────────────────────────────────────────────────────────────────

// browseRemoteSkills discovers all SKILL.md files in the remote registry using
// the provider's tree API and fetches their frontmatter via raw file access.
func browseRemoteSkills(ref repoRef, token, registryDir string) ([]remoteSkillInfo, error) {
	entries, err := ref.treeEntries(token)
	if err != nil {
		return nil, err
	}

	var skills []remoteSkillInfo
	for _, e := range entries {
		if e.Path == "__truncated__" {
			skills = append(skills, remoteSkillInfo{Name: "__truncated__", DirPath: "__truncated__"})
			continue
		}
		if e.Type != "blob" || !strings.HasSuffix(e.Path, "/SKILL.md") {
			continue
		}
		dirPath := strings.TrimSuffix(e.Path, "/SKILL.md")
		if dirPath == "" {
			continue // SKILL.md directly at root — skip
		}

		slash := strings.LastIndex(dirPath, "/")
		var group, leafDir string
		if slash >= 0 {
			group, leafDir = dirPath[:slash], dirPath[slash+1:]
		} else {
			leafDir = dirPath
		}

		sk := remoteSkillInfo{
			Name:    leafDir,
			DirPath: dirPath,
			Group:   group,
		}

		// Check if already installed (by leaf dir name).
		if _, err := os.Stat(filepath.Join(registryDir, leafDir, "SKILL.md")); err == nil {
			sk.Installed = true
		}

		// Fetch SKILL.md to read frontmatter.
		rawBody, status, err := ref.rawFile(e.Path, token)
		if err == nil && status == 200 {
			fm, _ := parseSkillFrontmatter(rawBody)
			if fm.Name != "" {
				sk.Name = fm.Name
				// Re-check installed with the canonical name from frontmatter.
				if _, err := os.Stat(filepath.Join(registryDir, fm.Name, "SKILL.md")); err == nil {
					sk.Installed = true
				}
			}
			sk.Description = fm.Description
			sk.Author = fm.author()
			sk.Tags = fm.tags()
		}

		skills = append(skills, sk)
	}

	if skills == nil {
		skills = []remoteSkillInfo{}
	}
	return skills, nil
}

// ── Install ───────────────────────────────────────────────────────────────────

// installRemoteSkill downloads files from a skill directory (at dirPath relative
// to the registry root) and installs them under registryDir. Returns the skill name.
func installRemoteSkill(ref repoRef, token, dirPath, registryDir string) (string, error) {
	files, err := ref.dirFiles(dirPath, token)
	if err != nil {
		return "", err
	}
	if len(files) == 0 {
		return "", fmt.Errorf("no files found in skill directory")
	}

	leafDir := dirPath
	if i := strings.LastIndex(dirPath, "/"); i >= 0 {
		leafDir = dirPath[i+1:]
	}
	skillName := leafDir

	// Determine canonical skill name from SKILL.md frontmatter.
	for _, f := range files {
		if f.Name != "SKILL.md" {
			continue
		}
		rawBody, status, err := ref.rawFile(f.RelPath, token)
		if err == nil && status == 200 {
			fm, _ := parseSkillFrontmatter(rawBody)
			if fm.Name != "" {
				skillName = fm.Name
			}
		}
		break
	}

	if !skillNameRe.MatchString(skillName) {
		return "", fmt.Errorf("skill name %q is not valid", skillName)
	}

	skillDir := filepath.Join(registryDir, skillName)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		return "", err
	}

	for _, f := range files {
		rawBody, status, err := ref.rawFile(f.RelPath, token)
		if err != nil {
			return "", fmt.Errorf("download %s: %w", f.Name, err)
		}
		if status != 200 {
			return "", fmt.Errorf("download %s: HTTP %d", f.Name, status)
		}
		if int64(len(rawBody)) > maxUploadPerFile {
			return "", fmt.Errorf("file %s exceeds per-file size limit", f.Name)
		}
		if err := os.WriteFile(filepath.Join(skillDir, f.Name), rawBody, 0o644); err != nil {
			return "", err
		}
	}
	return skillName, nil
}

// ── Route registration ────────────────────────────────────────────────────────

// registerRemoteRegistryRoutes mounts the /remotes endpoints on rg.
func registerRemoteRegistryRoutes(rg *gin.RouterGroup, cfgPath, registryDir string) {
	type publicRemote struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		URL      string `json:"url"`
		Provider string `json:"provider,omitempty"`
		HasToken bool   `json:"has_token"`
	}

	// GET /remotes — list configured remotes without exposing tokens.
	rg.GET("/remotes", func(c *gin.Context) {
		list, err := loadRemoteRegistries(cfgPath)
		if err != nil {
			c.JSON(http.StatusInternalServerError, skillsErr("FS_ERROR", err.Error()))
			return
		}
		out := make([]publicRemote, len(list))
		for i, r := range list {
			out[i] = publicRemote{ID: r.ID, Name: r.Name, URL: r.URL, Provider: r.Provider, HasToken: r.Token != ""}
		}
		c.JSON(http.StatusOK, gin.H{"remotes": out})
	})

	// POST /remotes — add a remote registry.
	rg.POST("/remotes", func(c *gin.Context) {
		var req struct {
			URL      string `json:"url"`
			Name     string `json:"name"`
			Provider string `json:"provider"`
			Token    string `json:"token"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, skillsErr("BAD_REQUEST", "invalid JSON"))
			return
		}
		rawURL := strings.TrimSpace(req.URL)
		if rawURL == "" {
			c.JSON(http.StatusBadRequest, skillsErr("BAD_REQUEST", "url is required"))
			return
		}
		provider := strings.TrimSpace(req.Provider)
		ref, err := parseRepoRef(rawURL, provider)
		if err != nil {
			c.JSON(http.StatusBadRequest, skillsErr("BAD_URL", err.Error()))
			return
		}
		list, err := loadRemoteRegistries(cfgPath)
		if err != nil {
			c.JSON(http.StatusInternalServerError, skillsErr("FS_ERROR", err.Error()))
			return
		}
		for _, r := range list {
			if r.URL == rawURL {
				c.JSON(http.StatusConflict, skillsErr("DUPLICATE", "a registry with this URL already exists"))
				return
			}
		}
		name := strings.TrimSpace(req.Name)
		if name == "" {
			name = ref.autoName()
		}
		reg := remoteRegistry{
			ID:       newRemoteID(),
			Name:     name,
			URL:      rawURL,
			Provider: provider,
			Token:    strings.TrimSpace(req.Token),
		}
		list = append(list, reg)
		if err := saveRemoteRegistries(cfgPath, list); err != nil {
			c.JSON(http.StatusInternalServerError, skillsErr("FS_ERROR", err.Error()))
			return
		}
		c.JSON(http.StatusCreated, publicRemote{ID: reg.ID, Name: reg.Name, URL: reg.URL, Provider: reg.Provider, HasToken: reg.Token != ""})
	})

	// PUT /remotes/:id — update a registry entry.
	rg.PUT("/remotes/:id", func(c *gin.Context) {
		id := c.Param("id")
		var req struct {
			Name     string `json:"name"`
			URL      string `json:"url"`
			Provider string `json:"provider"`
			Token    string `json:"token"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, skillsErr("BAD_REQUEST", "invalid JSON"))
			return
		}
		list, err := loadRemoteRegistries(cfgPath)
		if err != nil {
			c.JSON(http.StatusInternalServerError, skillsErr("FS_ERROR", err.Error()))
			return
		}
		var reg *remoteRegistry
		for i := range list {
			if list[i].ID == id {
				reg = &list[i]
				break
			}
		}
		if reg == nil {
			c.JSON(http.StatusNotFound, skillsErr("NOT_FOUND", "registry not found"))
			return
		}
		// Update URL if provided (validate first).
		if newURL := strings.TrimSpace(req.URL); newURL != "" {
			if _, err := parseRepoRef(newURL, strings.TrimSpace(req.Provider)); err != nil {
				c.JSON(http.StatusBadRequest, skillsErr("BAD_URL", err.Error()))
				return
			}
			reg.URL = newURL
		}
		// Update name if provided.
		if newName := strings.TrimSpace(req.Name); newName != "" {
			reg.Name = newName
		}
		// Always update provider (empty = auto).
		reg.Provider = strings.TrimSpace(req.Provider)
		// Update token only if a new value is provided.
		if newToken := strings.TrimSpace(req.Token); newToken != "" {
			reg.Token = newToken
		}
		if err := saveRemoteRegistries(cfgPath, list); err != nil {
			c.JSON(http.StatusInternalServerError, skillsErr("FS_ERROR", err.Error()))
			return
		}
		c.Status(http.StatusNoContent)
	})

	// DELETE /remotes/:id — remove a registry.
	rg.DELETE("/remotes/:id", func(c *gin.Context) {
		id := c.Param("id")
		list, err := loadRemoteRegistries(cfgPath)
		if err != nil {
			c.JSON(http.StatusInternalServerError, skillsErr("FS_ERROR", err.Error()))
			return
		}
		newList := list[:0]
		found := false
		for _, r := range list {
			if r.ID == id {
				found = true
			} else {
				newList = append(newList, r)
			}
		}
		if !found {
			c.JSON(http.StatusNotFound, skillsErr("NOT_FOUND", "registry not found"))
			return
		}
		if err := saveRemoteRegistries(cfgPath, newList); err != nil {
			c.JSON(http.StatusInternalServerError, skillsErr("FS_ERROR", err.Error()))
			return
		}
		c.Status(http.StatusNoContent)
	})

	// GET /remotes/:id/browse — fetch skill list using the provider's tree API.
	rg.GET("/remotes/:id/browse", func(c *gin.Context) {
		id := c.Param("id")
		list, err := loadRemoteRegistries(cfgPath)
		if err != nil {
			c.JSON(http.StatusInternalServerError, skillsErr("FS_ERROR", err.Error()))
			return
		}
		var reg *remoteRegistry
		for i := range list {
			if list[i].ID == id {
				reg = &list[i]
				break
			}
		}
		if reg == nil {
			c.JSON(http.StatusNotFound, skillsErr("NOT_FOUND", "registry not found"))
			return
		}
		ref, err := parseRepoRef(reg.URL, reg.Provider)
		if err != nil {
			c.JSON(http.StatusBadRequest, skillsErr("BAD_URL", err.Error()))
			return
		}
		skills, err := browseRemoteSkills(ref, reg.Token, registryDir)
		if err != nil {
			c.JSON(http.StatusBadGateway, skillsErr("REMOTE_ERROR", err.Error()))
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"skills":   skills,
			"registry": publicRemote{ID: reg.ID, Name: reg.Name, URL: reg.URL, Provider: reg.Provider, HasToken: reg.Token != ""},
		})
	})

	// GET /remotes/:id/skill/*dirpath — fetch raw SKILL.md content for a remote skill.
	rg.GET("/remotes/:id/skill/*dirpath", func(c *gin.Context) {
		id := c.Param("id")
		dirPath := strings.Trim(c.Param("dirpath"), "/")
		if dirPath == "" {
			c.JSON(http.StatusBadRequest, skillsErr("BAD_REQUEST", "dirpath is required"))
			return
		}
		list, err := loadRemoteRegistries(cfgPath)
		if err != nil {
			c.JSON(http.StatusInternalServerError, skillsErr("FS_ERROR", err.Error()))
			return
		}
		var reg *remoteRegistry
		for i := range list {
			if list[i].ID == id {
				reg = &list[i]
				break
			}
		}
		if reg == nil {
			c.JSON(http.StatusNotFound, skillsErr("NOT_FOUND", "registry not found"))
			return
		}
		ref, err := parseRepoRef(reg.URL, reg.Provider)
		if err != nil {
			c.JSON(http.StatusBadRequest, skillsErr("BAD_URL", err.Error()))
			return
		}
		rawBody, status, err := ref.rawFile(dirPath+"/SKILL.md", reg.Token)
		if err != nil {
			c.JSON(http.StatusBadGateway, skillsErr("REMOTE_ERROR", err.Error()))
			return
		}
		if status != 200 {
			c.JSON(http.StatusBadGateway, skillsErr("REMOTE_ERROR", fmt.Sprintf("HTTP %d fetching SKILL.md", status)))
			return
		}
		c.JSON(http.StatusOK, gin.H{"content": string(rawBody)})
	})

	// POST /remotes/:id/install/*dirpath — download and install a skill.
	rg.POST("/remotes/:id/install/*dirpath", func(c *gin.Context) {
		id := c.Param("id")
		dirPath := strings.Trim(c.Param("dirpath"), "/")
		if dirPath == "" {
			c.JSON(http.StatusBadRequest, skillsErr("BAD_REQUEST", "dirpath is required"))
			return
		}
		list, err := loadRemoteRegistries(cfgPath)
		if err != nil {
			c.JSON(http.StatusInternalServerError, skillsErr("FS_ERROR", err.Error()))
			return
		}
		var reg *remoteRegistry
		for i := range list {
			if list[i].ID == id {
				reg = &list[i]
				break
			}
		}
		if reg == nil {
			c.JSON(http.StatusNotFound, skillsErr("NOT_FOUND", "registry not found"))
			return
		}
		ref, err := parseRepoRef(reg.URL, reg.Provider)
		if err != nil {
			c.JSON(http.StatusBadRequest, skillsErr("BAD_URL", err.Error()))
			return
		}
		if err := os.MkdirAll(registryDir, 0o755); err != nil {
			c.JSON(http.StatusInternalServerError, skillsErr("FS_ERROR", err.Error()))
			return
		}
		skillName, err := installRemoteSkill(ref, reg.Token, dirPath, registryDir)
		if err != nil {
			c.JSON(http.StatusBadGateway, skillsErr("INSTALL_ERROR", err.Error()))
			return
		}
		c.JSON(http.StatusCreated, gin.H{"name": skillName})
	})
}

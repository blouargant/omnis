package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gopkg.in/yaml.v3"

	"github.com/blouargant/agent-toolkit/agent"
)

const (
	maxUploadCompressed   = 5 << 20  // 5 MiB
	maxUploadUncompressed = 50 << 20 // 50 MiB
	maxUploadPerFile      = 10 << 20 // 10 MiB
	maxUploadEntries      = 2000
)

var skillNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// skillsDeps holds resolved filesystem paths for skills management.
type skillsDeps struct {
	RegistryDir         string // abs path to skills-registry/installed/
	AgentYAML           string // abs path to agent.yaml (re-read on each request for freshness)
	RemoteRegistriesCfg string // abs path to config/remote_registries.json
}

// resolveSkillsDeps derives skills paths from the already-resolved config files.
func resolveSkillsDeps(cfgFiles configFiles) skillsDeps {
	registryDir := "skills-registry/installed"
	if v := os.Getenv("GOAGENT_SKILLS_REGISTRY_DIR"); v != "" {
		registryDir = v
	}
	absReg, _ := filepath.Abs(registryDir)
	cfgDir := filepath.Dir(cfgFiles.Agent)
	absRemoteCfg, _ := filepath.Abs(filepath.Join(cfgDir, "remote_registries.json"))
	return skillsDeps{
		RegistryDir:         absReg,
		AgentYAML:           cfgFiles.Agent,
		RemoteRegistriesCfg: absRemoteCfg,
	}
}

// ── Frontmatter ────────────────────────────────────────────────────────────

type skillFrontmatter struct {
	Name        string                 `yaml:"name"`
	Description string                 `yaml:"description"`
	Metadata    map[string]interface{} `yaml:"metadata"`
}

func (fm skillFrontmatter) author() string {
	if fm.Metadata == nil {
		return ""
	}
	s, _ := fm.Metadata["author"].(string)
	return s
}

func (fm skillFrontmatter) tags() []string {
	if fm.Metadata == nil {
		return nil
	}
	raw, ok := fm.Metadata["tags"]
	if !ok {
		return nil
	}
	switch v := raw.(type) {
	case []string:
		return v
	case []interface{}:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// parseSkillFrontmatter extracts YAML front matter (--- ... ---) from a SKILL.md.
func parseSkillFrontmatter(content []byte) (skillFrontmatter, error) {
	s := string(content)
	s = strings.TrimLeft(s, "\r\n")
	if !strings.HasPrefix(s, "---") {
		return skillFrontmatter{}, fmt.Errorf("no YAML frontmatter (missing opening ---)")
	}
	rest := s[3:]
	idx := strings.Index(rest, "\n---")
	if idx < 0 {
		return skillFrontmatter{}, fmt.Errorf("unclosed frontmatter (missing closing ---)")
	}
	var fm skillFrontmatter
	if err := yaml.Unmarshal([]byte(rest[:idx]), &fm); err != nil {
		return skillFrontmatter{}, fmt.Errorf("invalid frontmatter YAML: %w", err)
	}
	return fm, nil
}

// ── Registry helpers ───────────────────────────────────────────────────────

type skillInfo struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Author      string                 `json:"author,omitempty"`
	Tags        []string               `json:"tags,omitempty"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
	MTime       time.Time              `json:"mtime"`
	Size        int64                  `json:"size"`
	LinkedIn    []string               `json:"linked_in"`
}

// listRegistrySkills scans registryDir and returns metadata for each skill.
func listRegistrySkills(registryDir string) ([]skillInfo, error) {
	entries, err := os.ReadDir(registryDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []skillInfo{}, nil
		}
		return nil, err
	}
	var out []skillInfo
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		skillMD := filepath.Join(registryDir, name, "SKILL.md")
		data, err := os.ReadFile(skillMD)
		if err != nil {
			continue
		}
		st, _ := os.Stat(skillMD)
		fm, _ := parseSkillFrontmatter(data)
		displayName := fm.Name
		if displayName == "" {
			displayName = name
		}
		info := skillInfo{
			Name:        displayName,
			Description: fm.Description,
			Author:      fm.author(),
			Tags:        fm.tags(),
			Metadata:    fm.Metadata,
			Size:        int64(len(data)),
			LinkedIn:    []string{},
		}
		if st != nil {
			info.MTime = st.ModTime()
		}
		out = append(out, info)
	}
	if out == nil {
		return []skillInfo{}, nil
	}
	return out, nil
}

// listAgentLinks returns (linked, broken) symlink names in agentSkillsDir.
func listAgentLinks(agentSkillsDir string) (linked, broken []string) {
	if agentSkillsDir == "" {
		return []string{}, []string{}
	}
	entries, err := os.ReadDir(agentSkillsDir)
	if err != nil {
		return []string{}, []string{}
	}
	for _, e := range entries {
		if e.Type()&os.ModeSymlink == 0 {
			continue
		}
		name := e.Name()
		target := filepath.Join(agentSkillsDir, name)
		if _, err := os.Stat(target); err != nil {
			broken = append(broken, name)
		} else {
			linked = append(linked, name)
		}
	}
	if linked == nil {
		linked = []string{}
	}
	if broken == nil {
		broken = []string{}
	}
	return linked, broken
}

// agentHasSkillsTool reports whether the agent's tools list includes "skills".
// For leader, an empty tools list means all tools enabled (including skills).
func agentHasSkillsTool(a agent.RuntimeAgentConfig) bool {
	if a.Name == "leader" {
		return len(a.Tools) == 0 || slices.Contains(a.Tools, "skills")
	}
	return slices.Contains(a.Tools, "skills")
}

// readRuntimeAgents re-reads agent.yaml and returns the resolved agent list.
func readRuntimeAgents(agentYAML string) ([]agent.RuntimeAgentConfig, error) {
	s, err := agent.ResolveRuntimeSettings(agent.Options{
		ConfigPath:       agentYAML,
		ConfigPathStrict: true,
	})
	if err != nil {
		return nil, err
	}
	return s.Agents, nil
}

// validatedAgent looks up an agent by name from the live config.
func validatedAgent(agentYAML, name string) (agent.RuntimeAgentConfig, error) {
	agents, err := readRuntimeAgents(agentYAML)
	if err != nil {
		return agent.RuntimeAgentConfig{}, err
	}
	for _, a := range agents {
		if a.Name == name {
			return a, nil
		}
	}
	return agent.RuntimeAgentConfig{}, fmt.Errorf("unknown agent %q", name)
}

// ── Path safety ────────────────────────────────────────────────────────────

// safeJoin joins base and name, rejecting path traversal attempts.
func safeJoin(base, name string) (string, error) {
	if filepath.IsAbs(name) {
		return "", fmt.Errorf("absolute path not allowed")
	}
	clean := filepath.Clean(name)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path traversal not allowed")
	}
	result := filepath.Join(base, clean)
	base = filepath.Clean(base)
	if result != base && !strings.HasPrefix(result, base+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes base dir")
	}
	return result, nil
}

// ── Archive extraction ─────────────────────────────────────────────────────

func detectArchiveFormat(data []byte) string {
	if len(data) >= 4 && data[0] == 'P' && data[1] == 'K' && data[2] == 0x03 && data[3] == 0x04 {
		return "zip"
	}
	if len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b {
		return "tgz"
	}
	return ""
}

// extractSkillArchive extracts a zip or tar.gz archive to a fresh temp dir,
// validates its structure, and returns (tempDir, skillName).
// The caller must remove tempDir when done (even on success, after installing).
func extractSkillArchive(data []byte) (tempDir, skillName string, err error) {
	format := detectArchiveFormat(data)
	if format == "" {
		return "", "", fmt.Errorf("unsupported format: must be .zip or .tar.gz")
	}

	tmp, err := os.MkdirTemp("", "skill-upload-*")
	if err != nil {
		return "", "", fmt.Errorf("create temp dir: %w", err)
	}
	defer func() {
		if err != nil {
			os.RemoveAll(tmp)
		}
	}()

	var totalUncompressed int64
	entryCount := 0

	writeEntry := func(destPath string, r io.Reader) error {
		entryCount++
		if entryCount > maxUploadEntries {
			return fmt.Errorf("archive exceeds %d entries", maxUploadEntries)
		}
		if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
			return err
		}
		f, err := os.Create(destPath)
		if err != nil {
			return err
		}
		defer f.Close()
		written, err := io.Copy(f, io.LimitReader(r, maxUploadPerFile+1))
		if err != nil {
			return err
		}
		if written > maxUploadPerFile {
			return fmt.Errorf("file exceeds %d MiB uncompressed", maxUploadPerFile>>20)
		}
		totalUncompressed += written
		if totalUncompressed > maxUploadUncompressed {
			return fmt.Errorf("archive exceeds %d MiB uncompressed total", maxUploadUncompressed>>20)
		}
		return nil
	}

	switch format {
	case "zip":
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return "", "", fmt.Errorf("invalid zip: %w", err)
		}
		for _, f := range zr.File {
			if f.Mode()&os.ModeSymlink != 0 {
				continue
			}
			destPath, err := safeJoin(tmp, f.Name)
			if err != nil {
				return "", "", fmt.Errorf("archive entry %q: %w", f.Name, err)
			}
			if f.FileInfo().IsDir() {
				_ = os.MkdirAll(destPath, 0o755)
				continue
			}
			rc, err := f.Open()
			if err != nil {
				return "", "", err
			}
			writeErr := writeEntry(destPath, rc)
			rc.Close()
			if writeErr != nil {
				return "", "", writeErr
			}
		}

	case "tgz":
		gr, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return "", "", fmt.Errorf("invalid gzip: %w", err)
		}
		defer gr.Close()
		tr := tar.NewReader(gr)
		for {
			hdr, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return "", "", fmt.Errorf("read tar: %w", err)
			}
			if hdr.Typeflag == tar.TypeSymlink || hdr.Typeflag == tar.TypeLink {
				continue
			}
			destPath, err := safeJoin(tmp, hdr.Name)
			if err != nil {
				return "", "", fmt.Errorf("archive entry %q: %w", hdr.Name, err)
			}
			if hdr.Typeflag == tar.TypeDir {
				_ = os.MkdirAll(destPath, 0o755)
				continue
			}
			if hdr.Typeflag != tar.TypeReg {
				continue
			}
			if err := writeEntry(destPath, tr); err != nil {
				return "", "", err
			}
		}
	}

	name, err := detectSkillRoot(tmp)
	if err != nil {
		return "", "", err
	}
	return tmp, name, nil
}

// detectSkillRoot locates and normalises the skill root inside the extracted temp dir.
// Accepted layouts:
//
//	<tmp>/SKILL.md                   (flat; name from frontmatter)
//	<tmp>/<skillname>/SKILL.md       (rooted; name from dir, must match frontmatter)
func detectSkillRoot(tmp string) (string, error) {
	// Flat layout: SKILL.md at top level.
	if data, err := os.ReadFile(filepath.Join(tmp, "SKILL.md")); err == nil {
		fm, err := parseSkillFrontmatter(data)
		if err != nil {
			return "", fmt.Errorf("SKILL.md: %w", err)
		}
		if fm.Name == "" {
			return "", fmt.Errorf("SKILL.md frontmatter must declare 'name'")
		}
		if !skillNameRe.MatchString(fm.Name) {
			return "", fmt.Errorf("skill name %q must match ^[a-z0-9][a-z0-9._-]{0,63}$", fm.Name)
		}
		// Move everything into a named subdir.
		subdir := filepath.Join(tmp, fm.Name)
		if err := os.MkdirAll(subdir, 0o755); err != nil {
			return "", err
		}
		entries, _ := os.ReadDir(tmp)
		for _, e := range entries {
			if e.Name() == fm.Name {
				continue
			}
			_ = os.Rename(filepath.Join(tmp, e.Name()), filepath.Join(subdir, e.Name()))
		}
		return fm.Name, nil
	}

	// Rooted layout: single top-level directory.
	entries, err := os.ReadDir(tmp)
	if err != nil {
		return "", err
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			dirs = append(dirs, e.Name())
		}
	}
	if len(dirs) != 1 {
		return "", fmt.Errorf("archive must contain exactly one root directory or a SKILL.md at the top level")
	}
	dirName := dirs[0]
	if !skillNameRe.MatchString(dirName) {
		return "", fmt.Errorf("root directory %q must match the skill name pattern ^[a-z0-9][a-z0-9._-]{0,63}$", dirName)
	}
	data, err := os.ReadFile(filepath.Join(tmp, dirName, "SKILL.md"))
	if err != nil {
		return "", fmt.Errorf("SKILL.md not found inside archive root %q", dirName)
	}
	fm, err := parseSkillFrontmatter(data)
	if err != nil {
		return "", fmt.Errorf("SKILL.md: %w", err)
	}
	if fm.Name != "" && fm.Name != dirName {
		return "", fmt.Errorf("SKILL.md declares name=%q but root directory is named %q — they must match", fm.Name, dirName)
	}
	return dirName, nil
}

// ── Resource listing ───────────────────────────────────────────────────────

// collectResources returns relative paths of files under references/, assets/, scripts/.
func collectResources(skillDir string) []string {
	var out []string
	for _, sub := range []string{"references", "assets", "scripts"} {
		entries, err := os.ReadDir(filepath.Join(skillDir, sub))
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				out = append(out, sub+"/"+e.Name())
			}
		}
	}
	if out == nil {
		return []string{}
	}
	return out
}

// ── Error helper ───────────────────────────────────────────────────────────

func skillsErr(code, msg string) gin.H {
	return gin.H{"error": msg, "code": code}
}

func skillsErrDetail(code, msg string, details gin.H) gin.H {
	return gin.H{"error": msg, "code": code, "details": details}
}

// ── Route registration ─────────────────────────────────────────────────────

func registerSkillsRoutes(rg *gin.RouterGroup, deps skillsDeps) {
	// ── Registry ──────────────────────────────────────────────────────────

	// GET /registry — list all installed skills with linked_in annotation.
	rg.GET("/registry", func(c *gin.Context) {
		skills, err := listRegistrySkills(deps.RegistryDir)
		if err != nil {
			c.JSON(http.StatusInternalServerError, skillsErr("FS_ERROR", err.Error()))
			return
		}
		// Annotate linked_in per skill.
		agents, _ := readRuntimeAgents(deps.AgentYAML)
		for i, sk := range skills {
			for _, a := range agents {
				if a.SkillsDir == "" {
					continue
				}
				dir, _ := filepath.Abs(a.SkillsDir)
				fi, err := os.Lstat(filepath.Join(dir, sk.Name))
				if err == nil && fi.Mode()&os.ModeSymlink != 0 {
					skills[i].LinkedIn = append(skills[i].LinkedIn, a.Name)
				}
			}
			if skills[i].LinkedIn == nil {
				skills[i].LinkedIn = []string{}
			}
		}
		c.JSON(http.StatusOK, gin.H{"skills": skills})
	})

	// POST /registry/upload — upload a zip or tar.gz archive. Registered before
	// the /:name wildcard so Gin's static-beats-wildcard priority applies.
	rg.POST("/registry/upload", func(c *gin.Context) {
		// Enforce size before multipart parsing.
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxUploadCompressed+1)
		file, header, err := c.Request.FormFile("file")
		if err != nil {
			c.JSON(http.StatusBadRequest, skillsErr("BAD_REQUEST", "missing 'file' field in multipart form"))
			return
		}
		defer file.Close()
		if header.Size > maxUploadCompressed {
			c.JSON(http.StatusRequestEntityTooLarge,
				skillsErr("TOO_LARGE", fmt.Sprintf("upload exceeds %d MiB", maxUploadCompressed>>20)))
			return
		}
		data, err := io.ReadAll(io.LimitReader(file, maxUploadCompressed+1))
		if err != nil {
			c.JSON(http.StatusBadRequest, skillsErr("READ_ERROR", err.Error()))
			return
		}
		overwrite := c.Query("overwrite") == "1"

		tmp, name, err := extractSkillArchive(data)
		if err != nil {
			c.JSON(http.StatusBadRequest, skillsErr("EXTRACT_ERROR", err.Error()))
			return
		}
		defer os.RemoveAll(tmp)

		target := filepath.Join(deps.RegistryDir, name)
		if _, err := os.Stat(target); err == nil && !overwrite {
			c.JSON(http.StatusConflict,
				skillsErr("NAME_TAKEN", fmt.Sprintf("skill %q already exists; add ?overwrite=1 to replace", name)))
			return
		}
		if err := os.MkdirAll(deps.RegistryDir, 0o755); err != nil {
			c.JSON(http.StatusInternalServerError, skillsErr("FS_ERROR", err.Error()))
			return
		}
		if overwrite {
			old := target + fmt.Sprintf(".old-%d", time.Now().UnixNano())
			_ = os.Rename(target, old)
			defer os.RemoveAll(old)
		}
		if err := os.Rename(filepath.Join(tmp, name), target); err != nil {
			c.JSON(http.StatusInternalServerError, skillsErr("FS_ERROR", err.Error()))
			return
		}
		c.JSON(http.StatusCreated, gin.H{"name": name})
	})

	// GET /registry/:name — get SKILL.md content + resource listing.
	rg.GET("/registry/:name", func(c *gin.Context) {
		name := c.Param("name")
		if !skillNameRe.MatchString(name) {
			c.JSON(http.StatusBadRequest, skillsErr("NAME_INVALID", "invalid skill name"))
			return
		}
		skillMD := filepath.Join(deps.RegistryDir, name, "SKILL.md")
		data, err := os.ReadFile(skillMD)
		if err != nil {
			if os.IsNotExist(err) {
				c.JSON(http.StatusNotFound, skillsErr("NOT_FOUND", "skill not found"))
				return
			}
			c.JSON(http.StatusInternalServerError, skillsErr("FS_ERROR", err.Error()))
			return
		}
		st, _ := os.Stat(skillMD)
		fm, _ := parseSkillFrontmatter(data)
		var mtime time.Time
		if st != nil {
			mtime = st.ModTime()
		}
		// Collect linked agents.
		agents, _ := readRuntimeAgents(deps.AgentYAML)
		var linkedIn []string
		for _, a := range agents {
			if a.SkillsDir == "" {
				continue
			}
			dir, _ := filepath.Abs(a.SkillsDir)
			fi, err := os.Lstat(filepath.Join(dir, name))
			if err == nil && fi.Mode()&os.ModeSymlink != 0 {
				linkedIn = append(linkedIn, a.Name)
			}
		}
		if linkedIn == nil {
			linkedIn = []string{}
		}
		c.JSON(http.StatusOK, gin.H{
			"name":        firstNonEmpty(fm.Name, name),
			"description": fm.Description,
			"author":      fm.author(),
			"tags":        fm.tags(),
			"metadata":    fm.Metadata,
			"content":     string(data),
			"mtime":       mtime,
			"resources":   collectResources(filepath.Join(deps.RegistryDir, name)),
			"linked_in":   linkedIn,
		})
	})

	// POST /registry — create a new skill with an empty SKILL.md skeleton.
	rg.POST("/registry", func(c *gin.Context) {
		var req struct {
			Name    string `json:"name"`
			Content string `json:"content"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, skillsErr("BAD_REQUEST", "invalid JSON body"))
			return
		}
		name := strings.TrimSpace(req.Name)
		if !skillNameRe.MatchString(name) {
			c.JSON(http.StatusBadRequest, skillsErr("NAME_INVALID", "skill name must match ^[a-z0-9][a-z0-9._-]{0,63}$"))
			return
		}
		skillDir := filepath.Join(deps.RegistryDir, name)
		if _, err := os.Stat(skillDir); err == nil {
			c.JSON(http.StatusConflict, skillsErr("NAME_TAKEN", fmt.Sprintf("skill %q already exists", name)))
			return
		}
		content := strings.TrimSpace(req.Content)
		if content == "" {
			content = fmt.Sprintf("---\nname: %s\ndescription: \n---\n\n# %s\n", name, name)
		}
		if err := os.MkdirAll(skillDir, 0o755); err != nil {
			c.JSON(http.StatusInternalServerError, skillsErr("FS_ERROR", err.Error()))
			return
		}
		if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
			c.JSON(http.StatusInternalServerError, skillsErr("FS_ERROR", err.Error()))
			return
		}
		c.JSON(http.StatusCreated, gin.H{"name": name})
	})

	// PUT /registry/:name — update SKILL.md content (optimistic concurrency via mtime).
	rg.PUT("/registry/:name", func(c *gin.Context) {
		name := c.Param("name")
		if !skillNameRe.MatchString(name) {
			c.JSON(http.StatusBadRequest, skillsErr("NAME_INVALID", "invalid skill name"))
			return
		}
		var req struct {
			Content string     `json:"content"`
			MTime   *time.Time `json:"mtime,omitempty"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, skillsErr("BAD_REQUEST", "invalid JSON body"))
			return
		}
		skillMD := filepath.Join(deps.RegistryDir, name, "SKILL.md")
		if _, err := os.Stat(skillMD); os.IsNotExist(err) {
			c.JSON(http.StatusNotFound, skillsErr("NOT_FOUND", "skill not found"))
			return
		}
		if status, body := checkMtime(skillMD, req.MTime); status != 0 {
			c.JSON(status, body)
			return
		}
		if err := atomicWriteFile(skillMD, []byte(req.Content)); err != nil {
			c.JSON(http.StatusInternalServerError, skillsErr("FS_ERROR", err.Error()))
			return
		}
		st, _ := os.Stat(skillMD)
		var mtime time.Time
		if st != nil {
			mtime = st.ModTime()
		}
		c.JSON(http.StatusOK, gin.H{"name": name, "mtime": mtime})
	})

	// DELETE /registry/:name — delete a skill; refuse if still linked unless ?force=1.
	rg.DELETE("/registry/:name", func(c *gin.Context) {
		name := c.Param("name")
		if !skillNameRe.MatchString(name) {
			c.JSON(http.StatusBadRequest, skillsErr("NAME_INVALID", "invalid skill name"))
			return
		}
		skillDir := filepath.Join(deps.RegistryDir, name)
		if _, err := os.Stat(skillDir); os.IsNotExist(err) {
			c.JSON(http.StatusNotFound, skillsErr("NOT_FOUND", "skill not found"))
			return
		}
		force := c.Query("force") == "1"
		agents, _ := readRuntimeAgents(deps.AgentYAML)
		var linkedAgents []string
		for _, a := range agents {
			if a.SkillsDir == "" {
				continue
			}
			dir, _ := filepath.Abs(a.SkillsDir)
			fi, err := os.Lstat(filepath.Join(dir, name))
			if err == nil && fi.Mode()&os.ModeSymlink != 0 {
				linkedAgents = append(linkedAgents, a.Name)
			}
		}
		if len(linkedAgents) > 0 && !force {
			c.JSON(http.StatusConflict, skillsErrDetail("LINKED_IN_AGENTS",
				"skill is still used by one or more agents",
				gin.H{"agents": linkedAgents}))
			return
		}
		// Remove links first when force=1.
		for _, a := range agents {
			if a.SkillsDir == "" {
				continue
			}
			dir, _ := filepath.Abs(a.SkillsDir)
			linkPath := filepath.Join(dir, name)
			fi, err := os.Lstat(linkPath)
			if err == nil && fi.Mode()&os.ModeSymlink != 0 {
				_ = os.Remove(linkPath)
			}
		}
		if err := os.RemoveAll(skillDir); err != nil {
			c.JSON(http.StatusInternalServerError, skillsErr("FS_ERROR", err.Error()))
			return
		}
		c.Status(http.StatusNoContent)
	})

	// ── Agent skill assignments ────────────────────────────────────────────

	// GET /agents — list all agents with their linked/broken skills.
	rg.GET("/agents", func(c *gin.Context) {
		agents, err := readRuntimeAgents(deps.AgentYAML)
		if err != nil {
			c.JSON(http.StatusInternalServerError, skillsErr("CONFIG_ERROR", err.Error()))
			return
		}
		type agentSkillInfo struct {
			Name          string   `json:"name"`
			SkillsDir     string   `json:"skills_dir"`
			HasSkillsTool bool     `json:"has_skills_tool"`
			Linked        []string `json:"linked"`
			Broken        []string `json:"broken"`
		}
		out := make([]agentSkillInfo, 0, len(agents))
		for _, a := range agents {
			var absDir string
			if a.SkillsDir != "" {
				absDir, _ = filepath.Abs(a.SkillsDir)
			}
			linked, broken := listAgentLinks(absDir)
			out = append(out, agentSkillInfo{
				Name:          a.Name,
				SkillsDir:     absDir,
				HasSkillsTool: agentHasSkillsTool(a),
				Linked:        linked,
				Broken:        broken,
			})
		}
		c.JSON(http.StatusOK, gin.H{"agents": out})
	})

	// POST /agents/:agent/skills/:name — create a relative symlink for one skill.
	rg.POST("/agents/:agent/skills/:name", func(c *gin.Context) {
		agentName := c.Param("agent")
		skillName := c.Param("name")
		if !skillNameRe.MatchString(skillName) {
			c.JSON(http.StatusBadRequest, skillsErr("NAME_INVALID", "invalid skill name"))
			return
		}
		a, err := validatedAgent(deps.AgentYAML, agentName)
		if err != nil {
			c.JSON(http.StatusNotFound, skillsErr("AGENT_NOT_FOUND", err.Error()))
			return
		}
		if a.SkillsDir == "" {
			c.JSON(http.StatusBadRequest, skillsErr("NO_SKILLS_DIR",
				fmt.Sprintf("agent %q has no skills_dir configured", agentName)))
			return
		}
		registrySkillDir := filepath.Join(deps.RegistryDir, skillName)
		if _, err := os.Stat(registrySkillDir); os.IsNotExist(err) {
			c.JSON(http.StatusNotFound, skillsErr("NOT_FOUND",
				fmt.Sprintf("skill %q not found in registry", skillName)))
			return
		}
		agentDir, _ := filepath.Abs(a.SkillsDir)
		if err := os.MkdirAll(agentDir, 0o755); err != nil {
			c.JSON(http.StatusInternalServerError, skillsErr("FS_ERROR", err.Error()))
			return
		}
		linkPath := filepath.Join(agentDir, skillName)
		if fi, err := os.Lstat(linkPath); err == nil {
			if fi.Mode()&os.ModeSymlink != 0 {
				c.Status(http.StatusNoContent) // already linked — idempotent
				return
			}
			c.JSON(http.StatusConflict, skillsErr("PATH_CONFLICT",
				fmt.Sprintf("%q exists and is not a symlink", linkPath)))
			return
		}
		relTarget, err := filepath.Rel(agentDir, registrySkillDir)
		if err != nil {
			c.JSON(http.StatusInternalServerError, skillsErr("FS_ERROR", err.Error()))
			return
		}
		if err := os.Symlink(relTarget, linkPath); err != nil {
			c.JSON(http.StatusInternalServerError, skillsErr("FS_ERROR", err.Error()))
			return
		}
		c.Status(http.StatusNoContent)
	})

	// DELETE /agents/:agent/skills/:name — remove a symlink.
	rg.DELETE("/agents/:agent/skills/:name", func(c *gin.Context) {
		agentName := c.Param("agent")
		skillName := c.Param("name")
		if !skillNameRe.MatchString(skillName) {
			c.JSON(http.StatusBadRequest, skillsErr("NAME_INVALID", "invalid skill name"))
			return
		}
		a, err := validatedAgent(deps.AgentYAML, agentName)
		if err != nil {
			c.JSON(http.StatusNotFound, skillsErr("AGENT_NOT_FOUND", err.Error()))
			return
		}
		if a.SkillsDir == "" {
			c.JSON(http.StatusBadRequest, skillsErr("NO_SKILLS_DIR", "agent has no skills_dir configured"))
			return
		}
		agentDir, _ := filepath.Abs(a.SkillsDir)
		linkPath := filepath.Join(agentDir, skillName)
		fi, err := os.Lstat(linkPath)
		if os.IsNotExist(err) {
			c.Status(http.StatusNoContent) // idempotent
			return
		}
		if err != nil {
			c.JSON(http.StatusInternalServerError, skillsErr("FS_ERROR", err.Error()))
			return
		}
		if fi.Mode()&os.ModeSymlink == 0 {
			c.JSON(http.StatusConflict, skillsErr("NOT_SYMLINK",
				"path exists but is not a symlink — refusing to delete"))
			return
		}
		if err := os.Remove(linkPath); err != nil {
			c.JSON(http.StatusInternalServerError, skillsErr("FS_ERROR", err.Error()))
			return
		}
		c.Status(http.StatusNoContent)
	})

	// ── Remote registries ─────────────────────────────────────────────────
	registerRemoteRegistryRoutes(rg, deps.RemoteRegistriesCfg, deps.RegistryDir)

	// POST /agents/:agent/skills — bulk action: body {"action":"all"} or {"action":"none"}.
	rg.POST("/agents/:agent/skills", func(c *gin.Context) {
		agentName := c.Param("agent")
		var req struct {
			Action string `json:"action"` // "all" | "none"
		}
		if err := c.ShouldBindJSON(&req); err != nil || (req.Action != "all" && req.Action != "none") {
			c.JSON(http.StatusBadRequest, skillsErr("BAD_REQUEST", `body must be {"action":"all"} or {"action":"none"}`))
			return
		}
		a, err := validatedAgent(deps.AgentYAML, agentName)
		if err != nil {
			c.JSON(http.StatusNotFound, skillsErr("AGENT_NOT_FOUND", err.Error()))
			return
		}
		if a.SkillsDir == "" {
			c.JSON(http.StatusBadRequest, skillsErr("NO_SKILLS_DIR", "agent has no skills_dir configured"))
			return
		}
		agentDir, _ := filepath.Abs(a.SkillsDir)

		if req.Action == "none" {
			entries, err := os.ReadDir(agentDir)
			if os.IsNotExist(err) {
				c.JSON(http.StatusOK, gin.H{"removed": []string{}})
				return
			}
			if err != nil {
				c.JSON(http.StatusInternalServerError, skillsErr("FS_ERROR", err.Error()))
				return
			}
			var removed []string
			for _, e := range entries {
				if e.Type()&os.ModeSymlink == 0 {
					continue
				}
				if err := os.Remove(filepath.Join(agentDir, e.Name())); err == nil {
					removed = append(removed, e.Name())
				}
			}
			if removed == nil {
				removed = []string{}
			}
			c.JSON(http.StatusOK, gin.H{"removed": removed})
			return
		}

		// action == "all": link every registry skill.
		skills, err := listRegistrySkills(deps.RegistryDir)
		if err != nil {
			c.JSON(http.StatusInternalServerError, skillsErr("FS_ERROR", err.Error()))
			return
		}
		if err := os.MkdirAll(agentDir, 0o755); err != nil {
			c.JSON(http.StatusInternalServerError, skillsErr("FS_ERROR", err.Error()))
			return
		}
		var linked []string
		for _, sk := range skills {
			registrySkillDir := filepath.Join(deps.RegistryDir, sk.Name)
			linkPath := filepath.Join(agentDir, sk.Name)
			if fi, err := os.Lstat(linkPath); err == nil {
				if fi.Mode()&os.ModeSymlink != 0 {
					linked = append(linked, sk.Name)
				}
				continue // skip non-symlinks to avoid clobbering
			}
			relTarget, err := filepath.Rel(agentDir, registrySkillDir)
			if err != nil {
				continue
			}
			if err := os.Symlink(relTarget, linkPath); err == nil {
				linked = append(linked, sk.Name)
			}
		}
		if linked == nil {
			linked = []string{}
		}
		c.JSON(http.StatusOK, gin.H{"linked": linked})
	})
}

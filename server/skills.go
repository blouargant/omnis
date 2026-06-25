package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/json"
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

	"github.com/blouargant/omnis/agent"
	"github.com/blouargant/omnis/internal/paths"
	"github.com/blouargant/omnis/internal/registries"
	"github.com/blouargant/omnis/internal/registrymeta"
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
	RegistryDir              string   // abs path to first-existing registry/skills/ (legacy single-dir callers)
	RegistryAllDirs          []string // abs paths of all skill directories to read, in precedence order
	RegistryWriteDir         string   // abs path to registry/skills/ write target (always $OMNIS_HOME)
	AgentYAML                string   // abs path to agent.json (re-read on each request for freshness)
	AgentsRegistryReadDir    string   // abs path to registry/agents/ (read — first-existing-wins)
	AgentsRegistryWriteDir   string   // abs path to registry/agents/ write target (always $OMNIS_HOME)
	RemoteRegistriesCfgWrite string   // abs write target for remote_registries.json
}

// findSkillDir returns the absolute path of the directory that contains the
// named skill (i.e. <dir>/<name>/SKILL.md exists), searching RegistryAllDirs
// in precedence order. Returns "" when the skill is not found.
func (d skillsDeps) findSkillDir(name string) string {
	for _, dir := range d.RegistryAllDirs {
		if _, err := os.Stat(filepath.Join(dir, name, "SKILL.md")); err == nil {
			return filepath.Join(dir, name)
		}
	}
	return ""
}

// resolveSkillsDeps derives skills paths from the already-resolved config files.
// Read paths use first-existing-wins so a developer checkout stays visible.
// Write paths always land under $OMNIS_HOME — web UI saves never touch the
// local checkout, mirroring the config write contract.
func resolveSkillsDeps(cfgFiles configFiles) skillsDeps {
	registryWriteDir := paths.SkillsRegistryWriteDir()
	if v := os.Getenv("OMNIS_SKILLS_REGISTRY_DIR"); v != "" {
		registryWriteDir = v
	}
	absWriteReg, _ := filepath.Abs(registryWriteDir)

	// Build the full ordered list of skill read directories.
	allDirs := paths.SkillsAllSearchDirs()
	if v := os.Getenv("OMNIS_SKILLS_REGISTRY_DIR"); v != "" {
		// When the env var overrides the registry dir, only that dir is used.
		absEnv, _ := filepath.Abs(v)
		allDirs = []string{absEnv}
	}
	var absAllDirs []string
	for _, d := range allDirs {
		if abs, err := filepath.Abs(d); err == nil {
			absAllDirs = append(absAllDirs, abs)
		}
	}

	// Legacy single-dir field: first directory that exists (for callers that
	// haven't been updated to multi-dir yet).
	var absReadReg string
	for _, d := range absAllDirs {
		if st, err := os.Stat(d); err == nil && st.IsDir() {
			absReadReg = d
			break
		}
	}
	if absReadReg == "" {
		absReadReg = absWriteReg
	}

	absAgentsRead, _ := filepath.Abs(paths.AgentsRegistryDir())
	absAgentsWrite, _ := filepath.Abs(paths.AgentsRegistryWriteDir())
	if v := os.Getenv("OMNIS_AGENTS_REGISTRY_DIR"); v != "" {
		absAgentsRead, _ = filepath.Abs(v)
		absAgentsWrite = absAgentsRead
	}
	absRemoteWrite, _ := filepath.Abs(filepath.Join(paths.ConfigWriteDir(), "remote_registries.json"))
	return skillsDeps{
		RegistryDir:              absReadReg,
		RegistryAllDirs:          absAllDirs,
		RegistryWriteDir:         absWriteReg,
		AgentYAML:                cfgFiles.Agent,
		AgentsRegistryReadDir:    absAgentsRead,
		AgentsRegistryWriteDir:   absAgentsWrite,
		RemoteRegistriesCfgWrite: absRemoteWrite,
	}
}

// ── Registry helpers ───────────────────────────────────────────────────────

// skillFrontmatter, skillInfo, parseSkillFrontmatter, and
// listRegistrySkills now live in internal/registrymeta. The aliases
// below keep this file readable without re-exporting at every call site.
type (
	skillFrontmatter = registrymeta.Frontmatter
	skillInfo        = registrymeta.SkillInfo
)

var (
	parseSkillFrontmatter = registrymeta.ParseFrontmatter
	listRegistrySkills    = registrymeta.ListSkills
)

// pathLayer returns "local", "user", or "system" based on where dirPath sits
// in the config search chain. Used to label skills and agents in the web UI.
// Delegates to paths.Layer so both `.agents/` and `agents/` are recognised as
// local.
func pathLayer(dirPath string) string {
	return paths.Layer(dirPath)
}

// agentSkillList returns the agent's declared skills, normalised to a non-nil slice.
func agentSkillList(a agent.RuntimeAgentConfig) []string {
	if a.Skills == nil {
		return []string{}
	}
	return a.Skills
}

// agentHasSkillsTool reports whether the agent's tools list includes "Skill".
// For leader, an empty tools list means all tools enabled (including skills).
func agentHasSkillsTool(a agent.RuntimeAgentConfig) bool {
	if a.Name == "leader" {
		return len(a.Tools) == 0 || slices.Contains(a.Tools, "Skill")
	}
	return slices.Contains(a.Tools, "Skill")
}

// readRuntimeAgents re-reads agent.json and returns the resolved agent list.
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
		skills, err := listRegistrySkills(deps.RegistryAllDirs...)
		if err != nil {
			c.JSON(http.StatusInternalServerError, skillsErr("FS_ERROR", err.Error()))
			return
		}
		// Annotate linked_in per skill using agents' declared skills lists.
		agents, _ := readRuntimeAgents(deps.AgentYAML)
		for i, sk := range skills {
			for _, a := range agents {
				if slices.Contains(a.Skills, sk.Name) {
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

		target := filepath.Join(deps.RegistryWriteDir, name)
		if _, err := os.Stat(target); err == nil && !overwrite {
			c.JSON(http.StatusConflict,
				skillsErr("NAME_TAKEN", fmt.Sprintf("skill %q already exists; add ?overwrite=1 to replace", name)))
			return
		}
		if err := os.MkdirAll(deps.RegistryWriteDir, 0o755); err != nil {
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
		skillDir := deps.findSkillDir(name)
		if skillDir == "" {
			c.JSON(http.StatusNotFound, skillsErr("NOT_FOUND", "skill not found"))
			return
		}
		skillMD := filepath.Join(skillDir, "SKILL.md")
		data, err := os.ReadFile(skillMD)
		if err != nil {
			c.JSON(http.StatusInternalServerError, skillsErr("FS_ERROR", err.Error()))
			return
		}
		st, _ := os.Stat(skillMD)
		fm, _ := parseSkillFrontmatter(data)
		var mtime time.Time
		if st != nil {
			mtime = st.ModTime()
		}
		// Collect agents that declare this skill.
		agents, _ := readRuntimeAgents(deps.AgentYAML)
		var linkedIn []string
		for _, a := range agents {
			if slices.Contains(a.Skills, name) {
				linkedIn = append(linkedIn, a.Name)
			}
		}
		if linkedIn == nil {
			linkedIn = []string{}
		}
		c.JSON(http.StatusOK, gin.H{
			"name":        firstNonEmpty(fm.Name, name),
			"description": fm.Description,
			"author":      fm.Author(),
			"tags":        fm.Tags(),
			"metadata":    fm.Metadata,
			"content":     string(data),
			"mtime":       mtime,
			"resources":   collectResources(skillDir),
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
		skillDir := filepath.Join(deps.RegistryWriteDir, name)
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
		// Find skill in read chain (may be in .agents/skills/ or registry/skills/).
		sourceDir := deps.findSkillDir(name)
		if sourceDir == "" {
			c.JSON(http.StatusNotFound, skillsErr("NOT_FOUND", "skill not found"))
			return
		}
		readSkillMD := filepath.Join(sourceDir, "SKILL.md")
		if status, body := checkMtime(readSkillMD, req.MTime); status != 0 {
			c.JSON(status, body)
			return
		}
		// Always write to home-anchored registry (fork-on-first-edit).
		writeSkillMD := filepath.Join(deps.RegistryWriteDir, name, "SKILL.md")
		if err := os.MkdirAll(filepath.Dir(writeSkillMD), 0o755); err != nil {
			c.JSON(http.StatusInternalServerError, skillsErr("FS_ERROR", err.Error()))
			return
		}
		if err := atomicWriteFile(writeSkillMD, []byte(req.Content)); err != nil {
			c.JSON(http.StatusInternalServerError, skillsErr("FS_ERROR", err.Error()))
			return
		}
		st, _ := os.Stat(writeSkillMD)
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
		skillDir := deps.findSkillDir(name)
		if skillDir == "" {
			c.JSON(http.StatusNotFound, skillsErr("NOT_FOUND", "skill not found"))
			return
		}
		force := c.Query("force") == "1"
		agents, _ := readRuntimeAgents(deps.AgentYAML)
		var linkedAgents []string
		for _, a := range agents {
			if slices.Contains(a.Skills, name) {
				linkedAgents = append(linkedAgents, a.Name)
			}
		}
		if len(linkedAgents) > 0 && !force {
			c.JSON(http.StatusConflict, skillsErrDetail("LINKED_IN_AGENTS",
				"skill is still used by one or more agents",
				gin.H{"agents": linkedAgents}))
			return
		}
		// Remove the skill from each agent's list when force=1.
		for _, a := range agents {
			if slices.Contains(a.Skills, name) {
				_, _ = registries.RemoveSkillFromAgent(deps.AgentsRegistryReadDir, deps.AgentsRegistryWriteDir, a.Name, name)
			}
		}
		// Delete from the home-anchored write dir only (never delete from dev checkout).
		if err := os.RemoveAll(filepath.Join(deps.RegistryWriteDir, name)); err != nil {
			c.JSON(http.StatusInternalServerError, skillsErr("FS_ERROR", err.Error()))
			return
		}
		c.Status(http.StatusNoContent)
	})

	// ── Agent skill assignments ────────────────────────────────────────────

	// GET /agents — list all agents with their declared skills.
	rg.GET("/agents", func(c *gin.Context) {
		agents, err := readRuntimeAgents(deps.AgentYAML)
		if err != nil {
			c.JSON(http.StatusInternalServerError, skillsErr("CONFIG_ERROR", err.Error()))
			return
		}
		type agentSkillInfo struct {
			Name          string   `json:"name"`
			HasSkillsTool bool     `json:"has_skills_tool"`
			Skills        []string `json:"skills"`
		}
		out := make([]agentSkillInfo, 0, len(agents))
		for _, a := range agents {
			out = append(out, agentSkillInfo{
				Name:          a.Name,
				HasSkillsTool: agentHasSkillsTool(a),
				Skills:        agentSkillList(a),
			})
		}
		c.JSON(http.StatusOK, gin.H{"agents": out})
	})

	// POST /agents/:agent/skills/:name — add a skill to an agent's list.
	rg.POST("/agents/:agent/skills/:name", func(c *gin.Context) {
		agentName := c.Param("agent")
		skillName := c.Param("name")
		if !skillNameRe.MatchString(skillName) {
			c.JSON(http.StatusBadRequest, skillsErr("NAME_INVALID", "invalid skill name"))
			return
		}
		if _, err := validatedAgent(deps.AgentYAML, agentName); err != nil {
			c.JSON(http.StatusNotFound, skillsErr("AGENT_NOT_FOUND", err.Error()))
			return
		}
		registrySkillDir := filepath.Join(deps.RegistryDir, skillName)
		if _, err := os.Stat(registrySkillDir); os.IsNotExist(err) {
			c.JSON(http.StatusNotFound, skillsErr("NOT_FOUND",
				fmt.Sprintf("skill %q not found in registry", skillName)))
			return
		}
		if _, err := registries.AddSkillToAgent(deps.AgentsRegistryReadDir, deps.AgentsRegistryWriteDir, agentName, skillName); err != nil {
			c.JSON(http.StatusInternalServerError, skillsErr("CONFIG_ERROR", err.Error()))
			return
		}
		c.Status(http.StatusNoContent)
	})

	// DELETE /agents/:agent/skills/:name — remove a skill from an agent's list.
	rg.DELETE("/agents/:agent/skills/:name", func(c *gin.Context) {
		agentName := c.Param("agent")
		skillName := c.Param("name")
		if !skillNameRe.MatchString(skillName) {
			c.JSON(http.StatusBadRequest, skillsErr("NAME_INVALID", "invalid skill name"))
			return
		}
		if _, err := validatedAgent(deps.AgentYAML, agentName); err != nil {
			c.JSON(http.StatusNotFound, skillsErr("AGENT_NOT_FOUND", err.Error()))
			return
		}
		if _, err := registries.RemoveSkillFromAgent(deps.AgentsRegistryReadDir, deps.AgentsRegistryWriteDir, agentName, skillName); err != nil {
			c.JSON(http.StatusInternalServerError, skillsErr("CONFIG_ERROR", err.Error()))
			return
		}
		c.Status(http.StatusNoContent)
	})

	// ── Remote registries ─────────────────────────────────────────────────
	registerRemoteRegistryRoutes(rg, func() string {
		// Re-resolve every request so a save into $OMNIS_HOME on the
		// previous request is visible immediately, without restart.
		p, _ := filepath.Abs(paths.FindConfig("remote_registries.json"))
		return p
	}, deps.RemoteRegistriesCfgWrite, deps.RegistryDir, deps.RegistryWriteDir)

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
		if _, err := validatedAgent(deps.AgentYAML, agentName); err != nil {
			c.JSON(http.StatusNotFound, skillsErr("AGENT_NOT_FOUND", err.Error()))
			return
		}

		if req.Action == "none" {
			// Read from read dir; write to home-anchored write dir.
			readAgentPath := filepath.Join(deps.AgentsRegistryReadDir, agentName, "agent.json")
			data, err := os.ReadFile(readAgentPath)
			if err != nil {
				c.JSON(http.StatusInternalServerError, skillsErr("CONFIG_ERROR", err.Error()))
				return
			}
			var entry map[string]any
			if err := json.Unmarshal(data, &entry); err != nil {
				c.JSON(http.StatusInternalServerError, skillsErr("CONFIG_ERROR", err.Error()))
				return
			}
			removed := []string{}
			if raw, ok := entry["skills"]; ok {
				if list, ok := raw.([]any); ok {
					for _, item := range list {
						if s, ok := item.(string); ok {
							removed = append(removed, s)
						}
					}
				}
			}
			entry["skills"] = []string{}
			out, _ := json.MarshalIndent(entry, "", "  ")
			out = append(out, '\n')
			writeAgentPath := filepath.Join(deps.AgentsRegistryWriteDir, agentName, "agent.json")
			if err := os.MkdirAll(filepath.Dir(writeAgentPath), 0o755); err != nil {
				c.JSON(http.StatusInternalServerError, skillsErr("CONFIG_ERROR", err.Error()))
				return
			}
			if err := os.WriteFile(writeAgentPath, out, 0o644); err != nil {
				c.JSON(http.StatusInternalServerError, skillsErr("CONFIG_ERROR", err.Error()))
				return
			}
			c.JSON(http.StatusOK, gin.H{"removed": removed})
			return
		}

		// action == "all": add every registry skill to the agent's list.
		skillsList, err := listRegistrySkills(deps.RegistryAllDirs...)
		if err != nil {
			c.JSON(http.StatusInternalServerError, skillsErr("FS_ERROR", err.Error()))
			return
		}
		var linked []string
		for _, sk := range skillsList {
			if _, err := registries.AddSkillToAgent(deps.AgentsRegistryReadDir, deps.AgentsRegistryWriteDir, agentName, sk.Name); err == nil {
				linked = append(linked, sk.Name)
			}
		}
		if linked == nil {
			linked = []string{}
		}
		c.JSON(http.StatusOK, gin.H{"linked": linked})
	})
}

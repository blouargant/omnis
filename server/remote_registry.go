package main

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/blouargant/omnis/internal/paths"
	"github.com/blouargant/omnis/internal/registries"
)

// prefixAll returns a copy of names with prefix prepended to each entry.
func prefixAll(prefix string, names []string) []string {
	if len(names) == 0 {
		return nil
	}
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = prefix + n
	}
	return out
}

// publicRemote is the browser-safe shape of a remoteRegistry (no token).
type publicRemote struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	URL      string   `json:"url"`
	Provider string   `json:"provider,omitempty"`
	Kind     string   `json:"kind"`  // canonical joined form, kept for backward compat
	Kinds    []string `json:"kinds"` // the content kinds this registry serves
	HasToken bool     `json:"has_token"`
}

func toPublicRemote(r registries.Registry) publicRemote {
	return publicRemote{
		ID:       r.ID,
		Name:     r.Name,
		URL:      r.URL,
		Provider: r.Provider,
		Kind:     r.NormalizedKind(),
		Kinds:    r.EffectiveKinds(),
		HasToken: r.Token != "",
	}
}

// normalizeKinds validates and de-duplicates the content kinds supplied by the
// web UI. It accepts the canonical `kinds` array and the legacy single `kind`
// string (which may be the "both" alias); the "both" alias expands to
// skills+agents. An empty or all-invalid input falls back to defaultKind so
// each tab keeps its preferred default.
func normalizeKinds(kinds []string, single, defaultKind string) []string {
	raw := kinds
	if len(raw) == 0 && strings.TrimSpace(single) != "" {
		raw = []string{single}
	}
	out := make([]string, 0, len(raw))
	seen := map[string]bool{}
	add := func(k string) {
		if k == "" || seen[k] {
			return
		}
		seen[k] = true
		out = append(out, k)
	}
	for _, k := range raw {
		switch k = strings.TrimSpace(k); {
		case k == registries.KindBoth:
			add(registries.KindSkills)
			add(registries.KindAgents)
		case registries.ValidKind(k):
			add(k)
		}
	}
	if len(out) == 0 {
		return []string{defaultKind}
	}
	return out
}

// containsStr reports whether s is in list.
func containsStr(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// registerRemoteRegistryRoutes mounts the /remotes endpoints on rg.
// readPath is a thunk so the 3-layer config chain is re-resolved on every
// request: after a save creates a fresh override under $OMNIS_HOME/config,
// subsequent reads transparently pick it up. writePath is fixed under
// $OMNIS_HOME/config (the fork-on-first-edit destination).
// registerRemoteRegistryRoutes mounts the /remotes endpoints on rg.
// registryReadDir is used by browse to check which skills are already installed
// (first-existing-wins). registryWriteDir is the install target — always
// $OMNIS_HOME/registry/skills so remote installs never land in a local checkout.
func registerRemoteRegistryRoutes(rg *gin.RouterGroup, readPath func() string, writePath, registryReadDir, registryWriteDir string) {
	registerRemoteRegistryCRUD(rg, readPath, writePath, registries.KindSkills)

	// GET /remotes/:id/browse — fetch skill list using the provider's tree API.
	rg.GET("/remotes/:id/browse", func(c *gin.Context) {
		reg, ref, ok := loadRegistryForKind(c, readPath, c.Param("id"), registries.KindSkills)
		if !ok {
			return
		}
		skills, err := registries.BrowseSkills(ref, reg.Token, registryReadDir)
		if err != nil {
			c.JSON(http.StatusBadGateway, skillsErr("REMOTE_ERROR", err.Error()))
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"skills":   skills,
			"registry": toPublicRemote(*reg),
		})
	})

	// GET /remotes/:id/skill/*dirpath — fetch raw SKILL.md content.
	rg.GET("/remotes/:id/skill/*dirpath", func(c *gin.Context) {
		dirPath := strings.Trim(c.Param("dirpath"), "/")
		if dirPath == "" {
			c.JSON(http.StatusBadRequest, skillsErr("BAD_REQUEST", "dirpath is required"))
			return
		}
		reg, ref, ok := loadRegistryForKind(c, readPath, c.Param("id"), registries.KindSkills)
		if !ok {
			return
		}
		body, err := registries.FetchSkillMD(ref, reg.Token, dirPath)
		if err != nil {
			c.JSON(http.StatusBadGateway, skillsErr("REMOTE_ERROR", err.Error()))
			return
		}
		c.JSON(http.StatusOK, gin.H{"content": string(body)})
	})

	// POST /remotes/:id/install/*dirpath — download and install a skill.
	rg.POST("/remotes/:id/install/*dirpath", func(c *gin.Context) {
		dirPath := strings.Trim(c.Param("dirpath"), "/")
		if dirPath == "" {
			c.JSON(http.StatusBadRequest, skillsErr("BAD_REQUEST", "dirpath is required"))
			return
		}
		reg, ref, ok := loadRegistryForKind(c, readPath, c.Param("id"), registries.KindSkills)
		if !ok {
			return
		}
		if err := os.MkdirAll(registryWriteDir, 0o755); err != nil {
			c.JSON(http.StatusInternalServerError, skillsErr("FS_ERROR", err.Error()))
			return
		}
		skillName, err := registries.InstallSkill(ref, reg.Token, dirPath, registryWriteDir)
		if err != nil {
			c.JSON(http.StatusBadGateway, skillsErr("INSTALL_ERROR", err.Error()))
			return
		}
		// Cascade the skill's declared commands + permission rule-sets so it
		// arrives with everything it needs (mirrors the agent dependency
		// cascade and the helper agent's install_remote_item).
		var installedDeps, warnings []string
		if raw, ferr := registries.FetchSkillMD(ref, reg.Token, dirPath); ferr == nil {
			commands, perms := parseSkillMDDeps(raw)
			cmdInstalled, cmdWarns := tryAutoInstallCommands(commands, readPath())
			installedDeps = append(installedDeps, prefixAll("command:", cmdInstalled)...)
			warnings = append(warnings, cmdWarns...)

			permRead := paths.FindConfig("permissions.json")
			permWrite := filepath.Join(paths.ConfigWriteDir(), "permissions.json")
			permInstalled, permWarns := tryAutoInstallPermissions(perms, permRead, permWrite, readPath())
			installedDeps = append(installedDeps, prefixAll("permission:", permInstalled)...)
			warnings = append(warnings, permWarns...)
		}
		resp := gin.H{"name": skillName}
		if len(installedDeps) > 0 {
			resp["installed_deps"] = installedDeps
		}
		if len(warnings) > 0 {
			resp["warnings"] = warnings
		}
		c.JSON(http.StatusCreated, resp)
	})
}

// loadRegistryForKind looks up the registry by ID, validates it serves the
// requested kind, parses its URL, and writes a uniform error response on
// any failure. The caller short-circuits when ok==false.
func loadRegistryForKind(c *gin.Context, readPath func() string, id, kind string) (*registries.Registry, registries.RepoRef, bool) {
	list, err := registries.LoadRegistries(readPath())
	if err != nil {
		c.JSON(http.StatusInternalServerError, skillsErr("FS_ERROR", err.Error()))
		return nil, nil, false
	}
	reg := registries.FindByID(list, id)
	if reg == nil || !reg.Serves(kind) {
		c.JSON(http.StatusNotFound, skillsErr("NOT_FOUND", "registry not found"))
		return nil, nil, false
	}
	ref, err := registries.ParseRepoRef(reg.URL, reg.Provider)
	if err != nil {
		c.JSON(http.StatusBadRequest, skillsErr("BAD_URL", err.Error()))
		return nil, nil, false
	}
	return reg, ref, true
}

// registerRemoteRegistryCRUD mounts the registry list / create / update /
// delete endpoints on rg. The same backing remote_registries.json is shared
// between the skill and agent tabs; each tab filters by kind and writes the
// caller-provided defaultKind into new entries that don't specify one.
//
// Updates and deletes that target a "both" registry from one tab demote it
// to the other kind rather than removing the entry outright — so the other
// tab keeps its view of the registry.
func registerRemoteRegistryCRUD(rg *gin.RouterGroup, readPath func() string, writePath, defaultKind string) {
	rg.GET("/remotes", func(c *gin.Context) {
		list, err := registries.LoadRegistries(readPath())
		if err != nil {
			c.JSON(http.StatusInternalServerError, skillsErr("FS_ERROR", err.Error()))
			return
		}
		out := make([]publicRemote, 0, len(list))
		for _, r := range list {
			if r.Serves(defaultKind) {
				out = append(out, toPublicRemote(r))
			}
		}
		c.JSON(http.StatusOK, gin.H{"remotes": out})
	})

	rg.POST("/remotes", func(c *gin.Context) {
		var req struct {
			URL      string   `json:"url"`
			Name     string   `json:"name"`
			Provider string   `json:"provider"`
			Kind     string   `json:"kind"`
			Kinds    []string `json:"kinds"`
			Token    string   `json:"token"`
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
		ref, err := registries.ParseRepoRef(rawURL, provider)
		if err != nil {
			c.JSON(http.StatusBadRequest, skillsErr("BAD_URL", err.Error()))
			return
		}
		list, err := registries.LoadRegistries(readPath())
		if err != nil {
			c.JSON(http.StatusInternalServerError, skillsErr("FS_ERROR", err.Error()))
			return
		}
		kinds := normalizeKinds(req.Kinds, req.Kind, defaultKind)
		// If an entry already exists at this URL, union the requested kinds into
		// it rather than rejecting — the user effectively asked for more kinds.
		for i, r := range list {
			if r.URL == rawURL {
				merged := r.EffectiveKinds()
				grew := false
				for _, k := range kinds {
					if !containsStr(merged, k) {
						merged = append(merged, k)
						grew = true
					}
				}
				if !grew {
					c.JSON(http.StatusConflict, skillsErr("DUPLICATE", "a registry with this URL already exists"))
					return
				}
				list[i].Kinds = merged
				list[i].Kind = ""
				if err := registries.SaveRegistries(writePath, list); err != nil {
					c.JSON(http.StatusInternalServerError, skillsErr("FS_ERROR", err.Error()))
					return
				}
				c.JSON(http.StatusCreated, toPublicRemote(list[i]))
				return
			}
		}
		name := strings.TrimSpace(req.Name)
		if name == "" {
			name = ref.AutoName()
		}
		reg := registries.Registry{
			ID:       registries.NewID(),
			Name:     name,
			URL:      rawURL,
			Provider: provider,
			Kinds:    kinds,
			Token:    strings.TrimSpace(req.Token),
		}
		list = append(list, reg)
		if err := registries.SaveRegistries(writePath, list); err != nil {
			c.JSON(http.StatusInternalServerError, skillsErr("FS_ERROR", err.Error()))
			return
		}
		c.JSON(http.StatusCreated, toPublicRemote(reg))
	})

	rg.PUT("/remotes/:id", func(c *gin.Context) {
		id := c.Param("id")
		var req struct {
			Name     string   `json:"name"`
			URL      string   `json:"url"`
			Provider string   `json:"provider"`
			Kind     string   `json:"kind"`
			Kinds    []string `json:"kinds"`
			Token    string   `json:"token"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, skillsErr("BAD_REQUEST", "invalid JSON"))
			return
		}
		list, err := registries.LoadRegistries(readPath())
		if err != nil {
			c.JSON(http.StatusInternalServerError, skillsErr("FS_ERROR", err.Error()))
			return
		}
		reg := registries.FindByID(list, id)
		if reg == nil || !reg.Serves(defaultKind) {
			c.JSON(http.StatusNotFound, skillsErr("NOT_FOUND", "registry not found"))
			return
		}
		if newURL := strings.TrimSpace(req.URL); newURL != "" {
			if _, err := registries.ParseRepoRef(newURL, strings.TrimSpace(req.Provider)); err != nil {
				c.JSON(http.StatusBadRequest, skillsErr("BAD_URL", err.Error()))
				return
			}
			reg.URL = newURL
		}
		if newName := strings.TrimSpace(req.Name); newName != "" {
			reg.Name = newName
		}
		reg.Provider = strings.TrimSpace(req.Provider)
		if len(req.Kinds) > 0 || req.Kind != "" {
			reg.Kinds = normalizeKinds(req.Kinds, req.Kind, defaultKind)
			reg.Kind = ""
		}
		if newToken := strings.TrimSpace(req.Token); newToken != "" {
			reg.Token = newToken
		}
		if err := registries.SaveRegistries(writePath, list); err != nil {
			c.JSON(http.StatusInternalServerError, skillsErr("FS_ERROR", err.Error()))
			return
		}
		c.Status(http.StatusNoContent)
	})

	rg.DELETE("/remotes/:id", func(c *gin.Context) {
		id := c.Param("id")
		list, err := registries.LoadRegistries(readPath())
		if err != nil {
			c.JSON(http.StatusInternalServerError, skillsErr("FS_ERROR", err.Error()))
			return
		}
		idx := -1
		for i, r := range list {
			if r.ID == id {
				idx = i
				break
			}
		}
		if idx < 0 || !list[idx].Serves(defaultKind) {
			c.JSON(http.StatusNotFound, skillsErr("NOT_FOUND", "registry not found"))
			return
		}
		// A multi-kind registry is only removed when this tab's kind was its
		// last one; otherwise just this kind is dropped so the sibling tabs
		// keep the entry they share.
		served := list[idx].EffectiveKinds()
		remaining := make([]string, 0, len(served))
		for _, k := range served {
			if k != defaultKind {
				remaining = append(remaining, k)
			}
		}
		if len(remaining) == 0 {
			list = append(list[:idx], list[idx+1:]...)
		} else {
			list[idx].Kinds = remaining
			list[idx].Kind = ""
		}
		if err := registries.SaveRegistries(writePath, list); err != nil {
			c.JSON(http.StatusInternalServerError, skillsErr("FS_ERROR", err.Error()))
			return
		}
		c.Status(http.StatusNoContent)
	})
}

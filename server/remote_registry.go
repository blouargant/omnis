package main

import (
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/blouargant/yoke/internal/registries"
)

// publicRemote is the browser-safe shape of a remoteRegistry (no token).
type publicRemote struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	URL      string `json:"url"`
	Provider string `json:"provider,omitempty"`
	Kind     string `json:"kind"`
	HasToken bool   `json:"has_token"`
}

func toPublicRemote(r registries.Registry) publicRemote {
	return publicRemote{
		ID:       r.ID,
		Name:     r.Name,
		URL:      r.URL,
		Provider: r.Provider,
		Kind:     r.NormalizedKind(),
		HasToken: r.Token != "",
	}
}

// normalizeKindInput coerces a kind value supplied by the web UI to one of
// the canonical values. An empty input falls back to defaultKind so each
// tab can choose its preferred default ("skills" for skill UI, "agents" for
// the agents UI).
func normalizeKindInput(raw, defaultKind string) string {
	switch strings.TrimSpace(raw) {
	case registries.KindSkills, registries.KindAgents, registries.KindBoth, registries.KindMCP, registries.KindA2A, registries.KindSquads:
		return strings.TrimSpace(raw)
	case "":
		return defaultKind
	}
	return defaultKind
}

// registerRemoteRegistryRoutes mounts the /remotes endpoints on rg.
// readPath is a thunk so the 3-layer config chain is re-resolved on every
// request: after a save creates a fresh override under $YOKE_HOME/config,
// subsequent reads transparently pick it up. writePath is fixed under
// $YOKE_HOME/config (the fork-on-first-edit destination).
// registerRemoteRegistryRoutes mounts the /remotes endpoints on rg.
// registryReadDir is used by browse to check which skills are already installed
// (first-existing-wins). registryWriteDir is the install target — always
// $YOKE_HOME/registry/skills so remote installs never land in a local checkout.
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
		c.JSON(http.StatusCreated, gin.H{"name": skillName})
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
			URL      string `json:"url"`
			Name     string `json:"name"`
			Provider string `json:"provider"`
			Kind     string `json:"kind"`
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
		kind := normalizeKindInput(req.Kind, defaultKind)
		// If an entry already exists at this URL, promote it to "both" rather
		// than rejecting — the user effectively asked for the other kind too.
		for i, r := range list {
			if r.URL == rawURL {
				if r.Serves(kind) {
					c.JSON(http.StatusConflict, skillsErr("DUPLICATE", "a registry with this URL already exists"))
					return
				}
				list[i].Kind = registries.KindBoth
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
			Kind:     kind,
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
			Name     string `json:"name"`
			URL      string `json:"url"`
			Provider string `json:"provider"`
			Kind     string `json:"kind"`
			Token    string `json:"token"`
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
		if req.Kind != "" {
			reg.Kind = normalizeKindInput(req.Kind, defaultKind)
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
		// "both" registries are demoted to the other kind rather than removed,
		// so the sibling tab keeps the entry it added.
		if list[idx].NormalizedKind() == registries.KindBoth {
			other := registries.KindAgents
			if defaultKind == registries.KindAgents {
				other = registries.KindSkills
			}
			list[idx].Kind = other
		} else {
			list = append(list[:idx], list[idx+1:]...)
		}
		if err := registries.SaveRegistries(writePath, list); err != nil {
			c.JSON(http.StatusInternalServerError, skillsErr("FS_ERROR", err.Error()))
			return
		}
		c.Status(http.StatusNoContent)
	})
}

package main

import (
	"fmt"
	"net/http"
	"path"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/blouargant/omnis/internal/claudeformat"
	"github.com/blouargant/omnis/internal/paths"
	"github.com/blouargant/omnis/internal/registries"
)

// registerRemoteCommandsRegistryRoutes mounts /remotes endpoints scoped to
// "commands" kind on rg. Shares the backing remote_registries.json with the
// other registry tabs. Installed commands land in the per-user
// user_commands.json file (one flat list, no per-directory layout).
//
// Commands follow Anthropic Claude Code's slash-command formalism: each .md
// file is one command. The filename (without .md) is the command name; YAML
// frontmatter provides description and argument-hint; the body is the prompt
// template (supports $1..$N and $* placeholders, same as the local editor).
func registerRemoteCommandsRegistryRoutes(
	rg *gin.RouterGroup,
	readPath func() string,
	writePath string,
	store *userCommandsStore,
) {
	registerRemoteRegistryCRUD(rg, readPath, writePath, registries.KindCommands)

	// GET /remotes/:id/browse — list slash-commands discoverable in the remote tree.
	rg.GET("/remotes/:id/browse", func(c *gin.Context) {
		reg, ref, ok := loadRegistryForKind(c, readPath, c.Param("id"), registries.KindCommands)
		if !ok {
			return
		}
		installed := map[string]bool{}
		for _, cmd := range store.list() {
			installed[normName(cmd.Name)] = true
		}
		for name := range reservedNames {
			installed[name] = true
		}
		commands, err := registries.BrowseCommands(ref, reg.Token, installed)
		if err != nil {
			c.JSON(http.StatusBadGateway, skillsErr("REMOTE_ERROR", err.Error()))
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"commands": commands,
			"registry": toPublicRemote(*reg),
		})
	})

	// GET /remotes/:id/command/*dirpath — fetch raw command markdown for preview.
	rg.GET("/remotes/:id/command/*dirpath", func(c *gin.Context) {
		filePath := strings.Trim(c.Param("dirpath"), "/")
		if filePath == "" {
			c.JSON(http.StatusBadRequest, skillsErr("BAD_REQUEST", "dirpath is required"))
			return
		}
		reg, ref, ok := loadRegistryForKind(c, readPath, c.Param("id"), registries.KindCommands)
		if !ok {
			return
		}
		body, err := registries.FetchCommandMD(ref, reg.Token, filePath)
		if err != nil {
			c.JSON(http.StatusBadGateway, skillsErr("REMOTE_ERROR", err.Error()))
			return
		}
		c.JSON(http.StatusOK, gin.H{"content": string(body)})
	})

	// POST /remotes/:id/install/*dirpath — download and merge a command into
	// user_commands.json. An existing command with the same name is replaced
	// (idempotent update). Reserved built-in names are rejected.
	rg.POST("/remotes/:id/install/*dirpath", func(c *gin.Context) {
		filePath := strings.Trim(c.Param("dirpath"), "/")
		if filePath == "" {
			c.JSON(http.StatusBadRequest, skillsErr("BAD_REQUEST", "dirpath is required"))
			return
		}
		reg, ref, ok := loadRegistryForKind(c, readPath, c.Param("id"), registries.KindCommands)
		if !ok {
			return
		}
		raw, err := registries.FetchCommandMD(ref, reg.Token, filePath)
		if err != nil {
			c.JSON(http.StatusBadGateway, skillsErr("INSTALL_ERROR", err.Error()))
			return
		}
		def, err := claudeformat.ParseCommandMarkdown(raw)
		if err != nil {
			c.JSON(http.StatusBadGateway, skillsErr("INSTALL_ERROR", fmt.Sprintf("parse command: %v", err)))
			return
		}
		// Name resolution: frontmatter > filename leaf.
		name := def.Name
		if name == "" {
			leaf := path.Base(filePath)
			name = strings.TrimSuffix(leaf, ".md")
		}
		cmd := userCommand{
			Name:        name,
			Description: def.Description,
			Args:        def.ArgumentHint,
			Prompt:      def.Prompt,
		}
		if err := validateCommand(&cmd); err != nil {
			c.JSON(http.StatusBadRequest, skillsErr("INSTALL_ERROR", err.Error()))
			return
		}
		// Detect overwrite vs fresh add so the UI can distinguish them.
		var existed bool
		for _, existing := range store.list() {
			if normName(existing.Name) == cmd.Name {
				existed = true
				break
			}
		}
		if _, err := store.upsert(cmd, cmd.Name); err != nil {
			c.JSON(http.StatusInternalServerError, skillsErr("FS_ERROR", err.Error()))
			return
		}
		c.JSON(http.StatusCreated, gin.H{"name": cmd.Name, "added": !existed})
	})
}

// registerCommandsRoutes mounts the /api/commands/* remote-registry routes.
// Called from server.go alongside the other remote-registry registrants.
func registerCommandsRoutes(rg *gin.RouterGroup, store *userCommandsStore) {
	deps := resolveCommandsRoutesDeps()
	registerRemoteCommandsRegistryRoutes(rg, deps.RemoteRegistriesRead, deps.RemoteRegistriesWrite, store)
}

// commandsRoutesDeps mirrors the dep bundle pattern used by the other
// remote-registry registrants. Built once per server boot.
type commandsRoutesDeps struct {
	RemoteRegistriesWrite string
	RemoteRegistriesRead  func() string
}

func resolveCommandsRoutesDeps() commandsRoutesDeps {
	absRemoteWrite, _ := filepath.Abs(filepath.Join(paths.ConfigWriteDir(), registries.ConfigFileName))
	return commandsRoutesDeps{
		RemoteRegistriesWrite: absRemoteWrite,
		RemoteRegistriesRead: func() string {
			p, _ := filepath.Abs(paths.FindConfig(registries.ConfigFileName))
			return p
		},
	}
}

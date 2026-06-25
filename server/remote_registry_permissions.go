package main

import (
	"net/http"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/blouargant/omnis/internal/paths"
	"github.com/blouargant/omnis/internal/registries"
)

// registerRemotePermissionsRegistryRoutes mounts /remotes endpoints scoped to
// "permissions" kind. Shares the backing remote_registries.json with the other
// registry tabs. Each remote item is a directory containing a permissions.json
// (same shape as the local permissions.json); installing merges its rules into
// the user's permissions.json (deduped by pattern).
//
// permConfigRead re-resolves the 3-layer config chain on each request so a
// newly-saved override under $OMNIS_HOME/config is picked up immediately.
// permConfigWrite is the fixed write target under $OMNIS_HOME/config.
func registerRemotePermissionsRegistryRoutes(
	rg *gin.RouterGroup,
	readPath func() string,
	writePath string,
	permConfigRead func() string,
	permConfigWrite string,
) {
	registerRemoteRegistryCRUD(rg, readPath, writePath, registries.KindPermissions)

	// GET /remotes/:id/browse — list permission rule-sets in the remote tree.
	rg.GET("/remotes/:id/browse", func(c *gin.Context) {
		reg, ref, ok := loadRegistryForKind(c, readPath, c.Param("id"), registries.KindPermissions)
		if !ok {
			return
		}
		installedPatterns := registries.InstalledPermissionPatterns(permConfigRead())
		perms, err := registries.BrowsePermissions(ref, reg.Token, installedPatterns)
		if err != nil {
			c.JSON(http.StatusBadGateway, skillsErr("REMOTE_ERROR", err.Error()))
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"permissions": perms,
			"registry":    toPublicRemote(*reg),
		})
	})

	// GET /remotes/:id/permission/*dirpath — fetch raw permissions.json for preview.
	rg.GET("/remotes/:id/permission/*dirpath", func(c *gin.Context) {
		dirPath := strings.Trim(c.Param("dirpath"), "/")
		if dirPath == "" {
			c.JSON(http.StatusBadRequest, skillsErr("BAD_REQUEST", "dirpath is required"))
			return
		}
		reg, ref, ok := loadRegistryForKind(c, readPath, c.Param("id"), registries.KindPermissions)
		if !ok {
			return
		}
		body, err := registries.FetchPermissionJSON(ref, reg.Token, dirPath)
		if err != nil {
			c.JSON(http.StatusBadGateway, skillsErr("REMOTE_ERROR", err.Error()))
			return
		}
		c.JSON(http.StatusOK, gin.H{"content": string(body)})
	})

	// POST /remotes/:id/install/*dirpath — merge a permission rule-set into
	// permissions.json (deduped by pattern; idempotent).
	rg.POST("/remotes/:id/install/*dirpath", func(c *gin.Context) {
		dirPath := strings.Trim(c.Param("dirpath"), "/")
		if dirPath == "" {
			c.JSON(http.StatusBadRequest, skillsErr("BAD_REQUEST", "dirpath is required"))
			return
		}
		reg, ref, ok := loadRegistryForKind(c, readPath, c.Param("id"), registries.KindPermissions)
		if !ok {
			return
		}
		raw, err := registries.FetchPermissionJSON(ref, reg.Token, dirPath)
		if err != nil {
			c.JSON(http.StatusBadGateway, skillsErr("INSTALL_ERROR", err.Error()))
			return
		}
		added, err := registries.MergePermissionsFile(permConfigRead(), permConfigWrite, raw)
		if err != nil {
			c.JSON(http.StatusInternalServerError, skillsErr("FS_ERROR", err.Error()))
			return
		}
		c.JSON(http.StatusCreated, gin.H{
			"name":  registries.PermissionSetName(dirPath),
			"added": added > 0,
			"rules": added,
		})
	})
}

// permissionsRoutesDeps bundles the resolved paths required by the permissions
// remote-registry routes.
type permissionsRoutesDeps struct {
	RemoteRegistriesWrite string
	RemoteRegistriesRead  func() string
	PermConfigRead        func() string
	PermConfigWrite       string
}

// resolvePermissionsRoutesDeps derives the dep bundle from standard path
// conventions, mirroring resolveA2ARoutesDeps / resolveMCPRoutesDeps.
func resolvePermissionsRoutesDeps() permissionsRoutesDeps {
	absRemoteWrite, _ := filepath.Abs(filepath.Join(paths.ConfigWriteDir(), registries.ConfigFileName))
	absPermWrite, _ := filepath.Abs(filepath.Join(paths.ConfigWriteDir(), "permissions.json"))
	return permissionsRoutesDeps{
		RemoteRegistriesWrite: absRemoteWrite,
		RemoteRegistriesRead: func() string {
			p, _ := filepath.Abs(paths.FindConfig(registries.ConfigFileName))
			return p
		},
		PermConfigRead: func() string {
			p, _ := filepath.Abs(paths.FindConfig("permissions.json"))
			return p
		},
		PermConfigWrite: absPermWrite,
	}
}

// registerPermissionsRoutes mounts the /api/permissions-registry/* remote-registry
// routes. Called from server.go alongside the other register*Routes.
func registerPermissionsRoutes(rg *gin.RouterGroup) {
	deps := resolvePermissionsRoutesDeps()
	registerRemotePermissionsRegistryRoutes(
		rg,
		deps.RemoteRegistriesRead,
		deps.RemoteRegistriesWrite,
		deps.PermConfigRead,
		deps.PermConfigWrite,
	)
}

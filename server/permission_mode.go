package main

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/blouargant/omnis/core/permissions"
	"github.com/blouargant/omnis/internal/sessions"
)

type permModeError struct {
	status int
	msg    string
}

// validatePermissionMode parses a requested session mode. "" is valid (clear /
// no override). Unknown ⇒ 400; bypassPermissions while disabled by policy ⇒ 403.
func validatePermissionMode(d serverDeps, raw string) (permissions.Mode, *permModeError) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return permissions.ModeDefault, nil
	}
	m, ok := permissions.ParseModeStrict(raw)
	if !ok {
		return permissions.ModeDefault, &permModeError{http.StatusBadRequest, "unknown permission mode"}
	}
	if m == permissions.ModeBypass && d.PermModes.BypassDisabled() {
		return permissions.ModeDefault, &permModeError{http.StatusForbidden, "bypassPermissions is disabled by policy"}
	}
	return m, nil
}

// applyPermissionMode installs the override in the live store and the
// registry/conversation file. An empty mode (use applyPermissionModeString)
// clears it.
func applyPermissionMode(d serverDeps, id string, m permissions.Mode) {
	d.PermModes.Set(id, m)
	d.Registry.SetPermissionMode(id, m.String())
	_ = sessions.SetConversationPermissionMode(id, m.String())
}

func clearPermissionMode(d serverDeps, id string) {
	d.PermModes.Clear(id)
	d.Registry.SetPermissionMode(id, "")
	_ = sessions.SetConversationPermissionMode(id, "")
}

// copyPermissionMode copies src's override (if any) to dst: store + registry +
// file. Used by fork and spawn.
func copyPermissionMode(d serverDeps, src, dst string) {
	if m, ok := d.PermModes.Get(src); ok {
		applyPermissionMode(d, dst, m)
	}
}

func permissionModePayload(d serverDeps, id string) gin.H {
	override := ""
	if m, ok := d.PermModes.Get(id); ok {
		override = m.String()
	}
	return gin.H{
		"mode":            override,
		"effective":       d.PermModes.Effective(id).String(),
		"config_mode":     d.PermModes.ConfigMode().String(),
		"bypass_disabled": d.PermModes.BypassDisabled(),
	}
}

func handlePermissionModeGet(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		if _, ok := d.Registry.Get(id); !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
			return
		}
		c.JSON(http.StatusOK, permissionModePayload(d, id))
	}
}

func handlePermissionModeSet(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		meta, ok := d.Registry.Get(id)
		if !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
			return
		}
		if meta.Archived {
			c.JSON(http.StatusConflict, gin.H{"error": "session is archived; unarchive it to change its permission mode"})
			return
		}
		var req struct {
			Mode string `json:"mode"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON body"})
			return
		}
		m, perr := validatePermissionMode(d, req.Mode)
		if perr != nil {
			c.JSON(perr.status, gin.H{"error": perr.msg})
			return
		}
		if strings.TrimSpace(req.Mode) == "" {
			clearPermissionMode(d, id)
		} else {
			applyPermissionMode(d, id, m)
		}
		payload := permissionModePayload(d, id)
		if d.PushEvents != nil {
			d.PushEvents.broadcastData("permission_mode_changed", id, map[string]any{
				"mode":      payload["mode"],
				"effective": payload["effective"],
			})
		}
		c.JSON(http.StatusOK, payload)
	}
}

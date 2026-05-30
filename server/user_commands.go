package main

import (
	"errors"
	"fmt"
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"

	"github.com/blouargant/yoke/internal/usercommands"
)

// userCommand / errNotFound / normName / reservedNames / validateCommand are
// thin aliases over the shared internal/usercommands package, which owns the
// on-disk schema, validation, and reserved-name rules so the web-UI routes here
// and the agent-side crawler command install agree on the format.
type userCommand = usercommands.Command

var errNotFound = usercommands.ErrNotFound

func normName(s string) string { return usercommands.NormName(s) }

var reservedNames = usercommands.ReservedNames

func validateCommand(c *userCommand) error { return usercommands.Validate(c) }

// userCommandsStore persists user commands to the per-user user_commands.json
// file. It is a thin mutex wrapper over the shared usercommands package.
type userCommandsStore struct {
	path string
	mu   sync.Mutex
}

func newUserCommandsStore() *userCommandsStore {
	return &userCommandsStore{path: usercommands.DefaultPath()}
}

func (s *userCommandsStore) list() []userCommand {
	s.mu.Lock()
	defer s.mu.Unlock()
	return usercommands.Load(s.path)
}

// upsert inserts or replaces cmd by Name. Returns the resulting list.
func (s *userCommandsStore) upsert(cmd userCommand, originalName string) ([]userCommand, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cmds, _, err := usercommands.Upsert(s.path, cmd, originalName)
	return cmds, err
}

func (s *userCommandsStore) delete(name string) ([]userCommand, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return usercommands.Delete(s.path, name)
}

func registerUserCommandsRoutes(rg *gin.RouterGroup, store *userCommandsStore) {
	rg.GET("/user-commands", func(c *gin.Context) {
		cmds := store.list()
		if cmds == nil {
			cmds = []userCommand{}
		}
		c.JSON(http.StatusOK, gin.H{"commands": cmds})
	})

	rg.POST("/user-commands", func(c *gin.Context) {
		var req userCommand
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON body"})
			return
		}
		if err := validateCommand(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		// Reject when the name already exists; updates must go through PUT.
		for _, existing := range store.list() {
			if normName(existing.Name) == req.Name {
				c.JSON(http.StatusConflict, gin.H{"error": fmt.Sprintf("command /%s already exists", req.Name)})
				return
			}
		}
		cmds, err := store.upsert(req, "")
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"commands": cmds})
	})

	rg.PUT("/user-commands/:name", func(c *gin.Context) {
		original := normName(c.Param("name"))
		if original == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "name required"})
			return
		}
		var req userCommand
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON body"})
			return
		}
		if err := validateCommand(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		// If renaming, ensure the new name doesn't collide with another entry.
		if req.Name != original {
			for _, existing := range store.list() {
				if normName(existing.Name) == req.Name {
					c.JSON(http.StatusConflict, gin.H{"error": fmt.Sprintf("command /%s already exists", req.Name)})
					return
				}
			}
		}
		cmds, err := store.upsert(req, original)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"commands": cmds})
	})

	rg.DELETE("/user-commands/:name", func(c *gin.Context) {
		name := normName(c.Param("name"))
		cmds, err := store.delete(name)
		if errors.Is(err, errNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "command not found"})
			return
		}
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"commands": cmds})
	})
}

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"

	"github.com/blouargant/yoke/internal/paths"
)

// userCommand is a single user-defined slash command. The Name is the
// lookup key (without the leading slash); Prompt is the template body
// that gets sent to the agent when the command is invoked, with $1..$N
// (positional) and $* (all remaining args) substituted client-side.
type userCommand struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Args        string `json:"args,omitempty"`
	Prompt      string `json:"prompt"`
}

type userCommandsFile struct {
	Commands []userCommand `json:"commands"`
}

// userCommandsStore persists user commands to a single JSON file under
// $YOKE_HOME/config. Mirrors preferencesStore: tiny payload, one mutex,
// no fancier indexing needed.
type userCommandsStore struct {
	path string
	mu   sync.Mutex
}

func newUserCommandsStore() *userCommandsStore {
	return &userCommandsStore{path: filepath.Join(paths.ConfigWriteDir(), "user_commands.json")}
}

func (s *userCommandsStore) loadLocked() []userCommand {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return nil
	}
	var f userCommandsFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil
	}
	return f.Commands
}

func (s *userCommandsStore) saveLocked(cmds []userCommand) error {
	if cmds == nil {
		cmds = []userCommand{}
	}
	data, err := json.MarshalIndent(userCommandsFile{Commands: cmds}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0o644)
}

func (s *userCommandsStore) list() []userCommand {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadLocked()
}

// upsert inserts or replaces cmd by Name. Returns the resulting list.
func (s *userCommandsStore) upsert(cmd userCommand, originalName string) ([]userCommand, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cmds := s.loadLocked()
	target := normName(cmd.Name)
	cmd.Name = target
	orig := normName(originalName)
	// First, drop the original entry if we're renaming.
	if orig != "" && orig != target {
		cmds = removeByName(cmds, orig)
	}
	// Replace existing entry with the same name if any.
	replaced := false
	for i := range cmds {
		if normName(cmds[i].Name) == target {
			cmds[i] = cmd
			replaced = true
			break
		}
	}
	if !replaced {
		cmds = append(cmds, cmd)
	}
	sort.SliceStable(cmds, func(i, j int) bool {
		return cmds[i].Name < cmds[j].Name
	})
	if err := s.saveLocked(cmds); err != nil {
		return nil, err
	}
	return cmds, nil
}

func (s *userCommandsStore) delete(name string) ([]userCommand, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cmds := s.loadLocked()
	target := normName(name)
	out := removeByName(cmds, target)
	if len(out) == len(cmds) {
		return cmds, errNotFound
	}
	if err := s.saveLocked(out); err != nil {
		return nil, err
	}
	return out, nil
}

var errNotFound = errors.New("not found")

func removeByName(cmds []userCommand, name string) []userCommand {
	out := cmds[:0]
	for _, c := range cmds {
		if normName(c.Name) != name {
			out = append(out, c)
		}
	}
	return out
}

func normName(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "/")
	return strings.ToLower(s)
}

// nameRe matches the allowed shape for a command name: alphanumerics,
// hyphens, and underscores; 1..40 chars. Keeps the slash menu sane and
// avoids ambiguity with prompt content.
var nameRe = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,40}$`)

// reservedNames are the built-in commands handled in app.js. User commands
// cannot shadow them.
var reservedNames = map[string]struct{}{
	"help":         {},
	"compress":     {},
	"create-skill": {},
	"update-skill": {},
	"status":       {},
	"learn":        {},
	"learn-now":    {},
}

func validateCommand(c *userCommand) error {
	c.Name = normName(c.Name)
	if c.Name == "" {
		return fmt.Errorf("name is required")
	}
	if !nameRe.MatchString(c.Name) {
		return fmt.Errorf("name %q must be 1-40 chars of letters, digits, '-' or '_'", c.Name)
	}
	if _, ok := reservedNames[c.Name]; ok {
		return fmt.Errorf("name %q is reserved by a built-in command", c.Name)
	}
	c.Description = strings.TrimSpace(c.Description)
	c.Args = strings.TrimSpace(c.Args)
	c.Prompt = strings.TrimRight(c.Prompt, "\n")
	if c.Prompt == "" {
		return fmt.Errorf("prompt is required")
	}
	return nil
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

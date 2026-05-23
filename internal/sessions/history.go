package sessions

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/blouargant/yoke/agent"
	"github.com/blouargant/yoke/internal/paths"
)

// logsDir returns the per-user logs directory ($YOKE_HOME/logs). Resolved
// at each call so tests can redirect via t.Setenv("YOKE_HOME", ...).
func logsDir() string { return paths.LogsDir() }

// ConversationTurn is one user→assistant exchange persisted to disk.
type ConversationTurn struct {
	UserText      string    `json:"user_text"`
	AssistantText string    `json:"assistant_text"`
	At            time.Time `json:"at"`
}

// ConversationFile is the on-disk format for a session's history.
// Legacy files used a plain JSON array; those are read transparently.
type ConversationFile struct {
	Title     string             `json:"title,omitempty"`
	Squad     string             `json:"squad,omitempty"`
	Harvested bool               `json:"harvested,omitempty"`
	Turns     []ConversationTurn `json:"turns"`
}

// ConversationPath returns the on-disk path for a session's conversation file.
func ConversationPath(sessionID string) string {
	return filepath.Join(logsDir(), fmt.Sprintf("conversation_%s.json", sessionID))
}

// LoadConversationFile reads a session's conversation file, transparently
// migrating legacy plain-array files into the current envelope shape.
// A missing file is not an error and returns an empty ConversationFile.
func LoadConversationFile(sessionID string) (*ConversationFile, error) {
	data, err := os.ReadFile(ConversationPath(sessionID))
	if err != nil {
		if os.IsNotExist(err) {
			return &ConversationFile{}, nil
		}
		return nil, err
	}
	// Transparently migrate legacy plain-array format.
	if len(data) > 0 && data[0] == '[' {
		var turns []ConversationTurn
		if err := json.Unmarshal(data, &turns); err != nil {
			return nil, err
		}
		return &ConversationFile{Turns: turns}, nil
	}
	var f ConversationFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, err
	}
	return &f, nil
}

// SaveConversationFile writes f to disk under the session's conversation path.
func SaveConversationFile(sessionID string, f *ConversationFile) error {
	if err := os.MkdirAll(logsDir(), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(ConversationPath(sessionID), data, 0644)
}

// LoadConversationTurns returns just the turn list for a session.
func LoadConversationTurns(sessionID string) ([]ConversationTurn, error) {
	f, err := LoadConversationFile(sessionID)
	if err != nil {
		return nil, err
	}
	return f.Turns, nil
}

// AppendConversationTurn appends one user→assistant exchange and clears
// the Harvested flag so a fresh idle scan re-evaluates the session.
func AppendConversationTurn(sessionID, userText, assistantText string) error {
	f, err := LoadConversationFile(sessionID)
	if err != nil || f == nil {
		f = &ConversationFile{}
	}
	f.Turns = append(f.Turns, ConversationTurn{
		UserText:      userText,
		AssistantText: assistantText,
		At:            time.Now(),
	})
	f.Harvested = false // new activity resets the harvest flag
	return SaveConversationFile(sessionID, f)
}

// SetConversationHarvested persists the Harvested flag to disk without
// touching the conversation turns. Called by the idle harvester.
func SetConversationHarvested(sessionID string, v bool) error {
	f, err := LoadConversationFile(sessionID)
	if err != nil || f == nil {
		f = &ConversationFile{}
	}
	f.Harvested = v
	return SaveConversationFile(sessionID, f)
}

// SetConversationSquad persists the squad name to disk without touching the
// conversation turns. Called when a new session is first created so the
// choice survives a server restart.
func SetConversationSquad(sessionID, squad string) error {
	f, err := LoadConversationFile(sessionID)
	if err != nil || f == nil {
		f = &ConversationFile{}
	}
	f.Squad = squad
	return SaveConversationFile(sessionID, f)
}

// SetConversationTitle persists the session title without touching turns.
func SetConversationTitle(sessionID, title string) error {
	f, err := LoadConversationFile(sessionID)
	if err != nil || f == nil {
		f = &ConversationFile{}
	}
	f.Title = title
	return SaveConversationFile(sessionID, f)
}

// DeleteConversationFile removes the on-disk file for a session.
// A missing file is not an error.
func DeleteConversationFile(sessionID string) {
	if err := os.Remove(ConversationPath(sessionID)); err != nil && !os.IsNotExist(err) {
		log.Printf("history: failed to delete conversation %s: %v", sessionID, err)
	}
}

// DeleteSessionLogs removes all per-session log files produced by the agent
// runtime: tasks, todo, memory, statelog, and mailbox JSONL files. The
// conversation file is deleted separately by DeleteConversationFile via
// Registry.Delete.
func DeleteSessionLogs(userID, sessionID string) {
	suffix := agent.SessionSuffix(userID, sessionID)
	for _, name := range []string{
		fmt.Sprintf("agent_tasks_%s.json", suffix),
		fmt.Sprintf("agent_todo_%s.json", suffix),
		fmt.Sprintf("agent_memory_%s.md", suffix),
		fmt.Sprintf("agent_statelog_%s.json", suffix),
	} {
		_ = os.Remove(filepath.Join(logsDir(), name))
	}
	// Delete per-session mailbox files: $YOKE_HOME/mailboxes/<suffix>:*.jsonl
	matches, _ := filepath.Glob(filepath.Join(paths.MailboxesDir(), suffix+":*.jsonl"))
	for _, f := range matches {
		_ = os.Remove(f)
	}
}

// LoadPersistedSessions scans logs/ for conversation_*.json files and returns
// a SessionMeta for each, so the sidebar populates after a process restart.
func LoadPersistedSessions() []*SessionMeta {
	entries, err := os.ReadDir(logsDir())
	if err != nil {
		return nil
	}
	var out []*SessionMeta
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "conversation_") || !strings.HasSuffix(name, ".json") {
			continue
		}
		id := strings.TrimSuffix(strings.TrimPrefix(name, "conversation_"), ".json")
		f, err := LoadConversationFile(id)
		if err != nil || f == nil || len(f.Turns) == 0 {
			continue
		}
		out = append(out, &SessionMeta{
			ID:         id,
			Title:      f.Title,
			Squad:      f.Squad,
			Harvested:  f.Harvested,
			UserID:     DefaultUserID,
			CreatedAt:  f.Turns[0].At,
			LastUsedAt: f.Turns[len(f.Turns)-1].At,
			Turns:      len(f.Turns),
		})
	}
	return out
}

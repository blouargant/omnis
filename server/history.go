package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/blouargant/yoke/agent"
)

const logsDir = "logs"

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
	Harvested bool               `json:"harvested,omitempty"`
	Turns     []ConversationTurn `json:"turns"`
}

func conversationPath(sessionID string) string {
	return filepath.Join(logsDir, fmt.Sprintf("conversation_%s.json", sessionID))
}

func loadConversationFile(sessionID string) (*ConversationFile, error) {
	data, err := os.ReadFile(conversationPath(sessionID))
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

func saveConversationFile(sessionID string, f *ConversationFile) error {
	if err := os.MkdirAll(logsDir, 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(conversationPath(sessionID), data, 0644)
}

func loadConversationTurns(sessionID string) ([]ConversationTurn, error) {
	f, err := loadConversationFile(sessionID)
	if err != nil {
		return nil, err
	}
	return f.Turns, nil
}

func appendConversationTurn(sessionID, userText, assistantText string) error {
	f, err := loadConversationFile(sessionID)
	if err != nil || f == nil {
		f = &ConversationFile{}
	}
	f.Turns = append(f.Turns, ConversationTurn{
		UserText:      userText,
		AssistantText: assistantText,
		At:            time.Now(),
	})
	f.Harvested = false // new activity resets the harvest flag
	return saveConversationFile(sessionID, f)
}

// setConversationHarvested persists the Harvested flag to disk without
// touching the conversation turns. Called by the idle harvester.
func setConversationHarvested(sessionID string, v bool) error {
	f, err := loadConversationFile(sessionID)
	if err != nil || f == nil {
		f = &ConversationFile{}
	}
	f.Harvested = v
	return saveConversationFile(sessionID, f)
}

func setConversationTitle(sessionID, title string) error {
	f, err := loadConversationFile(sessionID)
	if err != nil || f == nil {
		f = &ConversationFile{}
	}
	f.Title = title
	return saveConversationFile(sessionID, f)
}

func deleteConversationFile(sessionID string) {
	if err := os.Remove(conversationPath(sessionID)); err != nil && !os.IsNotExist(err) {
		log.Printf("history: failed to delete conversation %s: %v", sessionID, err)
	}
}

// deleteSessionLogs removes all per-session log files produced by the agent
// runtime: tasks, todo, memory, statelog, and mailbox JSONL files. The
// conversation file is deleted separately by deleteConversationFile via
// registry.Delete.
func deleteSessionLogs(userID, sessionID string) {
	suffix := agent.SessionSuffix(userID, sessionID)
	for _, name := range []string{
		fmt.Sprintf("agent_tasks_%s.json", suffix),
		fmt.Sprintf("agent_todo_%s.json", suffix),
		fmt.Sprintf("agent_memory_%s.md", suffix),
		fmt.Sprintf("agent_statelog_%s.json", suffix),
	} {
		_ = os.Remove(filepath.Join(logsDir, name))
	}
	// Delete per-session mailbox files: .mailboxes/<suffix>:*.jsonl
	matches, _ := filepath.Glob(filepath.Join(".mailboxes", suffix+":*.jsonl"))
	for _, f := range matches {
		_ = os.Remove(f)
	}
}

// loadPersistedSessions scans logs/ for conversation_*.json files and returns
// a SessionMeta for each, so the sidebar populates after a server restart.
func loadPersistedSessions() []*SessionMeta {
	entries, err := os.ReadDir(logsDir)
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
		f, err := loadConversationFile(id)
		if err != nil || f == nil || len(f.Turns) == 0 {
			continue
		}
		out = append(out, &SessionMeta{
			ID:         id,
			Title:      f.Title,
			Harvested:  f.Harvested,
			UserID:     defaultUserID,
			CreatedAt:  f.Turns[0].At,
			LastUsedAt: f.Turns[len(f.Turns)-1].At,
			Turns:      len(f.Turns),
		})
	}
	return out
}

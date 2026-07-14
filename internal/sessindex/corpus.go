// corpus.go — reading the session corpus off disk.
//
// The corpus is the persisted conversation files ($OMNIS_HOME/logs/
// conversation_<id>.json). This package deliberately does NOT import
// internal/sessions: that package imports agent (for SessionSuffix), and agent
// imports this one (Infrastructure.SessionIndex + the `sessions` tool group), so
// reusing its loader would close an import cycle. The on-disk shape is stable and
// small, so a private, read-only view of it is cheaper than restructuring both
// packages — but it must stay in step with sessions.ConversationFile.
package sessindex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/blouargant/omnis/internal/paths"
)

// turn is one persisted user→assistant exchange. Only the two text fields are
// read: they are exactly what the user saw, which is what we index and search.
// Tool calls, usage and durations are deliberately ignored.
type turn struct {
	UserText      string    `json:"user_text"`
	AssistantText string    `json:"assistant_text"`
	At            time.Time `json:"at"`
}

// conv is the read-only view of a session's conversation file.
type conv struct {
	ID         string
	Title      string `json:"title,omitempty"`
	Collection string `json:"collection,omitempty"`
	Archived   bool   `json:"archived,omitempty"`
	Hidden     bool   `json:"hidden,omitempty"`
	Turns      []turn `json:"turns"`
}

// LastAt returns the timestamp of the session's most recent turn.
func (c *conv) LastAt() time.Time {
	if len(c.Turns) == 0 {
		return time.Time{}
	}
	return c.Turns[len(c.Turns)-1].At
}

// convPath is the on-disk path of a session's conversation file.
func convPath(id string) string {
	return filepath.Join(paths.LogsDir(), "conversation_"+id+".json")
}

// listSessionIDs returns the ids of every persisted conversation, sorted.
func listSessionIDs() []string {
	entries, err := os.ReadDir(paths.LogsDir())
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "conversation_") || !strings.HasSuffix(name, ".json") {
			continue
		}
		out = append(out, strings.TrimSuffix(strings.TrimPrefix(name, "conversation_"), ".json"))
	}
	return out
}

// loadConv reads one session's conversation file. A missing, empty, or corrupt
// file yields (nil, nil) — indexing and searching are best-effort and must never
// fail the caller over one bad file. Legacy plain-array files are read too.
func loadConv(id string) (*conv, []byte, error) {
	data, err := os.ReadFile(convPath(id))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	if len(data) == 0 {
		return nil, nil, nil
	}
	c := &conv{ID: id}
	if data[0] == '[' { // legacy plain-array transcript
		if err := json.Unmarshal(data, &c.Turns); err != nil {
			return nil, nil, nil
		}
		return c, data, nil
	}
	if err := json.Unmarshal(data, c); err != nil {
		return nil, nil, nil
	}
	c.ID = id
	return c, data, nil
}

// searchable reports whether a session belongs in the search corpus. Hidden
// utility sessions (the in-Settings assistant, the search agent's own session)
// are excluded: surfacing them would mean the search results are polluted by the
// searches themselves. Archived sessions ARE included — being able to find them
// is the point.
func (c *conv) searchable() bool {
	return c != nil && !c.Hidden && len(c.Turns) > 0
}

// removeConv deletes a session's conversation file. Test helper for the
// deleted-session path (an indexed session whose file disappears).
func removeConv(id string) error {
	return os.Remove(convPath(id))
}

package sessions

import (
	"strings"
	"time"
)

// ProducedFile is a file an agent created during the session (a Write to a
// path that did not exist yet). The web UI lists these in the environment
// panel's Résultats card with a download button. Persisted so the list
// survives a reload; Turn lets a rewind / fork drop files from cut turns.
type ProducedFile struct {
	Path string    `json:"path"`
	Turn int       `json:"turn"`
	At   time.Time `json:"at"`
}

// RecordProducedFile adds path to the session's produced files, or refreshes
// the time of an existing entry (keeping its first turn).
func RecordProducedFile(sessionID, path string) error {
	path = strings.TrimSpace(path)
	if sessionID == "" || path == "" {
		return nil
	}
	return mutateConversation(sessionID, func(f *ConversationFile) {
		for i := range f.Produced {
			if f.Produced[i].Path == path {
				f.Produced[i].At = time.Now()
				return
			}
		}
		f.Produced = append(f.Produced, ProducedFile{Path: path, Turn: len(f.Turns), At: time.Now()})
	})
}

// LoadProducedFiles returns the session's produced files (nil when none).
func LoadProducedFiles(sessionID string) ([]ProducedFile, error) {
	f, err := LoadConversationFile(sessionID)
	if err != nil {
		return nil, err
	}
	return f.Produced, nil
}

// producedBefore returns the entries first produced before turn `keep`.
func producedBefore(files []ProducedFile, keep int) []ProducedFile {
	var out []ProducedFile
	for _, p := range files {
		if p.Turn < keep {
			out = append(out, p)
		}
	}
	return out
}

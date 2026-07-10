package sessions

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/blouargant/omnis/internal/paths"
)

// GeneralCollection is the name of the virtual default collection. A session
// with an empty SessionMeta.Collection field belongs here. General is never
// stored in collections.json and cannot be renamed or deleted — it is the
// always-present bucket for sessions not filed under a user-created collection.
const GeneralCollection = "General"

// MaxCollectionNameLen bounds a collection name so it stays a sane label and a
// safe gin path segment.
const MaxCollectionNameLen = 60

// collectionsMu serialises the read-modify-write of collections.json, mirroring
// the per-session convLocks discipline (there is a single shared file here, so a
// single mutex suffices).
var collectionsMu sync.Mutex

// collectionsFile is the on-disk shape of collections.json: an ordered list of
// user-created collection names. General is virtual and never appears here.
type collectionsFile struct {
	Collections []string `json:"collections"`
}

// CollectionsPath returns the on-disk path for the collections list. Resolved
// at each call so tests can redirect via t.Setenv("OMNIS_HOME", ...).
func CollectionsPath() string {
	return filepath.Join(paths.ConfigWriteDir(), "collections.json")
}

// NormalizeCollectionName trims a collection name and folds the General bucket
// (blank or "General" in any case) to the empty string, which is how a General
// session is stored. Any other name is returned trimmed and unchanged.
func NormalizeCollectionName(name string) string {
	n := strings.TrimSpace(name)
	if n == "" || strings.EqualFold(n, GeneralCollection) {
		return ""
	}
	return n
}

// ValidCollectionName reports whether name is an acceptable user-created
// collection label: non-blank, not "General" (reserved for the virtual bucket),
// within the length cap, no path separators or control characters (so it is safe
// as a gin path segment and a display label).
func ValidCollectionName(name string) bool {
	n := strings.TrimSpace(name)
	if n == "" || len(n) > MaxCollectionNameLen {
		return false
	}
	if strings.EqualFold(n, GeneralCollection) {
		return false
	}
	for _, r := range n {
		if r < 0x20 || r == '/' || r == '\\' || r == 0x7f {
			return false
		}
	}
	return true
}

// loadCollectionsLocked reads collections.json. A missing/empty file yields an
// empty list. Must be called with collectionsMu held.
func loadCollectionsLocked() ([]string, error) {
	data, err := os.ReadFile(CollectionsPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if len(data) == 0 {
		return nil, nil
	}
	var f collectionsFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, err
	}
	return f.Collections, nil
}

// saveCollectionsLocked writes the ordered name list atomically (temp file +
// rename, like SaveConversationFile). Must be called with collectionsMu held.
func saveCollectionsLocked(names []string) error {
	dir := paths.ConfigWriteDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(collectionsFile{Collections: names}, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "collections_*.json.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = tmp.Close(); _ = os.Remove(tmpName) }
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Chmod(tmpName, 0644); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, CollectionsPath()); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}

// ListCollections returns the ordered user-created collection names (excluding
// the virtual General). A missing file yields an empty slice.
func ListCollections() ([]string, error) {
	collectionsMu.Lock()
	defer collectionsMu.Unlock()
	return loadCollectionsLocked()
}

// indexOfFold returns the index of name in names using case-insensitive
// comparison, or -1 when absent.
func indexOfFold(names []string, name string) int {
	for i, n := range names {
		if strings.EqualFold(n, name) {
			return i
		}
	}
	return -1
}

// AddCollection appends a new collection to the list if it is valid and not
// already present (case-insensitive). Returns the updated ordered list and
// whether a new entry was added. An invalid name is an error.
func AddCollection(name string) ([]string, bool, error) {
	name = strings.TrimSpace(name)
	if !ValidCollectionName(name) {
		return nil, false, fmt.Errorf("invalid collection name %q", name)
	}
	collectionsMu.Lock()
	defer collectionsMu.Unlock()
	names, err := loadCollectionsLocked()
	if err != nil {
		return nil, false, err
	}
	if indexOfFold(names, name) >= 0 {
		return names, false, nil // already present — idempotent
	}
	names = append(names, name)
	if err := saveCollectionsLocked(names); err != nil {
		return nil, false, err
	}
	return names, true, nil
}

// RemoveCollection drops name from the list (case-insensitive). Returns the
// updated list and whether an entry was removed. Cascading member sessions back
// to General is the caller's responsibility (it owns the session registry).
func RemoveCollection(name string) ([]string, bool, error) {
	collectionsMu.Lock()
	defer collectionsMu.Unlock()
	names, err := loadCollectionsLocked()
	if err != nil {
		return nil, false, err
	}
	i := indexOfFold(names, name)
	if i < 0 {
		return names, false, nil
	}
	names = append(names[:i:i], names[i+1:]...)
	if err := saveCollectionsLocked(names); err != nil {
		return nil, false, err
	}
	return names, true, nil
}

// RenameCollection replaces old with newName in the list, preserving its
// position. Returns the updated list and whether the rename happened. It errors
// on an invalid newName or when newName collides with a different existing
// collection. Cascading member sessions to the new name is the caller's
// responsibility.
func RenameCollection(old, newName string) ([]string, bool, error) {
	newName = strings.TrimSpace(newName)
	if !ValidCollectionName(newName) {
		return nil, false, fmt.Errorf("invalid collection name %q", newName)
	}
	collectionsMu.Lock()
	defer collectionsMu.Unlock()
	names, err := loadCollectionsLocked()
	if err != nil {
		return nil, false, err
	}
	i := indexOfFold(names, old)
	if i < 0 {
		return names, false, nil
	}
	// A no-op rename to the same name (possibly different case) just updates the
	// stored casing. A collision with a *different* existing entry is rejected.
	if j := indexOfFold(names, newName); j >= 0 && j != i {
		return names, false, fmt.Errorf("collection %q already exists", newName)
	}
	names[i] = newName
	if err := saveCollectionsLocked(names); err != nil {
		return nil, false, err
	}
	return names, true, nil
}

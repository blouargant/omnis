package sessions

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/blouargant/omnis/internal/collectionctx"
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
// user-created collection names, plus an optional per-collection colour map
// (keyed by the canonical stored name). General is virtual and never appears
// here. The colour is a short palette token (e.g. "blue") chosen by the web UI;
// the actual rendered colour is resolved theme-side from that token, so no hex
// value is stored.
type collectionsFile struct {
	Collections []string          `json:"collections"`
	Colors      map[string]string `json:"colors,omitempty"`
	// Profiles carries per-collection scalar defaults seeded onto new chats
	// filed under the collection (keyed by the canonical stored name). The prose
	// (instructions/memory) lives in per-collection files under internal/collectionctx;
	// only these small scalars live in this atomic single-file.
	Profiles map[string]collectionProfile `json:"profiles,omitempty"`
}

// collectionProfile is a collection's per-session defaults. Squad is the
// starting squad a new chat in this collection seeds onto (a hint, not a lock —
// routing still runs and the squad can hand back to the router); Cwd is the
// directory a new chat starts in. Both empty ⇒ the collection carries no
// defaults and new chats behave exactly as before.
type collectionProfile struct {
	Squad string `json:"squad,omitempty"`
	Cwd   string `json:"cwd,omitempty"`
	// MemorySize bounds the distilled/injected memory ("" | "small" | "medium" |
	// "large"; "" ⇒ medium). It is a soft target: it caps the distiller and drives
	// the editor word-counter, but never truncates manually typed memory.
	MemorySize string `json:"memory_size,omitempty"`
	// AutoUpdate turns on the server-side idle memory distiller for this
	// collection (opt-in, default off).
	AutoUpdate bool `json:"auto_update,omitempty"`
	// LastMemoryUpdate is the unix-seconds time of the last AUTOMATIC memory
	// commit; 0 ⇒ none. Drives the min-interval gate and the "auto-updated N ago"
	// marker; cleared on revert or a manual memory save.
	LastMemoryUpdate int64 `json:"last_memory_update,omitempty"`
	// Role marks this collection as a fleet project ("project") vs a plain
	// collection (""). Fleet fields are inert unless Role == "project".
	Role string `json:"role,omitempty"`
	// Engine selects the fleet worker backing this project: "omnis" | "claude".
	Engine string `json:"engine,omitempty"`
	// DependsOn lists the collection names this project depends on (the
	// cross-project dependency edges used to order fleet work).
	DependsOn []string `json:"depends_on,omitempty"`
}

// isEmpty reports whether the profile carries no data. Used instead of `==`
// because DependsOn ([]string) makes collectionProfile non-comparable.
func (p collectionProfile) isEmpty() bool {
	return p.Squad == "" && p.Cwd == "" && p.MemorySize == "" &&
		!p.AutoUpdate && p.LastMemoryUpdate == 0 &&
		p.Role == "" && p.Engine == "" && len(p.DependsOn) == 0
}

// CollectionProfileData is a collection's full per-collection scalar bag.
type CollectionProfileData struct {
	Squad            string
	Cwd              string
	MemorySize       string
	AutoUpdate       bool
	LastMemoryUpdate int64
	Role             string
	Engine           string
	DependsOn        []string
}

// CollectionProfileFull returns the full stored per-collection scalars. A missing
// collection or no recorded profile yields the zero value.
func CollectionProfileFull(name string) CollectionProfileData {
	collectionsMu.Lock()
	defer collectionsMu.Unlock()
	f, err := loadFileLocked()
	if err != nil {
		return CollectionProfileData{}
	}
	i := indexOfFold(f.Collections, name)
	if i < 0 {
		return CollectionProfileData{}
	}
	p := f.Profiles[f.Collections[i]]
	return CollectionProfileData{
		Squad: p.Squad, Cwd: p.Cwd, MemorySize: p.MemorySize,
		AutoUpdate: p.AutoUpdate, LastMemoryUpdate: p.LastMemoryUpdate,
		Role: p.Role, Engine: p.Engine, DependsOn: cloneStrings(p.DependsOn),
	}
}

// UpdateCollectionProfile applies mutate to a collection's scalar profile under a
// SINGLE held lock (atomic read-modify-write) and persists the result. All-zero
// after mutation drops the entry. An unknown collection is an error. This is the
// race-safe way to change one field without clobbering a concurrent writer's
// change to another (e.g. the PATCH route changing memory_size while the
// background auto-updater sets last_memory_update).
func UpdateCollectionProfile(name string, mutate func(p *CollectionProfileData)) error {
	name = strings.TrimSpace(name)
	collectionsMu.Lock()
	defer collectionsMu.Unlock()
	f, err := loadFileLocked()
	if err != nil {
		return err
	}
	i := indexOfFold(f.Collections, name)
	if i < 0 {
		return fmt.Errorf("collection %q not found", name)
	}
	canon := f.Collections[i]
	cur := f.Profiles[canon]
	d := CollectionProfileData{
		Squad: cur.Squad, Cwd: cur.Cwd, MemorySize: cur.MemorySize,
		AutoUpdate: cur.AutoUpdate, LastMemoryUpdate: cur.LastMemoryUpdate,
		Role: cur.Role, Engine: cur.Engine, DependsOn: cloneStrings(cur.DependsOn),
	}
	mutate(&d)
	p := collectionProfile{
		Squad:            strings.TrimSpace(d.Squad),
		Cwd:              strings.TrimSpace(d.Cwd),
		MemorySize:       strings.TrimSpace(d.MemorySize),
		AutoUpdate:       d.AutoUpdate,
		LastMemoryUpdate: d.LastMemoryUpdate,
		Role:             strings.TrimSpace(d.Role),
		Engine:           strings.TrimSpace(d.Engine),
		DependsOn:        cleanStrings(d.DependsOn),
	}
	if p.isEmpty() {
		if f.Profiles != nil {
			delete(f.Profiles, canon)
		}
	} else {
		if f.Profiles == nil {
			f.Profiles = map[string]collectionProfile{}
		}
		f.Profiles[canon] = p
	}
	return saveFileLocked(f)
}

// SetCollectionProfileData writes a collection's full scalar bag (all zero ⇒ the
// profile entry is dropped). An unknown collection is an error.
func SetCollectionProfileData(name string, d CollectionProfileData) error {
	name = strings.TrimSpace(name)
	collectionsMu.Lock()
	defer collectionsMu.Unlock()
	f, err := loadFileLocked()
	if err != nil {
		return err
	}
	i := indexOfFold(f.Collections, name)
	if i < 0 {
		return fmt.Errorf("collection %q not found", name)
	}
	canon := f.Collections[i]
	p := collectionProfile{
		Squad:            strings.TrimSpace(d.Squad),
		Cwd:              strings.TrimSpace(d.Cwd),
		MemorySize:       strings.TrimSpace(d.MemorySize),
		AutoUpdate:       d.AutoUpdate,
		LastMemoryUpdate: d.LastMemoryUpdate,
		Role:             strings.TrimSpace(d.Role),
		Engine:           strings.TrimSpace(d.Engine),
		DependsOn:        cleanStrings(d.DependsOn),
	}
	if p.isEmpty() {
		if f.Profiles != nil {
			delete(f.Profiles, canon)
		}
	} else {
		if f.Profiles == nil {
			f.Profiles = map[string]collectionProfile{}
		}
		f.Profiles[canon] = p
	}
	return saveFileLocked(f)
}

// CollectionProfile returns the stored (squad, cwd) defaults. Kept for callers
// that only need the seed scalars.
func CollectionProfile(name string) (squad, cwd string) {
	p := CollectionProfileFull(name)
	return p.Squad, p.Cwd
}

// SetCollectionProfile sets squad/cwd while PRESERVING memory_size/auto_update/
// last_memory_update (a squad/cwd-only PATCH must not clobber the memory fields).
func SetCollectionProfile(name, squad, cwd string) error {
	return UpdateCollectionProfile(name, func(p *CollectionProfileData) {
		p.Squad = squad
		p.Cwd = cwd
	})
}

// SetCollectionMemoryUpdate updates only the last-automatic-memory-commit
// timestamp (0 clears it), preserving the other scalars.
func SetCollectionMemoryUpdate(name string, ts int64) error {
	return UpdateCollectionProfile(name, func(p *CollectionProfileData) {
		p.LastMemoryUpdate = ts
	})
}

// ValidMemorySize reports whether s is an accepted memory-size token.
func ValidMemorySize(s string) bool {
	switch strings.TrimSpace(s) {
	case "", "small", "medium", "large":
		return true
	}
	return false
}

// MaxCollectionColorLen bounds a colour token so it stays a small palette key.
const MaxCollectionColorLen = 24

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

// loadFileLocked reads collections.json into its full shape (names + colours). A
// missing/empty file yields a zero-value struct. Must be called with
// collectionsMu held.
func loadFileLocked() (collectionsFile, error) {
	data, err := os.ReadFile(CollectionsPath())
	if err != nil {
		if os.IsNotExist(err) {
			return collectionsFile{}, nil
		}
		return collectionsFile{}, err
	}
	if len(data) == 0 {
		return collectionsFile{}, nil
	}
	var f collectionsFile
	if err := json.Unmarshal(data, &f); err != nil {
		return collectionsFile{}, err
	}
	return f, nil
}

// loadCollectionsLocked reads just the ordered name list. Must be called with
// collectionsMu held.
func loadCollectionsLocked() ([]string, error) {
	f, err := loadFileLocked()
	if err != nil {
		return nil, err
	}
	return f.Collections, nil
}

// pruneColorsLocked drops colour entries whose collection no longer exists so a
// deleted/renamed collection never leaves an orphaned colour behind.
func pruneColorsLocked(f *collectionsFile) {
	if len(f.Colors) == 0 {
		return
	}
	for key := range f.Colors {
		if indexOfFold(f.Collections, key) < 0 {
			delete(f.Colors, key)
		}
	}
	if len(f.Colors) == 0 {
		f.Colors = nil
	}
}

// pruneProfilesLocked drops profile entries whose collection no longer exists,
// mirroring pruneColorsLocked so a deleted/renamed collection leaves no orphaned
// profile behind.
func pruneProfilesLocked(f *collectionsFile) {
	if len(f.Profiles) == 0 {
		return
	}
	for key := range f.Profiles {
		if indexOfFold(f.Collections, key) < 0 {
			delete(f.Profiles, key)
		}
	}
	if len(f.Profiles) == 0 {
		f.Profiles = nil
	}
}

// saveFileLocked writes the full collections file (names + colours) atomically
// (temp file + rename, like SaveConversationFile). Must be called with
// collectionsMu held.
func saveFileLocked(f collectionsFile) error {
	pruneColorsLocked(&f)
	pruneProfilesLocked(&f)
	dir := paths.ConfigWriteDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(f, "", "  ")
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
	f, err := loadFileLocked()
	if err != nil {
		return nil, false, err
	}
	if indexOfFold(f.Collections, name) >= 0 {
		return f.Collections, false, nil // already present — idempotent
	}
	f.Collections = append(f.Collections, name)
	if err := saveFileLocked(f); err != nil {
		return nil, false, err
	}
	return f.Collections, true, nil
}

// RemoveCollection drops name from the list (case-insensitive), along with any
// colour recorded for it. Returns the updated list and whether an entry was
// removed. Cascading member sessions back to General is the caller's
// responsibility (it owns the session registry).
func RemoveCollection(name string) ([]string, bool, error) {
	collectionsMu.Lock()
	defer collectionsMu.Unlock()
	f, err := loadFileLocked()
	if err != nil {
		return nil, false, err
	}
	i := indexOfFold(f.Collections, name)
	if i < 0 {
		return f.Collections, false, nil
	}
	canon := f.Collections[i]
	if f.Colors != nil {
		delete(f.Colors, canon)
	}
	if f.Profiles != nil {
		delete(f.Profiles, canon)
	}
	f.Collections = append(f.Collections[:i:i], f.Collections[i+1:]...)
	if err := saveFileLocked(f); err != nil {
		return nil, false, err
	}
	// Best-effort: drop the collection's prose (instructions/memory) too. A
	// failure here leaves an orphaned dir but never blocks the delete.
	_ = collectionctx.RemoveDir(canon)
	return f.Collections, true, nil
}

// RenameCollection replaces old with newName in the list, preserving its
// position (and migrating any colour to the new name). Returns the updated list
// and whether the rename happened. It errors on an invalid newName or when
// newName collides with a different existing collection. Cascading member
// sessions to the new name is the caller's responsibility.
func RenameCollection(old, newName string) ([]string, bool, error) {
	newName = strings.TrimSpace(newName)
	if !ValidCollectionName(newName) {
		return nil, false, fmt.Errorf("invalid collection name %q", newName)
	}
	collectionsMu.Lock()
	defer collectionsMu.Unlock()
	f, err := loadFileLocked()
	if err != nil {
		return nil, false, err
	}
	names := f.Collections
	i := indexOfFold(names, old)
	if i < 0 {
		return names, false, nil
	}
	// A no-op rename to the same name (possibly different case) just updates the
	// stored casing. A collision with a *different* existing entry is rejected.
	if j := indexOfFold(names, newName); j >= 0 && j != i {
		return names, false, fmt.Errorf("collection %q already exists", newName)
	}
	oldCanon := names[i]
	renamed := !strings.EqualFold(oldCanon, newName)
	if f.Colors != nil && renamed {
		if c, ok := f.Colors[oldCanon]; ok {
			delete(f.Colors, oldCanon)
			f.Colors[newName] = c
		}
	}
	if f.Profiles != nil && renamed {
		if p, ok := f.Profiles[oldCanon]; ok {
			delete(f.Profiles, oldCanon)
			f.Profiles[newName] = p
		}
	}
	names[i] = newName
	if err := saveFileLocked(f); err != nil {
		return nil, false, err
	}
	// Best-effort: migrate the prose dir so instructions/memory follow the new
	// name. A case-only change resolves to the same dir (no-op inside RenameDir).
	if renamed {
		_ = collectionctx.RenameDir(oldCanon, newName)
	}
	return names, true, nil
}

// ValidCollectionColor reports whether color is an acceptable palette token: the
// empty string (meaning "no colour / default"), or a short slug of lowercase
// letters, digits and hyphens. The web UI owns the actual palette; the server
// only persists the token, so this is a defensive shape check, not a whitelist.
func ValidCollectionColor(color string) bool {
	color = strings.TrimSpace(color)
	if color == "" {
		return true
	}
	if len(color) > MaxCollectionColorLen {
		return false
	}
	for _, r := range color {
		if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-') {
			return false
		}
	}
	return true
}

// CollectionColors returns the name→colour map (canonical stored names). A
// missing file or no recorded colours yields an empty map.
func CollectionColors() (map[string]string, error) {
	collectionsMu.Lock()
	defer collectionsMu.Unlock()
	f, err := loadFileLocked()
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(f.Colors))
	for k, v := range f.Colors {
		out[k] = v
	}
	return out, nil
}

// SetCollectionColor sets (or, with an empty color, clears) the colour of an
// existing collection. The colour is keyed by the collection's canonical stored
// name. An unknown collection is an error; an invalid colour token is an error.
func SetCollectionColor(name, color string) error {
	name = strings.TrimSpace(name)
	color = strings.TrimSpace(color)
	if !ValidCollectionColor(color) {
		return fmt.Errorf("invalid collection colour %q", color)
	}
	collectionsMu.Lock()
	defer collectionsMu.Unlock()
	f, err := loadFileLocked()
	if err != nil {
		return err
	}
	i := indexOfFold(f.Collections, name)
	if i < 0 {
		return fmt.Errorf("collection %q not found", name)
	}
	canon := f.Collections[i]
	if color == "" {
		if f.Colors != nil {
			delete(f.Colors, canon)
		}
	} else {
		if f.Colors == nil {
			f.Colors = map[string]string{}
		}
		f.Colors[canon] = color
	}
	return saveFileLocked(f)
}

// cloneStrings returns a copy so callers can't mutate stored slices.
func cloneStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

// cleanStrings trims each entry and drops blanks (keeps order, dedups).
func cleanStrings(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

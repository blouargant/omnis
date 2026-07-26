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

// UngroupedFleet is the name of the virtual bucket for role:"project" collections
// that carry no fleet tag (or a tag naming a fleet that no longer exists). Like
// GeneralCollection it is never stored in fleets.json and is reserved as a name.
const UngroupedFleet = "Ungrouped"

// fleetsMu serialises the read-modify-write of fleets.json. It is a DIFFERENT
// mutex from collectionsMu: cross-file operations (RemoveFleet/RenameFleet/
// AssignProject) call the public collections.go functions, which take
// collectionsMu, so no function may ever hold both locks at once.
var fleetsMu sync.Mutex

// fleetMeta is the on-disk per-fleet metadata. Colour is a palette token resolved
// theme-side (no hex); DefaultEngine seeds a newly-assigned project's engine.
type fleetMeta struct {
	Color         string `json:"color,omitempty"`
	Description   string `json:"description,omitempty"`
	DefaultEngine string `json:"default_engine,omitempty"`
}

// fleetsFile is the on-disk shape of fleets.json: an ordered list of fleet names
// plus an optional per-fleet metadata map (keyed by the canonical stored name).
// Membership is NOT stored here — it lives on each collection's `fleet` tag.
type fleetsFile struct {
	Fleets []string             `json:"fleets"`
	Meta   map[string]fleetMeta `json:"meta,omitempty"`
}

// FleetMetaData is the exported metadata bag for one fleet.
type FleetMetaData struct {
	Color         string
	Description   string
	DefaultEngine string
}

// FleetsPath returns the on-disk path for fleets.json, resolved at each call so
// tests can redirect via t.Setenv("OMNIS_HOME", ...).
func FleetsPath() string {
	return filepath.Join(paths.ConfigWriteDir(), "fleets.json")
}

// ValidFleetName reports whether name is an acceptable fleet label: the same
// rules as ValidCollectionName, and additionally not "General" or "Ungrouped".
func ValidFleetName(name string) bool {
	n := strings.TrimSpace(name)
	if !ValidCollectionName(n) { // non-blank, length, no separators/control, not "General"
		return false
	}
	if strings.EqualFold(n, UngroupedFleet) {
		return false
	}
	return true
}

// ValidDefaultEngine reports whether e is an accepted engine token ("" ⇒ unset).
func ValidDefaultEngine(e string) bool {
	switch strings.TrimSpace(e) {
	case "", "omnis", "claude":
		return true
	}
	return false
}

// loadFleetsLocked reads fleets.json. A missing/empty file yields a zero value.
// Must be called with fleetsMu held.
func loadFleetsLocked() (fleetsFile, error) {
	data, err := os.ReadFile(FleetsPath())
	if err != nil {
		if os.IsNotExist(err) {
			return fleetsFile{}, nil
		}
		return fleetsFile{}, err
	}
	if len(data) == 0 {
		return fleetsFile{}, nil
	}
	var f fleetsFile
	if err := json.Unmarshal(data, &f); err != nil {
		return fleetsFile{}, err
	}
	return f, nil
}

// pruneFleetMetaLocked drops metadata for fleets no longer in the list.
func pruneFleetMetaLocked(f *fleetsFile) {
	if len(f.Meta) == 0 {
		return
	}
	for key := range f.Meta {
		if indexOfFold(f.Fleets, key) < 0 {
			delete(f.Meta, key)
		}
	}
	if len(f.Meta) == 0 {
		f.Meta = nil
	}
}

// saveFleetsLocked writes fleets.json atomically (temp file + rename), mirroring
// saveFileLocked. Must be called with fleetsMu held.
func saveFleetsLocked(f fleetsFile) error {
	pruneFleetMetaLocked(&f)
	dir := paths.ConfigWriteDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "fleets_*.json.tmp")
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
	if err := os.Rename(tmpName, FleetsPath()); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}

// ListFleets returns the ordered fleet names (excluding the virtual Ungrouped). A
// missing file yields an empty slice.
func ListFleets() ([]string, error) {
	fleetsMu.Lock()
	defer fleetsMu.Unlock()
	f, err := loadFleetsLocked()
	if err != nil {
		return nil, err
	}
	return f.Fleets, nil
}

// FleetExists reports whether name is a stored (non-virtual) fleet.
func FleetExists(name string) bool {
	names, err := ListFleets()
	if err != nil {
		return false
	}
	return indexOfFold(names, name) >= 0
}

// AddFleet appends a new fleet if valid and not already present (case-insensitive),
// storing its metadata. Returns the updated list and whether a new entry was added.
func AddFleet(name string, meta FleetMetaData) ([]string, bool, error) {
	name = strings.TrimSpace(name)
	if !ValidFleetName(name) {
		return nil, false, fmt.Errorf("invalid fleet name %q", name)
	}
	if !ValidDefaultEngine(meta.DefaultEngine) {
		return nil, false, fmt.Errorf("invalid default engine %q", meta.DefaultEngine)
	}
	fleetsMu.Lock()
	defer fleetsMu.Unlock()
	f, err := loadFleetsLocked()
	if err != nil {
		return nil, false, err
	}
	if indexOfFold(f.Fleets, name) >= 0 {
		return f.Fleets, false, nil // idempotent
	}
	f.Fleets = append(f.Fleets, name)
	m := fleetMeta{
		Color:         strings.TrimSpace(meta.Color),
		Description:   strings.TrimSpace(meta.Description),
		DefaultEngine: strings.TrimSpace(meta.DefaultEngine),
	}
	if m != (fleetMeta{}) {
		if f.Meta == nil {
			f.Meta = map[string]fleetMeta{}
		}
		f.Meta[name] = m
	}
	if err := saveFleetsLocked(f); err != nil {
		return nil, false, err
	}
	return f.Fleets, true, nil
}

// FleetMetaFor returns the stored metadata for a fleet (zero value if unknown).
func FleetMetaFor(name string) FleetMetaData {
	fleetsMu.Lock()
	defer fleetsMu.Unlock()
	f, err := loadFleetsLocked()
	if err != nil {
		return FleetMetaData{}
	}
	i := indexOfFold(f.Fleets, name)
	if i < 0 {
		return FleetMetaData{}
	}
	m := f.Meta[f.Fleets[i]]
	return FleetMetaData{Color: m.Color, Description: m.Description, DefaultEngine: m.DefaultEngine}
}

// UpdateFleetMeta applies mutate to a fleet's metadata under a single held lock
// (atomic read-modify-write). An unknown fleet is an error.
func UpdateFleetMeta(name string, mutate func(m *FleetMetaData)) error {
	name = strings.TrimSpace(name)
	fleetsMu.Lock()
	defer fleetsMu.Unlock()
	f, err := loadFleetsLocked()
	if err != nil {
		return err
	}
	i := indexOfFold(f.Fleets, name)
	if i < 0 {
		return fmt.Errorf("fleet %q not found", name)
	}
	canon := f.Fleets[i]
	cur := f.Meta[canon]
	d := FleetMetaData{Color: cur.Color, Description: cur.Description, DefaultEngine: cur.DefaultEngine}
	mutate(&d)
	if !ValidDefaultEngine(d.DefaultEngine) {
		return fmt.Errorf("invalid default engine %q", d.DefaultEngine)
	}
	m := fleetMeta{
		Color:         strings.TrimSpace(d.Color),
		Description:   strings.TrimSpace(d.Description),
		DefaultEngine: strings.TrimSpace(d.DefaultEngine),
	}
	if m == (fleetMeta{}) {
		if f.Meta != nil {
			delete(f.Meta, canon)
		}
	} else {
		if f.Meta == nil {
			f.Meta = map[string]fleetMeta{}
		}
		f.Meta[canon] = m
	}
	return saveFleetsLocked(f)
}

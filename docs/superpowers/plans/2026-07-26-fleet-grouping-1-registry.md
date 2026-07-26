# Fleet grouping — Plan 1: fleets.json registry + membership tag

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add the data layer for named fleets — a first-class `fleets.json` metadata registry and a `fleet` membership tag on the project collection profile — entirely inside `internal/sessions`, with no routes, agent, or UI changes yet.

**Architecture:** Mirror the existing `collections.go` registry exactly (one shared JSON file under `$OMNIS_HOME`, one mutex, atomic temp-file-rename write). Membership lives on the collection (`collectionProfile.Fleet`); fleet metadata lives in the new `fleets.json`. Cross-file operations (delete/rename a fleet) compose the existing public functions and never hold two locks at once, and any orphaned tag folds to a virtual **Ungrouped** bucket (self-healing, exactly like an unknown collection folds to General).

**Tech Stack:** Go, standard library only (`encoding/json`, `os`, `sync`), following `internal/sessions/collections.go`.

**Spec:** [docs/superpowers/specs/2026-07-26-fleet-grouping-design.md](../specs/2026-07-26-fleet-grouping-design.md)

## Global Constraints

- **Membership model A:** `fleets.json` holds fleet *metadata* only (name, colour, description, default engine, order); a collection's `fleet` tag is *membership*. A project is in exactly one fleet.
- **`internal/sessions` owns `fleets.json`.** `internal/fleet` must NOT import `sessions` (import cycle) — no change to `internal/fleet` in this plan.
- **Fold-unknown → Ungrouped.** A `role:"project"` collection whose `fleet` is empty *or* names a fleet not in `fleets.json` belongs to the virtual **Ungrouped** bucket. `"Ungrouped"` is reserved (never a real fleet name), mirroring how `"General"` is reserved for collections.
- **`fleet` is inert unless `role == "project"`.** (Not enforced in storage — a stray tag on a plain collection is simply never surfaced; membership queries filter on `role`.)
- **Deadlock-free:** `fleets.json` gets its own `fleetsMu`. No function holds `fleetsMu` and `collectionsMu` at the same time — cross-file ops call the existing public `collections.go` functions sequentially.
- **No-op contract:** absent `fleets.json` + no `fleet` tags ⇒ `ListFleets()` is empty and every existing collection behaves byte-identically.
- **Name validation:** a fleet name follows `ValidCollectionName`'s rules (non-blank, ≤ `MaxCollectionNameLen`, no path separators/control chars) and additionally rejects `"General"` and `"Ungrouped"` (case-insensitive).
- **Tests redirect state** with `t.Setenv("OMNIS_HOME", t.TempDir())` — both `CollectionsPath()` and `FleetsPath()` resolve `paths.ConfigWriteDir()` at each call.
- Commit after every task. `Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>` trailer on each commit.

---

### Task 1: `fleet` membership tag on the collection profile

**Files:**
- Modify: `internal/sessions/collections.go` (the `collectionProfile` struct, `CollectionProfileData`, `isEmpty`, `CollectionProfileFull`, `UpdateCollectionProfile`, `SetCollectionProfileData`)
- Test: `internal/sessions/collection_fleet_tag_test.go`

**Interfaces:**
- Consumes: the existing `AddCollection`, `UpdateCollectionProfile`, `CollectionProfileFull`, `CollectionProfileData`.
- Produces: `CollectionProfileData.Fleet string` — the membership field read by Task 3 and (later) Plan 2's scoping.

- [ ] **Step 1: Write the failing test**

Create `internal/sessions/collection_fleet_tag_test.go`:

```go
package sessions

import "testing"

func TestCollectionProfileFleetTagRoundTrips(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())

	if _, _, err := AddCollection("api"); err != nil {
		t.Fatalf("AddCollection: %v", err)
	}
	if err := UpdateCollectionProfile("api", func(p *CollectionProfileData) {
		p.Role = "project"
		p.Engine = "omnis"
		p.Fleet = "Payments"
	}); err != nil {
		t.Fatalf("UpdateCollectionProfile: %v", err)
	}
	got := CollectionProfileFull("api")
	if got.Fleet != "Payments" {
		t.Fatalf("Fleet = %q, want %q", got.Fleet, "Payments")
	}

	// A profile carrying ONLY a fleet tag must persist (isEmpty must count Fleet),
	// otherwise saveFileLocked prunes it and the tag is lost.
	if _, _, err := AddCollection("bare"); err != nil {
		t.Fatalf("AddCollection bare: %v", err)
	}
	if err := UpdateCollectionProfile("bare", func(p *CollectionProfileData) {
		p.Fleet = "X"
	}); err != nil {
		t.Fatalf("UpdateCollectionProfile bare: %v", err)
	}
	if got := CollectionProfileFull("bare").Fleet; got != "X" {
		t.Fatalf("bare Fleet = %q, want %q (profile with only Fleet was dropped)", got, "X")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/sessions/ -run TestCollectionProfileFleetTagRoundTrips -v`
Expected: FAIL — build error `unknown field 'Fleet' in struct literal of type CollectionProfileData`.

- [ ] **Step 3: Add the field and thread it through all five sites**

In `internal/sessions/collections.go`, add to the `collectionProfile` struct (after `ClaudeAllowedTools`):

```go
	// Fleet names the fleet this project belongs to (membership). Empty ⇒ the
	// project is Ungrouped. Inert unless Role == "project".
	Fleet string `json:"fleet,omitempty"`
```

Add to `isEmpty` (extend the final `&&` chain):

```go
func (p collectionProfile) isEmpty() bool {
	return p.Squad == "" && p.Cwd == "" && p.MemorySize == "" &&
		!p.AutoUpdate && p.LastMemoryUpdate == 0 &&
		p.Role == "" && p.Engine == "" && len(p.DependsOn) == 0 &&
		len(p.ClaudeAllowedTools) == 0 && p.Fleet == ""
}
```

Add to `CollectionProfileData` (after `ClaudeAllowedTools`):

```go
	Fleet              string
```

In `CollectionProfileFull`, add `Fleet: p.Fleet,` to the returned struct literal:

```go
	return CollectionProfileData{
		Squad: p.Squad, Cwd: p.Cwd, MemorySize: p.MemorySize,
		AutoUpdate: p.AutoUpdate, LastMemoryUpdate: p.LastMemoryUpdate,
		Role: p.Role, Engine: p.Engine, DependsOn: cloneStrings(p.DependsOn),
		ClaudeAllowedTools: cloneStrings(p.ClaudeAllowedTools),
		Fleet:              p.Fleet,
	}
```

In `UpdateCollectionProfile`, add `Fleet: cur.Fleet,` to the `d := CollectionProfileData{...}` literal, and `Fleet: strings.TrimSpace(d.Fleet),` to the `p := collectionProfile{...}` literal:

```go
	d := CollectionProfileData{
		Squad: cur.Squad, Cwd: cur.Cwd, MemorySize: cur.MemorySize,
		AutoUpdate: cur.AutoUpdate, LastMemoryUpdate: cur.LastMemoryUpdate,
		Role: cur.Role, Engine: cur.Engine, DependsOn: cloneStrings(cur.DependsOn),
		ClaudeAllowedTools: cloneStrings(cur.ClaudeAllowedTools),
		Fleet:              cur.Fleet,
	}
	mutate(&d)
	p := collectionProfile{
		Squad:              strings.TrimSpace(d.Squad),
		Cwd:                strings.TrimSpace(d.Cwd),
		MemorySize:         strings.TrimSpace(d.MemorySize),
		AutoUpdate:         d.AutoUpdate,
		LastMemoryUpdate:   d.LastMemoryUpdate,
		Role:               strings.TrimSpace(d.Role),
		Engine:             strings.TrimSpace(d.Engine),
		DependsOn:          cleanStrings(d.DependsOn),
		ClaudeAllowedTools: cleanStrings(d.ClaudeAllowedTools),
		Fleet:              strings.TrimSpace(d.Fleet),
	}
```

In `SetCollectionProfileData`, add `Fleet: strings.TrimSpace(d.Fleet),` to its `p := collectionProfile{...}` literal:

```go
	p := collectionProfile{
		Squad:              strings.TrimSpace(d.Squad),
		Cwd:                strings.TrimSpace(d.Cwd),
		MemorySize:         strings.TrimSpace(d.MemorySize),
		AutoUpdate:         d.AutoUpdate,
		LastMemoryUpdate:   d.LastMemoryUpdate,
		Role:               strings.TrimSpace(d.Role),
		Engine:             strings.TrimSpace(d.Engine),
		DependsOn:          cleanStrings(d.DependsOn),
		ClaudeAllowedTools: cleanStrings(d.ClaudeAllowedTools),
		Fleet:              strings.TrimSpace(d.Fleet),
	}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/sessions/ -run TestCollectionProfileFleetTagRoundTrips -v`
Expected: PASS.

- [ ] **Step 5: Run the whole package to confirm no regression**

Run: `go test ./internal/sessions/...`
Expected: PASS (existing `TestCollectionProfileFleetFields` etc. still green).

- [ ] **Step 6: Commit**

```bash
git add internal/sessions/collections.go internal/sessions/collection_fleet_tag_test.go
git commit -m "$(printf 'feat(fleet): add fleet membership tag to collection profile\n\nCo-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>')"
```

---

### Task 2: `fleets.json` registry core (list + metadata)

**Files:**
- Create: `internal/sessions/fleets.go`
- Test: `internal/sessions/fleets_test.go`

**Interfaces:**
- Consumes: `paths.ConfigWriteDir()`, `indexOfFold`, `MaxCollectionNameLen`, `GeneralCollection` (all already in the `sessions` package).
- Produces (used by Task 3 and Plan 2):
  - `const UngroupedFleet = "Ungrouped"`
  - `type FleetMetaData struct { Color, Description, DefaultEngine string }`
  - `func FleetsPath() string`
  - `func ValidFleetName(name string) bool`
  - `func ValidDefaultEngine(e string) bool`
  - `func ListFleets() ([]string, error)`
  - `func AddFleet(name string, meta FleetMetaData) ([]string, bool, error)`
  - `func FleetMetaFor(name string) FleetMetaData`
  - `func UpdateFleetMeta(name string, mutate func(m *FleetMetaData)) error`

- [ ] **Step 1: Write the failing test**

Create `internal/sessions/fleets_test.go`:

```go
package sessions

import "testing"

func TestFleetRegistryCoreRoundTrips(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())

	// Empty to start.
	if got, err := ListFleets(); err != nil || len(got) != 0 {
		t.Fatalf("ListFleets empty: got %v err %v", got, err)
	}

	// Add with metadata.
	names, added, err := AddFleet("Payments", FleetMetaData{Color: "blue", DefaultEngine: "omnis"})
	if err != nil || !added || len(names) != 1 || names[0] != "Payments" {
		t.Fatalf("AddFleet: names=%v added=%v err=%v", names, added, err)
	}
	// Idempotent re-add.
	if _, added, _ := AddFleet("Payments", FleetMetaData{}); added {
		t.Fatalf("AddFleet twice reported added=true")
	}
	// Metadata round-trips.
	if m := FleetMetaFor("Payments"); m.Color != "blue" || m.DefaultEngine != "omnis" {
		t.Fatalf("FleetMetaFor = %+v", m)
	}
	// Partial update preserves the untouched field.
	if err := UpdateFleetMeta("Payments", func(m *FleetMetaData) { m.Description = "billing rails" }); err != nil {
		t.Fatalf("UpdateFleetMeta: %v", err)
	}
	if m := FleetMetaFor("Payments"); m.Description != "billing rails" || m.Color != "blue" {
		t.Fatalf("after partial update: %+v", m)
	}

	// Validation.
	for _, bad := range []string{"", "General", "Ungrouped", "a/b"} {
		if ValidFleetName(bad) {
			t.Fatalf("ValidFleetName(%q) = true, want false", bad)
		}
	}
	if !ValidFleetName("Billing") {
		t.Fatalf("ValidFleetName(Billing) = false")
	}
	if _, _, err := AddFleet("Ungrouped", FleetMetaData{}); err == nil {
		t.Fatalf("AddFleet(Ungrouped) should error")
	}
	if !ValidDefaultEngine("") || !ValidDefaultEngine("omnis") || !ValidDefaultEngine("claude") || ValidDefaultEngine("gpt") {
		t.Fatalf("ValidDefaultEngine wrong")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/sessions/ -run TestFleetRegistryCoreRoundTrips -v`
Expected: FAIL — build error `undefined: ListFleets` (etc.).

- [ ] **Step 3: Create `internal/sessions/fleets.go`**

```go
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
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/sessions/ -run TestFleetRegistryCoreRoundTrips -v`
Expected: PASS.

- [ ] **Step 5: Vet + whole package**

Run: `go vet ./internal/sessions/ && go test ./internal/sessions/...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/sessions/fleets.go internal/sessions/fleets_test.go
git commit -m "$(printf 'feat(fleet): add fleets.json registry core (list + metadata)\n\nCo-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>')"
```

---

### Task 3: fleet membership operations (members, assign, unassign, rename, remove)

**Files:**
- Modify: `internal/sessions/fleets.go` (append the membership functions)
- Test: `internal/sessions/fleets_membership_test.go`

**Interfaces:**
- Consumes: Task 1's `CollectionProfileData.Fleet`; Task 2's `ListFleets`/`FleetExists`/`FleetMetaFor`/`loadFleetsLocked`/`saveFleetsLocked`/`fleetsMu`; the existing `ListCollections`, `CollectionProfileFull`, `UpdateCollectionProfile`.
- Produces (used by Plan 2's routes):
  - `func FleetMembers(fleet string) []string`
  - `func AssignProject(fleet, collection string) error`
  - `func UnassignProject(collection string) error`
  - `func RenameFleet(old, newName string) ([]string, bool, error)`
  - `func RemoveFleet(name string) ([]string, bool, error)`

- [ ] **Step 1: Write the failing test**

Create `internal/sessions/fleets_membership_test.go`:

```go
package sessions

import (
	"sort"
	"testing"
)

func mustAddCollection(t *testing.T, name string) {
	t.Helper()
	if _, _, err := AddCollection(name); err != nil {
		t.Fatalf("AddCollection %q: %v", name, err)
	}
}

func TestFleetMembershipLifecycle(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())

	mustAddCollection(t, "api")
	mustAddCollection(t, "gateway")
	mustAddCollection(t, "legacy") // stays a project but Ungrouped
	if _, _, err := AddFleet("Payments", FleetMetaData{DefaultEngine: "claude"}); err != nil {
		t.Fatalf("AddFleet: %v", err)
	}

	// Assign two projects; the third becomes a project with no fleet.
	if err := AssignProject("Payments", "api"); err != nil {
		t.Fatalf("AssignProject api: %v", err)
	}
	if err := AssignProject("Payments", "gateway"); err != nil {
		t.Fatalf("AssignProject gateway: %v", err)
	}
	if err := UpdateCollectionProfile("legacy", func(p *CollectionProfileData) {
		p.Role = "project"
		p.Engine = "omnis"
	}); err != nil {
		t.Fatalf("make legacy a project: %v", err)
	}

	// Assign seeds role=project and the fleet's default engine.
	ap := CollectionProfileFull("api")
	if ap.Role != "project" || ap.Fleet != "Payments" || ap.Engine != "claude" {
		t.Fatalf("assigned api profile = %+v (want role=project fleet=Payments engine=claude)", ap)
	}

	// Members are the tagged projects; Ungrouped catches the untagged project.
	if got := sortedCopy(FleetMembers("Payments")); !equalSlices(got, []string{"api", "gateway"}) {
		t.Fatalf("FleetMembers(Payments) = %v", got)
	}
	if got := FleetMembers(UngroupedFleet); !equalSlices(got, []string{"legacy"}) {
		t.Fatalf("FleetMembers(Ungrouped) = %v", got)
	}

	// Unassign returns a project to Ungrouped.
	if err := UnassignProject("gateway"); err != nil {
		t.Fatalf("UnassignProject: %v", err)
	}
	if got := FleetMembers("Payments"); !equalSlices(got, []string{"api"}) {
		t.Fatalf("after unassign, members = %v", got)
	}
	if got := sortedCopy(FleetMembers(UngroupedFleet)); !equalSlices(got, []string{"gateway", "legacy"}) {
		t.Fatalf("after unassign, ungrouped = %v", got)
	}

	// Rename migrates metadata AND every member's tag.
	if _, _, err := RenameFleet("Payments", "Billing"); err != nil {
		t.Fatalf("RenameFleet: %v", err)
	}
	if FleetExists("Payments") || !FleetExists("Billing") {
		t.Fatalf("rename didn't move the fleet object")
	}
	if CollectionProfileFull("api").Fleet != "Billing" {
		t.Fatalf("rename didn't rewrite member tag: %q", CollectionProfileFull("api").Fleet)
	}
	if FleetMetaFor("Billing").DefaultEngine != "claude" {
		t.Fatalf("rename didn't migrate metadata")
	}

	// Remove clears member tags (→ Ungrouped) and drops the fleet object.
	if _, _, err := RemoveFleet("Billing"); err != nil {
		t.Fatalf("RemoveFleet: %v", err)
	}
	if FleetExists("Billing") {
		t.Fatalf("fleet still present after remove")
	}
	if CollectionProfileFull("api").Fleet != "" {
		t.Fatalf("remove left an orphaned member tag: %q", CollectionProfileFull("api").Fleet)
	}
	if CollectionProfileFull("api").Role != "project" {
		t.Fatalf("remove must NOT strip role:project (still a project, just Ungrouped)")
	}
}

func sortedCopy(in []string) []string { out := append([]string(nil), in...); sort.Strings(out); return out }
func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/sessions/ -run TestFleetMembershipLifecycle -v`
Expected: FAIL — build error `undefined: AssignProject` (etc.).

- [ ] **Step 3: Append the membership functions to `internal/sessions/fleets.go`**

```go
// FleetMembers returns the collection names belonging to a fleet. For a real
// fleet, that is every role:"project" collection whose `fleet` tag matches. For
// UngroupedFleet, it is every project whose tag is empty OR names a fleet that no
// longer exists (fold-unknown → Ungrouped). Order follows ListCollections.
//
// It reads collections (collectionsMu) and the fleet list (fleetsMu) via the
// public functions, sequentially — it never holds both locks at once.
func FleetMembers(fleet string) []string {
	cols, err := ListCollections()
	if err != nil {
		return nil
	}
	known, _ := ListFleets()
	ungrouped := strings.EqualFold(strings.TrimSpace(fleet), UngroupedFleet)
	var out []string
	for _, c := range cols {
		p := CollectionProfileFull(c)
		if p.Role != "project" {
			continue
		}
		tag := strings.TrimSpace(p.Fleet)
		effective := tag == "" || indexOfFold(known, tag) < 0 // ⇒ Ungrouped
		if ungrouped {
			if effective {
				out = append(out, c)
			}
			continue
		}
		if !effective && strings.EqualFold(tag, fleet) {
			out = append(out, c)
		}
	}
	return out
}

// AssignProject files a collection under a fleet: it validates the fleet exists,
// marks the collection role:"project" (if it isn't already), seeds its engine from
// the fleet's default (falling back to "omnis") when the collection has none, and
// writes the `fleet` tag. An unknown fleet or collection is an error.
func AssignProject(fleet, collection string) error {
	fleet = strings.TrimSpace(fleet)
	if !FleetExists(fleet) {
		return fmt.Errorf("fleet %q not found", fleet)
	}
	def := strings.TrimSpace(FleetMetaFor(fleet).DefaultEngine)
	return UpdateCollectionProfile(collection, func(p *CollectionProfileData) {
		p.Role = "project"
		if strings.TrimSpace(p.Engine) == "" {
			if def != "" {
				p.Engine = def
			} else {
				p.Engine = "omnis"
			}
		}
		p.Fleet = fleet
	})
}

// UnassignProject clears a collection's fleet tag, returning it to Ungrouped. It
// leaves role:"project" intact (it is still a project, just not in a named fleet).
// An unknown collection is an error.
func UnassignProject(collection string) error {
	return UpdateCollectionProfile(collection, func(p *CollectionProfileData) {
		p.Fleet = ""
	})
}

// RenameFleet renames a fleet, migrating its metadata AND rewriting every member's
// `fleet` tag. Errors on an invalid newName or a collision with a different fleet.
// Not atomic across the two files, but self-healing: during the brief window a
// member may fold to Ungrouped, which resolves once the rewrite completes.
func RenameFleet(old, newName string) ([]string, bool, error) {
	newName = strings.TrimSpace(newName)
	if !ValidFleetName(newName) {
		return nil, false, fmt.Errorf("invalid fleet name %q", newName)
	}
	members := FleetMembers(old) // capture BEFORE the object rename

	fleetsMu.Lock()
	f, err := loadFleetsLocked()
	if err != nil {
		fleetsMu.Unlock()
		return nil, false, err
	}
	i := indexOfFold(f.Fleets, old)
	if i < 0 {
		fleetsMu.Unlock()
		return f.Fleets, false, nil
	}
	if j := indexOfFold(f.Fleets, newName); j >= 0 && j != i {
		fleetsMu.Unlock()
		return f.Fleets, false, fmt.Errorf("fleet %q already exists", newName)
	}
	oldCanon := f.Fleets[i]
	renamed := !strings.EqualFold(oldCanon, newName)
	if f.Meta != nil && renamed {
		if m, ok := f.Meta[oldCanon]; ok {
			delete(f.Meta, oldCanon)
			f.Meta[newName] = m
		}
	}
	f.Fleets[i] = newName
	names := f.Fleets
	if err := saveFleetsLocked(f); err != nil {
		fleetsMu.Unlock()
		return nil, false, err
	}
	fleetsMu.Unlock() // release BEFORE touching collections (no two-lock hold)

	if renamed {
		for _, c := range members {
			_ = UpdateCollectionProfile(c, func(p *CollectionProfileData) { p.Fleet = newName })
		}
	}
	return names, true, nil
}

// RemoveFleet deletes a fleet: it first clears every member's `fleet` tag (→
// Ungrouped, leaving role:"project" intact), then drops the fleet object. Returns
// the updated list and whether an entry was removed.
func RemoveFleet(name string) ([]string, bool, error) {
	for _, c := range FleetMembers(name) {
		_ = UnassignProject(c)
	}
	fleetsMu.Lock()
	defer fleetsMu.Unlock()
	f, err := loadFleetsLocked()
	if err != nil {
		return nil, false, err
	}
	i := indexOfFold(f.Fleets, name)
	if i < 0 {
		return f.Fleets, false, nil
	}
	canon := f.Fleets[i]
	if f.Meta != nil {
		delete(f.Meta, canon)
	}
	f.Fleets = append(f.Fleets[:i:i], f.Fleets[i+1:]...)
	if err := saveFleetsLocked(f); err != nil {
		return nil, false, err
	}
	return f.Fleets, true, nil
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/sessions/ -run TestFleetMembershipLifecycle -v`
Expected: PASS.

- [ ] **Step 5: Race + whole package + build**

Run: `go test -race ./internal/sessions/... && go build ./...`
Expected: PASS (no data race; the whole module still builds).

- [ ] **Step 6: Commit**

```bash
git add internal/sessions/fleets.go internal/sessions/fleets_membership_test.go
git commit -m "$(printf 'feat(fleet): fleet membership ops (members, assign, rename, remove)\n\nCo-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>')"
```

---

## Self-Review

**Spec coverage (Plan 1 slice):**
- `fleets.json` metadata registry (name/colour/description/default_engine/order) → Task 2. ✓
- `collectionProfile.Fleet` membership tag + threading → Task 1. ✓
- Cross-file invariants (delete clears member tags; rename migrates meta + tags) → Task 3 (`RemoveFleet`/`RenameFleet`) + test. ✓
- Derived members + fold-unknown→Ungrouped → Task 3 (`FleetMembers`). ✓
- Assign promotes to `role:project` + seeds engine from default → Task 3 (`AssignProject`) + test. ✓
- Deadlock-free (separate `fleetsMu`, no two-lock hold) → enforced by construction in Task 3; `-race` in Step 5. ✓
- Reserved `Ungrouped`/`General` names → Task 2 (`ValidFleetName`) + test. ✓
- No-op contract (absent file ⇒ empty) → Task 2 test (`ListFleets` empty to start). ✓

Deferred to later plans (correctly out of this plan): session `fleet` field, `internal/fleet` scoping, `/api/fleets` routes, `fleets_changed`, all web UI (Plan 2 + Plan 3).

**Placeholder scan:** none — every step has complete code + exact commands.

**Type consistency:** `FleetMetaData{Color,Description,DefaultEngine}`, `CollectionProfileData.Fleet`, and the function signatures in the Interfaces blocks match their definitions and uses across Tasks 1–3.

---

## Execution Handoff

Plan 1 is the data layer only. Plans 2 (session scoping + `/api/fleets` routes) and 3 (web UI) follow after this one is green, so each builds on reviewed code.

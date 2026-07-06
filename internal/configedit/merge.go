package configedit

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"

	"github.com/blouargant/omnis/internal/paths"
)

// This file implements the layered deep-merge that lets a per-user overlay in
// $OMNIS_HOME evolve with package updates instead of shadowing them wholesale.
//
// Every config file is merged across all layers of the search chain (system →
// user → local, low→high precedence) rather than the old first-existing-file
// wins. The merge is driven by a per-file spec of "collections" (named lists /
// maps whose entries merge by identity) plus generic deep-merge for everything
// else; a sibling "<key>_removed" tombstone in a higher layer drops entries a
// lower layer contributed.
//
// The dual DiffSection computes the minimal overlay (desired − base) so a save
// writes only what the user actually changed. The two satisfy the contract
//
//	MergeSection(name, [base, DiffSection(name, base, desired)]) == desired
//
// which the round-trip tests enforce per section.

// collKind is how a collection's entries are keyed for merge/diff.
type collKind int

const (
	// valueList: a JSON array of scalars or objects merged by whole-value
	// identity (deep-equal union; tombstone removes by deep-equal). Used for
	// the agents name-list and the permission allow/ask/deny tiers (whose
	// entries may be strings OR {regex,tools} objects).
	valueList collKind = iota
	// objList: a JSON array of objects keyed by a field (idField). Matching
	// entries are field-merged (fields in replaceFields replace instead of
	// merge); unmatched overlay entries are appended; tombstone removes by id.
	objList
	// mapColl: a JSON object keyed by name. Entries are field-merged per key;
	// tombstone removes by key.
	mapColl
)

// collectionSpec describes one merge-aware collection inside a config file.
type collectionSpec struct {
	path          []string // JSON path to the collection (e.g. ["permissions","allow"])
	kind          collKind
	idField       string          // objList only
	replaceFields map[string]bool // objList only: fields whose overlay value replaces rather than field-merges
}

// removedPath returns the tombstone key path for a collection: the sibling
// "<lastKey>_removed" alongside the collection.
func (c collectionSpec) removedPath() []string {
	if len(c.path) == 0 {
		return nil
	}
	out := append([]string(nil), c.path...)
	out[len(out)-1] = out[len(out)-1] + "_removed"
	return out
}

// sectionSpecs maps a config FILENAME to its collection specs. Files absent
// here (or unknown) fall back to a pure generic deep-merge, which is already
// correct for scalars and nested objects.
var sectionSpecs = map[string][]collectionSpec{
	"agents.json": {
		{path: []string{"agents"}, kind: valueList},
		{path: []string{"squads"}, kind: objList, idField: "name", replaceFields: map[string]bool{"members": true}},
	},
	"models.json": {
		{path: []string{"providers"}, kind: mapColl},
		{path: []string{"models"}, kind: mapColl},
	},
	"mcp_config.json": {
		{path: []string{"servers"}, kind: mapColl},
		{path: []string{"inputs"}, kind: objList, idField: "id"},
	},
	"a2a_config.json": {
		{path: []string{"agents"}, kind: mapColl},
		{path: []string{"inputs"}, kind: objList, idField: "id"},
	},
	"permissions.json": {
		{path: []string{"permissions", "allow"}, kind: valueList},
		{path: []string{"permissions", "ask"}, kind: valueList},
		{path: []string{"permissions", "deny"}, kind: valueList},
	},
	// hooks.json is handled specially (event→array-of-matchers concat); see
	// mergeHooks / diffHooks.
}

// ── Public API ──────────────────────────────────────────────────────────────

// LoadMergedSection enumerates every existing layer of a config filename
// (paths.ConfigLayers, low→high), parses each as a JSON object, and deep-merges
// them into one effective object. Returns the merged object (nil when the file
// exists in no layer), the contributing layer paths (low→high), and any parse
// error. A file that parses to a non-object is skipped with an error.
func LoadMergedSection(filename string) (map[string]any, []string, error) {
	layerPaths := paths.ConfigLayers(filename)
	parsed := make([]map[string]any, 0, len(layerPaths))
	used := make([]string, 0, len(layerPaths))
	for _, p := range layerPaths {
		data, err := os.ReadFile(p)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, nil, fmt.Errorf("read %q: %w", p, err)
		}
		if len(data) == 0 {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			return nil, nil, fmt.Errorf("%q is not valid JSON object: %w", p, err)
		}
		parsed = append(parsed, m)
		used = append(used, p)
	}
	if len(parsed) == 0 {
		return nil, nil, nil
	}
	return MergeSection(filename, parsed), used, nil
}

// MergedBytes is LoadMergedSection re-serialised as pretty JSON, or nil when the
// file exists in no layer. Convenience for consumers that parse from bytes into
// their own typed structs (agents/models/mcp/a2a loaders).
func MergedBytes(filename string) ([]byte, error) {
	merged, layers, err := LoadMergedSection(filename)
	if err != nil {
		return nil, err
	}
	if merged == nil || len(layers) == 0 {
		return nil, nil
	}
	out, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// MergeSection folds parsed layer objects (ordered LOW → HIGH precedence) into
// one effective object per the file's collection spec. Tombstone keys are
// applied per layer and never appear in the result.
func MergeSection(filename string, layers []map[string]any) map[string]any {
	var acc map[string]any
	for _, layer := range layers {
		acc = mergeLayer(filename, acc, layer)
	}
	if acc == nil {
		acc = map[string]any{}
	}
	// Strip every tombstone directive from the effective object — they steer the
	// merge, they are not config. Nested tombstones (permissions.allow_removed)
	// may have been copied in by the generic merge; delPath removes them wherever
	// they ended up.
	for _, sp := range sectionSpecs[filename] {
		delPath(acc, sp.removedPath())
	}
	if filename == "hooks.json" {
		delete(acc, "hooks_removed")
	}
	return acc
}

// ── Fold one layer ──────────────────────────────────────────────────────────

func mergeLayer(filename string, acc, over map[string]any) map[string]any {
	if acc == nil {
		acc = map[string]any{}
	}
	if over == nil {
		return acc
	}
	specs := sectionSpecs[filename]

	// Snapshot the base collection + tombstone values BEFORE the generic merge
	// overwrites them, so the collection-aware merge below sees the real base.
	type snap struct {
		base    any
		over    any
		removed any
	}
	snaps := make([]snap, len(specs))
	skip := map[string]bool{} // top-level keys the generic merge must not touch
	for i, sp := range specs {
		snaps[i] = snap{
			base:    getPath(acc, sp.path),
			over:    getPath(over, sp.path),
			removed: getPath(over, sp.removedPath()),
		}
		// Only exclude a collection (and its tombstone) from the generic merge
		// when it is a TOP-LEVEL key we replace wholesale. A nested collection
		// (e.g. permissions.allow) must let the generic merge run so its
		// siblings — permissions.defaultMode — still merge; the setPath below
		// then overwrites the sub-value with the correct collection merge, and
		// the nested tombstone is stripped at the end of MergeSection.
		if len(sp.path) == 1 {
			skip[sp.path[0]] = true
			skip[sp.removedPath()[0]] = true
		}
	}
	if filename == "hooks.json" {
		skip["hooks"] = true
		skip["hooks_removed"] = true
	}

	// Generic deep-merge for every non-collection, non-tombstone top-level key.
	for k, v := range over {
		if skip[k] {
			continue
		}
		acc[k] = deepMergeValue(acc[k], v)
	}

	// Collection-aware merge + per-layer tombstone application.
	for i, sp := range specs {
		merged := mergeCollection(sp, snaps[i].base, snaps[i].over)
		merged = removeFromCollection(sp, merged, snaps[i].removed)
		if merged == nil {
			// Nothing in either layer: leave acc as-is (may hold a base value
			// under a nested path already copied by the generic merge above —
			// but collection paths are skipped there, so ensure base survives).
			if b := snaps[i].base; b != nil {
				setPath(acc, sp.path, b)
			}
			continue
		}
		setPath(acc, sp.path, merged)
	}

	if filename == "hooks.json" {
		mergeHooks(acc, over)
	}
	return acc
}

// mergeCollection merges base+over for one collection per its kind. Either side
// may be nil.
func mergeCollection(sp collectionSpec, base, over any) any {
	switch sp.kind {
	case valueList:
		bl, _ := base.([]any)
		ol, _ := over.([]any)
		if bl == nil && ol == nil {
			return nil
		}
		return valueListUnion(bl, ol)
	case objList:
		bl, _ := base.([]any)
		ol, _ := over.([]any)
		if bl == nil && ol == nil {
			return nil
		}
		return objListMerge(bl, ol, sp.idField, sp.replaceFields)
	case mapColl:
		bm, _ := base.(map[string]any)
		om, _ := over.(map[string]any)
		if bm == nil && om == nil {
			return nil
		}
		res := deepMergeValue(bm, om)
		return res
	}
	return nil
}

func removeFromCollection(sp collectionSpec, coll, removed any) any {
	names := toStringSlice(removed)
	if len(names) == 0 || coll == nil {
		return coll
	}
	switch sp.kind {
	case valueList:
		return valueListRemove(coll.([]any), removedValues(removed))
	case objList:
		return objListRemove(coll.([]any), sp.idField, names)
	case mapColl:
		m, _ := coll.(map[string]any)
		for _, n := range names {
			delete(m, n)
		}
		return m
	}
	return coll
}

// mergeHooks concats each event's matcher array (dedup by deep-equal) and
// honours a top-level hooks_removed = [eventName,...] tombstone. hooks.json
// shape: { "hooks": { "PreToolUse": [ {matcher, hooks:[...]}, ... ], ... } }.
func mergeHooks(acc, over map[string]any) {
	ob, _ := over["hooks"].(map[string]any)
	if ob != nil {
		ab, _ := acc["hooks"].(map[string]any)
		if ab == nil {
			ab = map[string]any{}
		}
		for event, ov := range ob {
			oa, _ := ov.([]any)
			ba, _ := ab[event].([]any)
			ab[event] = valueListUnion(ba, oa)
		}
		acc["hooks"] = ab
	}
	if removed := toStringSlice(over["hooks_removed"]); len(removed) > 0 {
		if ab, ok := acc["hooks"].(map[string]any); ok {
			for _, ev := range removed {
				delete(ab, ev)
			}
		}
	}
}

// ── Generic value merge ─────────────────────────────────────────────────────

// deepMergeValue merges over onto base: two objects merge key-by-key
// recursively; anything else (scalars, arrays) is replaced by over. base/over
// may be nil.
func deepMergeValue(base, over any) any {
	if over == nil {
		return base
	}
	bm, bok := base.(map[string]any)
	om, ook := over.(map[string]any)
	if bok && ook {
		out := make(map[string]any, len(bm)+len(om))
		for k, v := range bm {
			out[k] = v
		}
		for k, v := range om {
			out[k] = deepMergeValue(out[k], v)
		}
		return out
	}
	return over
}

func valueListUnion(base, over []any) []any {
	out := make([]any, 0, len(base)+len(over))
	out = append(out, base...)
	for _, v := range over {
		if !containsValue(out, v) {
			out = append(out, v)
		}
	}
	return out
}

func valueListRemove(list []any, removed []any) []any {
	if len(removed) == 0 {
		return list
	}
	out := make([]any, 0, len(list))
	for _, v := range list {
		if !containsValue(removed, v) {
			out = append(out, v)
		}
	}
	return out
}

func objListMerge(base, over []any, idField string, replace map[string]bool) []any {
	out := make([]any, 0, len(base)+len(over))
	idx := map[string]int{} // id → position in out
	add := func(e any) {
		m, ok := e.(map[string]any)
		if !ok {
			out = append(out, e)
			return
		}
		id, _ := m[idField].(string)
		if id == "" {
			out = append(out, e)
			return
		}
		if pos, seen := idx[id]; seen {
			out[pos] = mergeObjEntry(out[pos], m, replace)
			return
		}
		idx[id] = len(out)
		out = append(out, m)
	}
	for _, e := range base {
		add(e)
	}
	for _, e := range over {
		add(e)
	}
	return out
}

// mergeObjEntry field-merges an overlay object entry onto a base one. Fields in
// replace are taken wholesale from the overlay when present (e.g. squad
// members); every other field deep-merges (scalars higher-wins).
func mergeObjEntry(base any, over map[string]any, replace map[string]bool) any {
	bm, ok := base.(map[string]any)
	if !ok {
		return over
	}
	out := make(map[string]any, len(bm)+len(over))
	for k, v := range bm {
		out[k] = v
	}
	for k, v := range over {
		if replace[k] {
			out[k] = v
			continue
		}
		out[k] = deepMergeValue(out[k], v)
	}
	return out
}

func objListRemove(list []any, idField string, removedIDs []string) []any {
	rm := map[string]bool{}
	for _, id := range removedIDs {
		rm[id] = true
	}
	out := make([]any, 0, len(list))
	for _, e := range list {
		if m, ok := e.(map[string]any); ok {
			if id, _ := m[idField].(string); id != "" && rm[id] {
				continue
			}
		}
		out = append(out, e)
	}
	return out
}

// ── path + value helpers ────────────────────────────────────────────────────

func getPath(m map[string]any, path []string) any {
	if m == nil || len(path) == 0 {
		return nil
	}
	cur := any(m)
	for _, key := range path {
		mm, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = mm[key]
	}
	return cur
}

func setPath(m map[string]any, path []string, v any) {
	if len(path) == 0 {
		return
	}
	cur := m
	for _, key := range path[:len(path)-1] {
		next, ok := cur[key].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[key] = next
		}
		cur = next
	}
	cur[path[len(path)-1]] = v
}

func delPath(m map[string]any, path []string) {
	if len(path) == 0 {
		return
	}
	cur := m
	for _, key := range path[:len(path)-1] {
		next, ok := cur[key].(map[string]any)
		if !ok {
			return
		}
		cur = next
	}
	delete(cur, path[len(path)-1])
}

func containsValue(list []any, v any) bool {
	for _, e := range list {
		if reflect.DeepEqual(e, v) {
			return true
		}
	}
	return false
}

// toStringSlice coerces a JSON value (expected []any of strings) to []string.
func toStringSlice(v any) []string {
	l, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(l))
	for _, e := range l {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// removedValues returns the raw tombstone entries (used for valueList removal by
// deep-equal, which may target non-string entries).
func removedValues(v any) []any {
	l, _ := v.([]any)
	return l
}

package configedit

import "reflect"

// DiffSection computes the minimal overlay that, layered on top of base,
// reproduces desired: DiffSection satisfies
//
//	MergeSection(name, [base, DiffSection(name, base, desired)]) == desired
//
// (set-level for order-insensitive collections — see below). It is the write
// side of the layered config model: only what the user actually changed is
// persisted, so an untouched field keeps evolving with package updates.
//
// Semantics per collection:
//   - valueList (agents, permission tiers): overlay holds the entries desired
//     adds over base; a "<key>_removed" tombstone holds the entries base has
//     that desired drops. Order within the list is not preserved as an overlay
//     (union appends), so the round-trip is set-level for these.
//   - objList (squads, a2a inputs): a new entry is emitted whole; a changed
//     entry is emitted as a minimal field-diff (idField + changed fields, plus
//     any replaceField that changed in full); "<key>_removed" holds dropped ids.
//   - mapColl (providers, models, servers, a2a agents): each added key is
//     emitted whole, each changed key as a minimal recursive field-diff;
//     "<key>_removed" holds dropped keys.
//   - hooks: per-event added matchers; "hooks_removed" holds dropped events.
//   - everything else: a scalar/object that differs is emitted (minimal for
//     nested objects). Removing a plain scalar a lower layer sets is not
//     expressible as an overlay (set it to a new value instead).
//
// base or desired may be nil (treated as empty). The returned overlay is nil
// when desired equals base.
func DiffSection(filename string, base, desired map[string]any) map[string]any {
	if base == nil {
		base = map[string]any{}
	}
	if desired == nil {
		desired = map[string]any{}
	}
	overlay := map[string]any{}
	specs := sectionSpecs[filename]

	// Which top-level keys are owned by a collection (so the generic pass skips
	// them). Nested-collection parents are NOT skipped — their non-collection
	// siblings (permissions.defaultMode) still need the generic diff.
	collTop := map[string]bool{}
	for _, sp := range specs {
		if len(sp.path) == 1 {
			collTop[sp.path[0]] = true
		}
	}

	// Generic pass: minimal diff of every non-collection-owned key.
	for k, dv := range desired {
		if collTop[k] {
			continue
		}
		if filename == "hooks.json" && k == "hooks" {
			continue
		}
		if ov, changed := diffValue(base[k], dv); changed {
			overlay[k] = ov
		}
	}

	// Collection pass.
	for _, sp := range specs {
		diffCollection(sp, base, desired, overlay)
	}

	if filename == "hooks.json" {
		diffHooks(base, desired, overlay)
	}

	if len(overlay) == 0 {
		return nil
	}
	return overlay
}

func diffCollection(sp collectionSpec, base, desired, overlay map[string]any) {
	bv := getPath(base, sp.path)
	dv := getPath(desired, sp.path)

	switch sp.kind {
	case valueList:
		bl, _ := bv.([]any)
		dl, _ := dv.([]any)
		added := make([]any, 0)
		for _, e := range dl {
			if !containsValue(bl, e) {
				added = append(added, e)
			}
		}
		removed := make([]any, 0)
		for _, e := range bl {
			if !containsValue(dl, e) {
				removed = append(removed, e)
			}
		}
		if len(added) > 0 {
			setPath(overlay, sp.path, added)
		}
		if len(removed) > 0 {
			setPath(overlay, sp.removedPath(), removed)
		}
	case objList:
		bl, _ := bv.([]any)
		dl, _ := dv.([]any)
		baseByID := indexByID(bl, sp.idField)
		var entries []any
		for _, e := range dl {
			m, ok := e.(map[string]any)
			if !ok {
				entries = append(entries, e)
				continue
			}
			id, _ := m[sp.idField].(string)
			bEntry, seen := baseByID[id]
			if !seen {
				entries = append(entries, m) // new: emit whole
				continue
			}
			if reflect.DeepEqual(bEntry, m) {
				continue // unchanged
			}
			entries = append(entries, diffObjEntry(bEntry, m, sp.idField, sp.replaceFields))
		}
		var removedIDs []any
		desiredByID := indexByID(dl, sp.idField)
		for id := range baseByID {
			if _, ok := desiredByID[id]; !ok {
				removedIDs = append(removedIDs, id)
			}
		}
		if len(entries) > 0 {
			setPath(overlay, sp.path, entries)
		}
		if len(removedIDs) > 0 {
			setPath(overlay, sp.removedPath(), removedIDs)
		}
	case mapColl:
		bm, _ := bv.(map[string]any)
		dm, _ := dv.(map[string]any)
		partial := map[string]any{}
		for k, dEntry := range dm {
			bEntry, seen := bm[k]
			if !seen {
				partial[k] = dEntry // new: emit whole
				continue
			}
			if ov, changed := diffValue(bEntry, dEntry); changed {
				partial[k] = ov
			}
		}
		var removed []any
		for k := range bm {
			if _, ok := dm[k]; !ok {
				removed = append(removed, k)
			}
		}
		if len(partial) > 0 {
			setPath(overlay, sp.path, partial)
		}
		if len(removed) > 0 {
			setPath(overlay, sp.removedPath(), removed)
		}
	}
}

// diffObjEntry emits the minimal overlay entry for a changed objList member:
// the id field, every replaceField that changed (in full), and a minimal diff
// of every other changed field.
func diffObjEntry(base any, desired map[string]any, idField string, replace map[string]bool) map[string]any {
	bm, _ := base.(map[string]any)
	out := map[string]any{}
	if id, ok := desired[idField].(string); ok {
		out[idField] = id
	}
	for k, dv := range desired {
		if k == idField {
			continue
		}
		if replace[k] {
			if bm == nil || !reflect.DeepEqual(bm[k], dv) {
				out[k] = dv
			}
			continue
		}
		var bv any
		if bm != nil {
			bv = bm[k]
		}
		if ov, changed := diffValue(bv, dv); changed {
			out[k] = ov
		}
	}
	return out
}

// diffValue returns the minimal overlay for one value and whether it differs
// from base. Objects are diffed recursively (only changed/added keys); anything
// else that differs is returned whole. Field REMOVAL inside an object is not
// representable (deep-merge is additive), so a desired that only drops a key
// reports no change for that key — documented limitation.
func diffValue(base, desired any) (any, bool) {
	if reflect.DeepEqual(base, desired) {
		return nil, false
	}
	bm, bok := base.(map[string]any)
	dm, dok := desired.(map[string]any)
	if bok && dok {
		out := map[string]any{}
		for k, dv := range dm {
			if ov, changed := diffValue(bm[k], dv); changed {
				out[k] = ov
			}
		}
		if len(out) == 0 {
			return nil, false
		}
		return out, true
	}
	return desired, true
}

func diffHooks(base, desired, overlay map[string]any) {
	bh, _ := base["hooks"].(map[string]any)
	dh, _ := desired["hooks"].(map[string]any)
	added := map[string]any{}
	for event, dv := range dh {
		da, _ := dv.([]any)
		ba, _ := bh[event].([]any)
		var evAdd []any
		for _, e := range da {
			if !containsValue(ba, e) {
				evAdd = append(evAdd, e)
			}
		}
		if len(evAdd) > 0 {
			added[event] = evAdd
		}
	}
	var removedEvents []any
	for event := range bh {
		if _, ok := dh[event]; !ok {
			removedEvents = append(removedEvents, event)
		}
	}
	if len(added) > 0 {
		overlay["hooks"] = added
	}
	if len(removedEvents) > 0 {
		overlay["hooks_removed"] = removedEvents
	}
}

func indexByID(list []any, idField string) map[string]any {
	out := map[string]any{}
	for _, e := range list {
		if m, ok := e.(map[string]any); ok {
			if id, _ := m[idField].(string); id != "" {
				out[id] = m
			}
		}
	}
	return out
}

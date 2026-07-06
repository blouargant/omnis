package configedit

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/blouargant/omnis/internal/paths"
)

// ConfigFileNames maps the editor's whitelisted short section names to the
// underlying JSON filenames resolved through the config search chain.
var ConfigFileNames = map[string]string{
	"agent":       "agents.json",
	"models":      "models.json",
	"permissions": "permissions.json",
	"mcp":         "mcp_config.json",
	"a2a":         "a2a_config.json",
	"hooks":       "hooks.json",
}

// FileNameForSection returns the JSON filename backing a whitelisted section
// name, and whether the name is known.
func FileNameForSection(name string) (string, bool) {
	f, ok := ConfigFileNames[name]
	return f, ok
}

// ReadPath returns the highest-precedence read path for a whitelisted section,
// resolved through the 3-layer config search chain. Empty when the file does
// not exist in any layer (a first write will fork it into the user layer).
func ReadPath(name string) (string, bool) {
	filename, ok := ConfigFileNames[name]
	if !ok {
		return "", false
	}
	return paths.FindConfig(filename), true
}

// WritePath returns the write target for a whitelisted section. For "agent" the
// body is consulted so an agents.json that references local-only items lands in
// the local layer; every other section preserves its source layer (forking
// system → user). body may be nil.
func WritePath(name string, body []byte) (string, bool) {
	filename, ok := ConfigFileNames[name]
	if !ok {
		return "", false
	}
	readPath, _ := ReadPath(name)
	var layer string
	if name == "agent" {
		layer = AgentsConfigLayer(readPath, body)
	} else {
		layer = SourceLayer(readPath)
	}
	return filepath.Join(paths.WriteDirForLayer(layer), filename), true
}

// ReadSection reads a whitelisted config section as the MERGED effective view —
// every layer of the search chain deep-merged (system → user → local), not just
// the highest-precedence file — so callers see the full config that actually
// takes effect (including package-shipped items a per-user overlay would
// otherwise shadow). It returns the parsed object (nil when the file exists in
// no layer), the highest-precedence read path (for display/mtime), the layer
// that path lives in, and that file's mtime. A non-existent file is not an error.
func ReadSection(name string) (parsed any, readPath, layer string, mtime time.Time, err error) {
	filename, ok := FileNameForSection(name)
	if !ok {
		return nil, "", "", time.Time{}, fmt.Errorf("unknown config section %q", name)
	}
	readPath, _ = ReadPath(name)
	layer = paths.Layer(readPath)
	merged, _, merr := LoadMergedSection(filename)
	if merr != nil {
		return nil, readPath, layer, time.Time{}, fmt.Errorf("%s: %w", name, merr)
	}
	if merged != nil {
		parsed = merged
	}
	if st, serr := os.Stat(readPath); serr == nil {
		mtime = st.ModTime()
	}
	return parsed, readPath, layer, mtime, nil
}

// WriteSection persists `data` (the full desired effective section) to the
// section's layer-aware write target, writing ONLY the delta against the merge
// of every layer below that target (see OverlayBytes) — so a per-user save stays
// minimal and package updates keep flowing through untouched fields. It returns
// the write path and the layer actually written to.
//
// `data` is expected to be a JSON object (map[string]any); a non-object payload
// is written verbatim (no delta semantics). Use this for every section EXCEPT
// the per-agent registry files, which are written by WriteAgentEntry.
func WriteSection(name string, data any) (writePath, layer string, err error) {
	filename, ok := FileNameForSection(name)
	if !ok {
		return "", "", fmt.Errorf("unknown config section %q", name)
	}
	full, merr := json.MarshalIndent(data, "", "  ")
	if merr != nil {
		return "", "", fmt.Errorf("cannot serialize %s: %w", name, merr)
	}
	full = append(full, '\n')
	// Resolve the write target with the full body in hand (an agents.json that
	// references local-only items lands in .agents/).
	writePath, ok = WritePath(name, full)
	if !ok {
		return "", "", fmt.Errorf("unknown config section %q", name)
	}
	layer = paths.Layer(writePath)

	out := full
	if desired, ok := data.(map[string]any); ok {
		if delta, derr := OverlayBytes(filename, desired, writePath); derr == nil {
			out = delta
		} else {
			return "", "", derr
		}
	}
	if err := os.MkdirAll(filepath.Dir(writePath), 0o755); err != nil {
		return "", "", err
	}
	if err := AtomicWriteFile(writePath, out); err != nil {
		return "", "", err
	}
	return writePath, layer, nil
}

// AtomicWriteFile writes data to path via a sibling temp file and renames it
// into place. The temp file is removed on any failure. The destination's
// existing file mode is preserved when present; otherwise 0o644 is used. The
// parent directory must already exist.
//
// Before overwriting, it snapshots the target into the config-change journal
// (when EnableHistory is active) so the write can later be rolled back; with the
// journal off this is a byte-identical no-op.
func AtomicWriteFile(path string, data []byte) error {
	recordHistory(path, data)
	return atomicWriteRaw(path, data)
}

// atomicWriteRaw is AtomicWriteFile without the history snapshot — used both as
// the write engine and by the rollback restore (which must not re-journal).
func atomicWriteRaw(path string, data []byte) error {
	perm := os.FileMode(0o644)
	if st, err := os.Stat(path); err == nil {
		perm = st.Mode().Perm()
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-cfg-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return err
	}
	return nil
}

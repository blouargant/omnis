package registries

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/blouargant/omnis/internal/paths"
)

// ConfigFileName is the basename of the remote-registry config file.
const ConfigFileName = "remote_registries.json"

// onSave is an optional process-wide hook fired after a successful
// SaveRegistries. The semantic registry index (internal/regindex) registers it
// to rebuild proactively when the web UI or a tool install changes the
// registry list. Guarded so set/fire never race.
var (
	onSaveMu sync.RWMutex
	onSave   func()
)

// SetOnSave registers (or clears, with nil) the post-save hook. Set-once
// semantics fit the caller (a process-wide index built lazily), but repeated
// calls simply replace the hook.
func SetOnSave(fn func()) {
	onSaveMu.Lock()
	onSave = fn
	onSaveMu.Unlock()
}

func fireOnSave() {
	onSaveMu.RLock()
	fn := onSave
	onSaveMu.RUnlock()
	if fn != nil {
		fn()
	}
}

// ReadConfigPath returns the highest-precedence config path (or the would-be
// write location when none exists). Use this for reads.
func ReadConfigPath() string {
	return paths.FindConfig(ConfigFileName)
}

// WriteConfigPath returns the fixed per-user write location.
func WriteConfigPath() string {
	return filepath.Join(paths.ConfigWriteDir(), ConfigFileName)
}

// LoadRegistries reads remote_registries.json from path. A missing file is
// treated as an empty list.
func LoadRegistries(path string) ([]Registry, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return []Registry{}, nil
	}
	if err != nil {
		return nil, err
	}
	var list []Registry
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, err
	}
	return list, nil
}

// SaveRegistries writes the list to path, creating parent dirs as needed.
func SaveRegistries(path string, list []Registry) error {
	if list == nil {
		list = []Registry{}
	}
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := atomicWriteFile(path, data); err != nil {
		return err
	}
	fireOnSave()
	return nil
}

// NewID returns a short random hex ID suitable for a Registry.ID.
func NewID() string {
	b := make([]byte, 4)
	f, _ := os.Open("/dev/urandom")
	if f != nil {
		_, _ = io.ReadFull(f, b)
		f.Close()
	}
	return fmt.Sprintf("%08x", b)
}

// atomicWriteFile writes data to path via a sibling temp file and renames it
// into place. The temp file is removed on any failure.
func atomicWriteFile(path string, data []byte) error {
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

// FindByID returns a pointer to the registry with the given ID, or nil.
func FindByID(list []Registry, id string) *Registry {
	for i := range list {
		if list[i].ID == id {
			return &list[i]
		}
	}
	return nil
}

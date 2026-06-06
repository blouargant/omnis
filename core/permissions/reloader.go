package permissions

import (
	"context"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// Reloader serves a periodically refreshed Config snapshot. It composes a base
// file with optional overlay paths (e.g. the user-owned permissions.json) plus
// an explicit in-memory overlay. mtime polling is used instead of fsnotify so
// it works on every filesystem with no extra dependencies.
//
// On load, any tracked file still in the old (regex-tier) format is converted
// to the new nomenclature and rewritten in place, once, after a .bak backup is
// taken. The in-memory snapshot is always the converted form regardless of
// whether the rewrite succeeds, so behavior is correct even on a read-only base.
type Reloader struct {
	basePath       string
	overlayPaths   []string
	staticOverlays []*Config // appended after files

	interval time.Duration

	rules    atomic.Pointer[Config]
	mtimes   map[string]time.Time
	mtimesMu sync.Mutex
	upgraded map[string]bool
	onChange []func()
}

// NewReloader builds a Reloader. basePath is the canonical permissions.json;
// overlayPaths are merged in order after the base (missing files tolerated);
// staticOverlays are non-file rule sets appended last. The first Refresh runs
// synchronously so a snapshot is ready before Start returns.
func NewReloader(basePath string, overlayPaths []string, staticOverlays []*Config) *Reloader {
	r := &Reloader{
		basePath:       basePath,
		overlayPaths:   overlayPaths,
		staticOverlays: staticOverlays,
		interval:       3 * time.Second,
		mtimes:         map[string]time.Time{},
		upgraded:       map[string]bool{},
	}
	_ = r.Refresh()
	return r
}

// SetInterval overrides the polling period.
func (r *Reloader) SetInterval(d time.Duration) {
	if d > 0 {
		r.interval = d
	}
}

// OnChange registers a callback fired whenever the rule set is swapped.
func (r *Reloader) OnChange(fn func()) { r.onChange = append(r.onChange, fn) }

// Snapshot returns the current Config. Safe for concurrent use.
func (r *Reloader) Snapshot() *Config {
	if v := r.rules.Load(); v != nil {
		return v
	}
	return &Config{}
}

// Refresh forces an immediate reload regardless of mtime.
func (r *Reloader) Refresh() error {
	return r.reload(true)
}

// Start launches the polling loop. Cancel the context to stop.
func (r *Reloader) Start(ctx context.Context) {
	go func() {
		t := time.NewTicker(r.interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				_ = r.reload(false)
			}
		}
	}()
}

// reload re-reads base + overlays if mtimes changed (or force=true) and
// publishes the merged Config atomically.
func (r *Reloader) reload(force bool) error {
	changed := force || r.detectChange()
	if !changed {
		return nil
	}

	r.upgradeIfLegacy(r.basePath)
	base, err := Load(r.basePath)
	if err != nil {
		base = &Config{}
	}
	var overlays []*Config
	for _, p := range r.overlayPaths {
		r.upgradeIfLegacy(p)
		ov, oerr := Load(p)
		if oerr == nil && ov != nil {
			overlays = append(overlays, ov)
		} else if oerr != nil && !os.IsNotExist(oerr) && err == nil {
			err = oerr
		}
	}
	overlays = append(overlays, r.staticOverlays...)

	merged := Merge(base, overlays...)
	r.rules.Store(merged)
	for _, fn := range r.onChange {
		fn()
	}
	if err != nil {
		return fmt.Errorf("reload (partial): %w", err)
	}
	return nil
}

// upgradeIfLegacy rewrites an old-format file in the new nomenclature, once,
// keeping a .bak backup. Best-effort: read/write errors are ignored (Load still
// converts in memory).
func (r *Reloader) upgradeIfLegacy(path string) {
	if path == "" {
		return
	}
	r.mtimesMu.Lock()
	done := r.upgraded[path]
	r.mtimesMu.Unlock()
	if done {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil || !isLegacyFormat(data) {
		r.mtimesMu.Lock()
		r.upgraded[path] = true
		r.mtimesMu.Unlock()
		return
	}
	legacy, perr := parseLegacy(data)
	if perr != nil {
		return
	}
	cfg, _ := ConvertLegacy(legacy)
	_ = os.WriteFile(path+".bak", data, 0o644)
	if werr := writeConfigAtomic(path, cfg); werr == nil {
		fmt.Fprintf(os.Stderr, "permissions: upgraded %s to the new nomenclature (backup at %s.bak)\n", path, path)
	}
	r.mtimesMu.Lock()
	r.upgraded[path] = true
	r.mtimesMu.Unlock()
}

// detectChange returns true when any tracked file's mtime changed.
func (r *Reloader) detectChange() bool {
	r.mtimesMu.Lock()
	defer r.mtimesMu.Unlock()

	paths := append([]string{r.basePath}, r.overlayPaths...)
	changed := false
	for _, p := range paths {
		info, err := os.Stat(p)
		var mt time.Time
		if err == nil {
			mt = info.ModTime()
		}
		if prev, ok := r.mtimes[p]; !ok || !prev.Equal(mt) {
			r.mtimes[p] = mt
			changed = true
		}
	}
	return changed
}

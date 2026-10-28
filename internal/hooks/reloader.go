package hooks

import (
	"context"
	"os"
	"slices"
	"sync"
	"sync/atomic"
	"time"
)

// Merge combines base with overlays, concatenating each event's matchers in
// order (base first, then each overlay). Hooks are additive — overlays add
// commands rather than replacing them — so a user-layer hooks.json augments the
// project/system layer rather than shadowing it. nil inputs are tolerated.
func Merge(base *Config, overlays ...*Config) *Config {
	out := Empty()
	add := func(c *Config) {
		if c == nil {
			return
		}
		for ev, ms := range c.events {
			out.events[ev] = append(out.events[ev], ms...)
		}
	}
	add(base)
	for _, o := range overlays {
		add(o)
	}
	return out
}

// Reloader serves a periodically refreshed Config snapshot, composing a base
// hooks.json with optional overlay paths (e.g. a user-owned hooks.json). It
// polls mtimes (no fsnotify dependency), mirroring core/permissions.Reloader.
type Reloader struct {
	basePath     string
	overlayPaths []string
	pluginLayers []PluginLayer
	interval     time.Duration

	cfg      atomic.Pointer[Config]
	mtimes   map[string]time.Time
	mtimesMu sync.Mutex
	onChange []func()
}

// NewReloader builds a Reloader. basePath is the canonical hooks.json;
// overlayPaths are merged after it (missing files tolerated). The first Refresh
// runs synchronously so a snapshot is ready before Start returns.
func NewReloader(basePath string, overlayPaths []string) *Reloader {
	r := &Reloader{
		basePath:     basePath,
		overlayPaths: overlayPaths,
		interval:     3 * time.Second,
		mtimes:       map[string]time.Time{},
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

// OnChange registers a callback fired whenever the hook set is swapped.
func (r *Reloader) OnChange(fn func()) { r.onChange = append(r.onChange, fn) }

// Snapshot returns the current Config. Safe for concurrent use.
func (r *Reloader) Snapshot() *Config {
	if v := r.cfg.Load(); v != nil {
		return v
	}
	return Empty()
}

// SetPluginLayers replaces the Claude Code plugin hook layers and forces a
// reload. They merge after the base and overlays, each matcher tagged with its
// plugin. A call with an unchanged set is a no-op.
func (r *Reloader) SetPluginLayers(layers []PluginLayer) {
	r.mtimesMu.Lock()
	if slices.Equal(r.pluginLayers, layers) {
		r.mtimesMu.Unlock()
		return
	}
	r.pluginLayers = append([]PluginLayer(nil), layers...)
	r.mtimesMu.Unlock()
	_ = r.reload(true)
}

// Refresh forces an immediate reload regardless of mtime.
func (r *Reloader) Refresh() error { return r.reload(true) }

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

func (r *Reloader) reload(force bool) error {
	if !force && !r.detectChange() {
		return nil
	}
	base, err := Load(r.basePath)
	if err != nil {
		base = Empty()
	}
	r.mtimesMu.Lock()
	plugins := r.pluginLayers
	r.mtimesMu.Unlock()
	var overlays []*Config
	for _, p := range r.overlayPaths {
		ov, oerr := Load(p)
		if oerr == nil && ov != nil {
			overlays = append(overlays, ov)
		} else if oerr != nil && !os.IsNotExist(oerr) && err == nil {
			err = oerr
		}
	}
	for _, pl := range plugins {
		pc, perr := Load(pl.Path)
		if perr != nil || pc == nil {
			continue
		}
		ref := &PluginRef{Name: pl.Name, Root: pl.Root}
		for ev, ms := range pc.events {
			tagged := make([]Matcher, len(ms))
			for i, m := range ms {
				m.Plugin = ref
				tagged[i] = m
			}
			pc.events[ev] = tagged
		}
		overlays = append(overlays, pc)
	}
	r.cfg.Store(Merge(base, overlays...))
	for _, fn := range r.onChange {
		fn()
	}
	return err
}

func (r *Reloader) detectChange() bool {
	r.mtimesMu.Lock()
	defer r.mtimesMu.Unlock()
	paths := append([]string{r.basePath}, r.overlayPaths...)
	for _, pl := range r.pluginLayers {
		paths = append(paths, pl.Path)
	}
	changed := false
	for _, p := range paths {
		if p == "" {
			continue
		}
		var mt time.Time
		if info, err := os.Stat(p); err == nil {
			mt = info.ModTime()
		}
		if prev, ok := r.mtimes[p]; !ok || !prev.Equal(mt) {
			r.mtimes[p] = mt
			changed = true
		}
	}
	return changed
}

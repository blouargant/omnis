// Package regindex builds and queries a semantic index over every item
// advertised by the configured remote registries — skills, agents, MCP servers,
// squads, A2A agents, and slash commands — so the Helper agent can find a
// relevant item by meaning ("a skill about flaky tests") instead of paging
// through browse_registry listings.
//
// The index is metadata-only: each item embeds the name + description + tags
// that browsing already returns, so indexing adds no HTTP fetches beyond a
// normal browse. One item is indexed per (registry_id, kind, dir_path); ids
// are derived deterministically from that triple so a rebuild is idempotent.
//
// Staleness has two triggers. Search() self-heals: it compares a corpus hash
// of the configured registry list (ids + urls + kinds) against the manifest
// and rebuilds when they diverge, so a registry added by any means is picked
// up on the next search. Saves through registries.SaveRegistries additionally
// fire registries.OnSave, which this package wires to a proactive background
// rebuild. Remote *content* drift (a registry whose URL is unchanged but whose
// skills changed) is only caught by the explicit reindex_registries tool.
//
// Like the other recall features, it is embedder-gated: Open(nil, …) returns
// (nil, nil) so callers skip mounting the tools and fall back to browse.
package regindex

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"

	"github.com/blouargant/yoke/core/embed"
	"github.com/blouargant/yoke/internal/paths"
	"github.com/blouargant/yoke/internal/registries"
	"github.com/blouargant/yoke/internal/semindex"
)

const (
	// SearchToolName / ReindexToolName are the agent-facing tool names.
	SearchToolName  = "search_registries"
	ReindexToolName = "reindex_registries"

	kindSkill   = "skill"
	kindAgent   = "agent"
	kindMCP     = "mcp"
	kindA2A     = "a2a"
	kindSquad   = "squad"
	kindCommand = "command"
)

// Config supplies the path thunks regindex needs. All are optional; a nil or
// empty resolver degrades gracefully (config defaults to the read chain;
// missing registry dirs / installed-name maps only affect the Installed
// annotation).
type Config struct {
	ConfigPath func() string // remote_registries.json (read path)
	SkillsDir  func() string // local skills registry dir (for Installed)
	AgentsDir  func() string // local agents registry dir (for Installed)

	// Installed-name providers for the kinds whose browse takes a name set
	// rather than a directory. Nil ⇒ items of that kind index with installed=false.
	InstalledMCP      func() map[string]bool // mcp_config.json server names
	InstalledSquads   func() map[string]bool // agents.json squad names
	InstalledA2A      func() map[string]bool // a2a_config.json agent names
	InstalledCommands func() map[string]bool // user_commands.json command names
}

// Meta is the per-item metadata persisted in the sidecar and returned on a hit.
type Meta struct {
	RegistryID   string   `json:"registry_id"`
	RegistryName string   `json:"registry_name,omitempty"`
	Kind         string   `json:"kind"` // "skill" | "agent" | "mcp" | "a2a" | "squad" | "command"
	Name         string   `json:"name"`
	DirPath      string   `json:"dir_path"`
	Description  string   `json:"description,omitempty"`
	Tags         []string `json:"tags,omitempty"`
	Installed    bool     `json:"installed"`
}

// Index is the semantic index over remote registry items.
type Index struct {
	store *semindex.Store
	emb   embed.Embedder
	cfg   Config

	// browse turns one configured registry into its indexable items. It is a
	// field so tests can inject canned results without hitting the network;
	// production wiring leaves it as defaultBrowse (browse via the providers).
	browse func(reg registries.Registry, cfg Config) []semindex.Item

	mu      sync.Mutex // serialises Reindex (one rebuild at a time)
	running bool       // an OnSave-triggered background rebuild is in flight
}

// Open opens (or creates) the registries index at $YOKE_HOME/index/registries,
// backed by emb. Returns (nil, nil) when emb is nil so callers skip mounting
// the tools. The returned Index registers a save hook so web-UI registry edits
// trigger a proactive background rebuild.
func Open(emb embed.Embedder, cfg Config) (*Index, error) {
	if emb == nil {
		return nil, nil
	}
	store, err := semindex.Open(filepath.Join(paths.IndexDir(), "registries"), emb)
	if err != nil {
		return nil, err
	}
	i := &Index{store: store, emb: emb, cfg: cfg}
	i.browse = defaultBrowse
	registries.SetOnSave(i.onRegistriesSaved)
	return i, nil
}

// Len reports the number of indexed items.
func (i *Index) Len() int {
	if i == nil {
		return 0
	}
	return i.store.Len()
}

func itemID(registryID, kind, dirPath string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(registryID))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(kind))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(dirPath))
	return h.Sum64()
}

func (i *Index) configPath() string {
	if i.cfg.ConfigPath != nil {
		if p := i.cfg.ConfigPath(); p != "" {
			return p
		}
	}
	return registries.ReadConfigPath()
}

func resolve(fn func() string) string {
	if fn == nil {
		return ""
	}
	return fn()
}

// corpusHash hashes the configured registry list (id|url|kind) so Search can
// detect when the set of registries changed without inspecting remote content.
func corpusHash(list []registries.Registry) string {
	parts := make([]string, 0, len(list))
	for _, r := range list {
		parts = append(parts, r.ID+"|"+r.URL+"|"+r.NormalizedKind())
	}
	sort.Strings(parts)
	h := fnv.New64a()
	_, _ = h.Write([]byte(strings.Join(parts, "\n")))
	return fmt.Sprintf("%x", h.Sum64())
}

// itemText builds the string embedded for one item: name, then description,
// then tags. Registry descriptions are written to be discoverable, so this is
// usually enough to rank well without fetching the full body.
func itemText(name, description string, tags []string) string {
	var b strings.Builder
	b.WriteString(name)
	if d := strings.TrimSpace(description); d != "" {
		b.WriteString(". ")
		b.WriteString(d)
	}
	if len(tags) > 0 {
		b.WriteString(" (tags: ")
		b.WriteString(strings.Join(tags, ", "))
		b.WriteString(")")
	}
	return b.String()
}

// resolveMap calls fn (a Config installed-name thunk), tolerating nil.
func resolveMap(fn func() map[string]bool) map[string]bool {
	if fn == nil {
		return nil
	}
	return fn()
}

// indexable reports whether a browsed item's dir_path is a real, indexable
// entry (browse functions emit a sentinel "__truncated__" row when a tree is
// too large to walk fully).
func indexable(dirPath string) bool {
	return dirPath != "" && dirPath != "__truncated__"
}

// defaultBrowse is the production browse seam: it parses the registry's repo
// reference and browses it for every kind it serves, returning one indexable
// item per discovered skill / agent / mcp server / squad / a2a agent / command.
// A misconfigured URL or a failed browse of one kind yields no items for that
// kind rather than failing the whole rebuild.
func defaultBrowse(reg registries.Registry, cfg Config) []semindex.Item {
	ref, perr := registries.ParseRepoRef(reg.URL, reg.Provider)
	if perr != nil {
		return nil
	}
	var items []semindex.Item
	if reg.Serves(registries.KindSkills) {
		if skills, berr := registries.BrowseSkills(ref, reg.Token, resolve(cfg.SkillsDir)); berr == nil {
			for _, s := range skills {
				if !indexable(s.DirPath) {
					continue
				}
				items = append(items, newItem(reg, kindSkill, s.Name, s.DirPath, s.Description, s.Tags, s.Installed))
			}
		}
	}
	if reg.Serves(registries.KindAgents) {
		if agents, berr := registries.BrowseAgents(ref, reg.Token, resolve(cfg.AgentsDir)); berr == nil {
			for _, a := range agents {
				if !indexable(a.DirPath) {
					continue
				}
				items = append(items, newItem(reg, kindAgent, a.Name, a.DirPath, a.Description, a.Tags, a.Installed))
			}
		}
	}
	if reg.Serves(registries.KindMCP) {
		if mcp, berr := registries.BrowseMCPTools(ref, reg.Token, resolveMap(cfg.InstalledMCP)); berr == nil {
			for _, m := range mcp {
				if !indexable(m.DirPath) {
					continue
				}
				items = append(items, newItem(reg, kindMCP, m.Name, m.DirPath, m.Description, nil, m.Installed))
			}
		}
	}
	if reg.Serves(registries.KindSquads) {
		if squads, berr := registries.BrowseSquads(ref, reg.Token, resolveMap(cfg.InstalledSquads)); berr == nil {
			for _, s := range squads {
				if !indexable(s.DirPath) {
					continue
				}
				items = append(items, newItem(reg, kindSquad, s.Name, s.DirPath, s.Description, nil, s.Installed))
			}
		}
	}
	if reg.Serves(registries.KindA2A) {
		if a2a, berr := registries.BrowseA2AAgents(ref, reg.Token, resolveMap(cfg.InstalledA2A)); berr == nil {
			for _, a := range a2a {
				if !indexable(a.DirPath) {
					continue
				}
				items = append(items, newItem(reg, kindA2A, a.Name, a.DirPath, a.Description, nil, a.Installed))
			}
		}
	}
	if reg.Serves(registries.KindCommands) {
		if cmds, berr := registries.BrowseCommands(ref, reg.Token, resolveMap(cfg.InstalledCommands)); berr == nil {
			for _, c := range cmds {
				if !indexable(c.DirPath) {
					continue
				}
				items = append(items, newItem(reg, kindCommand, c.Name, c.DirPath, c.Description, nil, c.Installed))
			}
		}
	}
	return items
}

// newItem builds the semindex item for one registry entry.
func newItem(reg registries.Registry, kind, name, dirPath, description string, tags []string, installed bool) semindex.Item {
	meta, _ := json.Marshal(Meta{
		RegistryID: reg.ID, RegistryName: reg.Name, Kind: kind,
		Name: name, DirPath: dirPath, Description: description,
		Tags: tags, Installed: installed,
	})
	return semindex.Item{
		ID:   itemID(reg.ID, kind, dirPath),
		Text: itemText(name, description, tags),
		Meta: meta,
	}
}

// Reindex rebuilds the index from all configured registries. It browses each
// registry for every kind it serves (skills, agents, mcp, squads, a2a,
// commands), embeds name + description + tags per item, and replaces the index
// wholesale. Re-embedding
// unchanged text hits the embedder's on-disk content cache, so a rebuild is
// cheap when little changed. Returns the number of items indexed.
func (i *Index) Reindex(ctx context.Context) (indexed int, err error) {
	if i == nil {
		return 0, embed.ErrNoEmbedder
	}
	i.mu.Lock()
	defer i.mu.Unlock()

	list, err := registries.LoadRegistries(i.configPath())
	if err != nil {
		return 0, fmt.Errorf("regindex: load registries: %w", err)
	}

	var items []semindex.Item
	for _, reg := range list {
		items = append(items, i.browse(reg, i.cfg)...)
	}

	// Wholesale rebuild: a remote item removed from a registry must drop out of
	// the index, and stable ids alone can't express deletions without tracking.
	i.store.Reset()
	if len(items) > 0 {
		if uerr := i.store.Upsert(ctx, items); uerr != nil {
			return 0, uerr
		}
	}
	i.store.SetCorpusHash(corpusHash(list))
	if serr := i.store.Save(); serr != nil {
		return len(items), serr
	}
	return len(items), nil
}

// stale reports whether the configured registry set differs from what the
// index was built against. Cheap: reads the config file, no network.
func (i *Index) stale() bool {
	list, err := registries.LoadRegistries(i.configPath())
	if err != nil {
		return false
	}
	return corpusHash(list) != i.store.Manifest().CorpusHash
}

// Hit is one registry search result.
type Hit struct {
	RegistryID   string   `json:"registry_id"`
	RegistryName string   `json:"registry_name,omitempty"`
	Kind         string   `json:"kind"`
	Name         string   `json:"name"`
	DirPath      string   `json:"dir_path"`
	Description  string   `json:"description,omitempty"`
	Tags         []string `json:"tags,omitempty"`
	Installed    bool     `json:"installed"`
	Score        float32  `json:"score"`
}

// Search returns the top-k registry items most relevant to query. It lazily
// builds the index on the first call when empty, and rebuilds when the
// configured registry set has changed since the last build.
func (i *Index) Search(ctx context.Context, query string, k int) ([]Hit, error) {
	if i == nil {
		return nil, embed.ErrNoEmbedder
	}
	if i.store.Len() == 0 || i.stale() {
		if _, err := i.Reindex(ctx); err != nil {
			return nil, err
		}
	}
	if k <= 0 {
		k = 8
	}
	hits, err := i.store.Query(ctx, query, k)
	if err != nil {
		return nil, err
	}
	out := make([]Hit, 0, len(hits))
	for _, h := range hits {
		var m Meta
		_ = json.Unmarshal(h.Meta, &m)
		out = append(out, Hit{
			RegistryID: m.RegistryID, RegistryName: m.RegistryName, Kind: m.Kind,
			Name: m.Name, DirPath: m.DirPath, Description: m.Description,
			Tags: m.Tags, Installed: m.Installed, Score: h.Score,
		})
	}
	return out, nil
}

// onRegistriesSaved is the registries.OnSave hook: a registry was added,
// edited, or removed via the web UI or a tool install, so rebuild in the
// background. A single rebuild runs at a time; concurrent saves coalesce.
func (i *Index) onRegistriesSaved() {
	if i == nil {
		return
	}
	i.mu.Lock()
	if i.running {
		i.mu.Unlock()
		return
	}
	i.running = true
	i.mu.Unlock()
	go func() {
		defer func() {
			i.mu.Lock()
			i.running = false
			i.mu.Unlock()
		}()
		_, _ = i.Reindex(context.Background())
	}()
}

type searchIn struct {
	Query string `json:"query"`
	K     int    `json:"k"`
}
type searchOut struct {
	Results []Hit `json:"results"`
}
type reindexIn struct{}
type reindexOut struct {
	Indexed int `json:"indexed"`
}

// Tools returns the search_registries + reindex_registries tools. Returns nil
// when the index is unavailable (no embedder).
func (i *Index) Tools() []tool.Tool {
	if i == nil {
		return nil
	}
	search, err := functiontool.New(functiontool.Config{
		Name: SearchToolName,
		Description: "Find skills, agents, MCP servers, squads, A2A agents, and slash commands across ALL " +
			"configured remote registries by meaning, not name. Returns the best-matching items " +
			"(registry_id, kind, name, dir_path, description) for a natural-language need — use it BEFORE " +
			"browse_registry when you don't know which registry holds a capability, then get_remote_item the " +
			"returned dir_path to inspect before installing. " +
			"Arguments: `query` (string, required); `k` (int, optional, default 8).",
	}, func(_ tool.Context, in searchIn) (searchOut, error) {
		q := strings.TrimSpace(in.Query)
		if q == "" {
			return searchOut{}, fmt.Errorf("query is required")
		}
		hits, err := i.Search(context.Background(), q, in.K)
		if err != nil {
			return searchOut{}, err
		}
		return searchOut{Results: hits}, nil
	})
	if err != nil {
		return nil
	}
	reindex, err := functiontool.New(functiontool.Config{
		Name: ReindexToolName,
		Description: "Rebuild the semantic index of remote registry items by re-browsing every configured " +
			"registry. Call this when search_registries seems stale after a registry's remote content changed " +
			"(new registries are picked up automatically). No arguments.",
	}, func(_ tool.Context, _ reindexIn) (reindexOut, error) {
		n, err := i.Reindex(context.Background())
		if err != nil {
			return reindexOut{}, err
		}
		return reindexOut{Indexed: n}, nil
	})
	if err != nil {
		return []tool.Tool{search}
	}
	return []tool.Tool{search, reindex}
}

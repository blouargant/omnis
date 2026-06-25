package softskills

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
	"gopkg.in/yaml.v3"

	"github.com/blouargant/omnis/core/embed"
	"github.com/blouargant/omnis/internal/paths"
	"github.com/blouargant/omnis/internal/semindex"
)

// RecallToolName is the semantic-recall counterpart to list_softskills.
const RecallToolName = "recall_softskills"

// RecallProtocolAddendum is appended to LoaderProtocol only when recall is
// mounted, telling the model to rank with recall_softskills before the glob
// scan. The glob path stays authoritative as a completeness fallback.
const RecallProtocolAddendum = `
- A 'recall_softskills' tool is available: call it FIRST with the user's task as the query to semantically rank soft-skills, then 'load_softskill' the top matches. Still scan 'list_softskills' afterwards for completeness — recall ranks, it does not replace discovery.
`

type recallEntry struct {
	name        string
	description string
}

// recaller owns the soft-skill semantic index and keeps it in sync with the
// on-disk corpus (content-hash gated, so an unchanged corpus is never
// re-embedded).
type recaller struct {
	dir   string
	store *semindex.Store

	mu sync.Mutex
}

func newRecaller(dir string, emb embed.Embedder) (*recaller, error) {
	if dir == "" {
		dir = DefaultDir()
	}
	store, err := semindex.Open(filepath.Join(paths.IndexDir(), "softskills"), emb)
	if err != nil {
		return nil, err
	}
	return &recaller{dir: dir, store: store}, nil
}

// scan reads every <dir>/<name>/SKILL.md and returns its name + description.
func (r *recaller) scan() ([]recallEntry, error) {
	ents, err := os.ReadDir(r.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []recallEntry
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		md := filepath.Join(r.dir, e.Name(), "SKILL.md")
		b, err := os.ReadFile(md)
		if err != nil {
			continue
		}
		name, desc := parseFrontmatter(b)
		if name == "" {
			name = e.Name()
		}
		out = append(out, recallEntry{name: name, description: desc})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out, nil
}

func corpusHash(entries []recallEntry) string {
	h := sha256.New()
	for _, e := range entries {
		h.Write([]byte(e.name))
		h.Write([]byte{0})
		h.Write([]byte(e.description))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// refresh rebuilds the index from the corpus when its content hash changed.
func (r *recaller) refresh(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	entries, err := r.scan()
	if err != nil {
		return err
	}
	h := corpusHash(entries)
	if r.store.Manifest().CorpusHash == h && r.store.Len() == len(entries) {
		return nil // up to date
	}
	r.store.Reset()
	if len(entries) > 0 {
		items := make([]semindex.Item, 0, len(entries))
		for _, e := range entries {
			meta, _ := json.Marshal(map[string]string{"name": e.name, "description": e.description})
			items = append(items, semindex.Item{
				ID:   nameID(e.name),
				Text: e.name + "\n" + e.description,
				Meta: meta,
			})
		}
		if err := r.store.Upsert(ctx, items); err != nil {
			return err
		}
	}
	r.store.SetCorpusHash(h)
	return r.store.Save()
}

type recallIn struct {
	Query string `json:"query"`
	K     int    `json:"k,omitempty"`
}

type recallHit struct {
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Score       float32 `json:"score"`
}

type recallOut struct {
	Skills []recallHit `json:"skills"`
}

// RecallTool builds the recall_softskills tool over `dir`, backed by `emb`.
// Returns (nil, nil) when emb is nil so callers can skip mounting it.
func RecallTool(ctx context.Context, dir string, emb embed.Embedder) (tool.Tool, error) {
	if emb == nil {
		return nil, nil
	}
	r, err := newRecaller(dir, emb)
	if err != nil {
		return nil, err
	}
	// Warm the index once at construction; ignore errors (recall degrades to
	// empty results and the glob path still works).
	_ = r.refresh(ctx)

	desc := "Semantically rank curator-distilled soft-skills by relevance to a task. " +
		"Call this FIRST with the user's task as `query`, then `load_softskill` the top " +
		"matches by `name`. Complements (does not replace) `list_softskills`. " +
		"Arguments: `query` (string, required) — the task or question; `k` (int, optional, " +
		"default 5) — how many matches to return."

	t, err := functiontool.New(functiontool.Config{Name: RecallToolName, Description: desc},
		func(fnCtx tool.Context, in recallIn) (recallOut, error) {
			query := strings.TrimSpace(in.Query)
			if query == "" {
				return recallOut{}, fmt.Errorf("query is required")
			}
			k := in.K
			if k <= 0 {
				k = 5
			}
			// Pick up newly-curated soft-skills since construction.
			_ = r.refresh(context.Background())
			hits, err := r.store.Query(context.Background(), query, k)
			if err != nil {
				return recallOut{}, err
			}
			out := recallOut{Skills: make([]recallHit, 0, len(hits))}
			for _, h := range hits {
				var m struct {
					Name        string `json:"name"`
					Description string `json:"description"`
				}
				_ = json.Unmarshal(h.Meta, &m)
				out.Skills = append(out.Skills, recallHit{Name: m.Name, Description: m.Description, Score: h.Score})
			}
			return out, nil
		})
	if err != nil {
		return nil, err
	}
	return t, nil
}

// nameID hashes a soft-skill name to a stable external id (FNV-1a 64).
func nameID(name string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(name))
	return h.Sum64()
}

// parseFrontmatter extracts name + description from a SKILL.md YAML frontmatter
// block delimited by leading `---` ... `---`. Returns empty strings when absent.
func parseFrontmatter(b []byte) (name, description string) {
	s := string(b)
	if !strings.HasPrefix(s, "---") {
		return "", ""
	}
	rest := strings.TrimPrefix(s, "---")
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return "", ""
	}
	block := rest[:end]
	var fm struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
	}
	if err := yaml.Unmarshal([]byte(block), &fm); err != nil {
		return "", ""
	}
	return strings.TrimSpace(fm.Name), strings.TrimSpace(fm.Description)
}

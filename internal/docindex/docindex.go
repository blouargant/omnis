// Package docindex builds and queries a semantic index over yoke's own
// documentation (the web UI user docs and the developer docs) so the Helper
// agent can answer natural-language questions about yoke and quote the exact
// passage that supports the answer.
//
// It mirrors internal/codeindex: markdown files are split into overlapping
// line windows, re-indexing is content-hash gated per file (only changed files
// are re-embedded, chunks of deleted files are removed), and the index is a
// single global store under $YOKE_HOME/index/docs. Unlike codeindex it indexes
// several doc roots at once (see Roots), tracks the nearest preceding heading
// for each chunk, and stores the chunk text in metadata so a hit carries the
// quotable passage directly.
package docindex

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

	"github.com/blouargant/yoke/core/embed"
	"github.com/blouargant/yoke/internal/paths"
	"github.com/blouargant/yoke/internal/semindex"
)

const (
	// SearchToolName / ReindexToolName are the agent-facing tool names.
	SearchToolName  = "search_docs"
	ReindexToolName = "reindex_docs"

	chunkLines   = 50 // window size
	chunkOverlap = 10 // overlap between consecutive windows
	maxFileBytes = 1 << 20
)

// docExts is the set of file extensions treated as documentation.
var docExts = map[string]bool{".md": true, ".markdown": true}

// chunkMeta is the per-chunk metadata persisted in the index and returned on a
// hit. Text holds the quotable body so a search result needs no follow-up read.
type chunkMeta struct {
	Root    string `json:"root"`
	Path    string `json:"path"` // path relative to Root
	Heading string `json:"heading,omitempty"`
	Start   int    `json:"start"`
	End     int    `json:"end"`
	Text    string `json:"text"`
}

// fileRecord tracks a file's content hash and the chunk ids derived from it, so
// stale chunks can be removed when the file changes or is deleted.
type fileRecord struct {
	Hash string   `json:"hash"`
	IDs  []uint64 `json:"ids"`
}

// Index is the global documentation search index.
type Index struct {
	store     *semindex.Store
	emb       embed.Embedder
	rootsFn   func() []string
	filesPath string // path to docs.files.json sidecar

	mu    sync.Mutex
	files map[string]fileRecord // absolute file path → record
}

// Open opens (or creates) the documentation index backed by emb. Returns
// (nil, nil) when emb is nil so callers skip mounting the semantic tools.
func Open(emb embed.Embedder) (*Index, error) {
	if emb == nil {
		return nil, nil
	}
	dir := paths.IndexDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	store, err := semindex.Open(filepath.Join(dir, "docs"), emb)
	if err != nil {
		return nil, err
	}
	idx := &Index{
		store:     store,
		emb:       emb,
		rootsFn:   Roots,
		filesPath: filepath.Join(dir, "docs.files.json"),
		files:     map[string]fileRecord{},
	}
	idx.loadFiles()
	return idx, nil
}

func (i *Index) loadFiles() {
	b, err := os.ReadFile(i.filesPath)
	if err != nil {
		return
	}
	_ = json.Unmarshal(b, &i.files)
}

func (i *Index) saveFiles() error {
	b, err := json.MarshalIndent(i.files, "", "  ")
	if err != nil {
		return err
	}
	tmp := i.filesPath + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, i.filesPath)
}

// Len reports the number of indexed chunks.
func (i *Index) Len() int {
	if i == nil {
		return 0
	}
	return i.store.Len()
}

func chunkID(absPath string, start int) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(absPath))
	_, _ = h.Write([]byte{0})
	_, _ = fmt.Fprintf(h, "%d", start)
	return h.Sum64()
}

// docFile pairs a markdown file's absolute path with the root it was found
// under (so hits can report a clean root-relative path).
type docFile struct {
	abs  string
	root string
	rel  string
}

// listFiles walks every configured doc root and returns the markdown files,
// sorted by absolute path. A file reachable from more than one root is reported
// once (first root wins).
func (i *Index) listFiles() []docFile {
	seen := map[string]bool{}
	var out []docFile
	for _, root := range i.rootsFn() {
		_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			if !docExts[strings.ToLower(filepath.Ext(path))] {
				return nil
			}
			if info.Size() > maxFileBytes {
				return nil
			}
			abs, aerr := filepath.Abs(path)
			if aerr != nil {
				abs = path
			}
			if seen[abs] {
				return nil
			}
			rel, rerr := filepath.Rel(root, abs)
			if rerr != nil {
				rel = filepath.Base(abs)
			}
			seen[abs] = true
			out = append(out, docFile{abs: abs, root: root, rel: rel})
			return nil
		})
	}
	sort.Slice(out, func(a, b int) bool { return out[a].abs < out[b].abs })
	return out
}

// headingBefore returns the most recent markdown ATX heading text at or before
// line index end (exclusive), or "" if none precedes the window.
func headingBefore(lines []string, end int) string {
	for j := end - 1; j >= 0; j-- {
		l := strings.TrimSpace(lines[j])
		if strings.HasPrefix(l, "#") {
			return strings.TrimSpace(strings.TrimLeft(l, "#"))
		}
	}
	return ""
}

// chunkFile splits a doc file's content into overlapping line windows and
// returns the semindex items + the chunk ids produced.
func (f docFile) chunkFile(content []byte) ([]semindex.Item, []uint64) {
	lines := strings.Split(string(content), "\n")
	var items []semindex.Item
	var ids []uint64
	for start := 0; start < len(lines); start += chunkLines - chunkOverlap {
		end := start + chunkLines
		if end > len(lines) {
			end = len(lines)
		}
		body := strings.TrimSpace(strings.Join(lines[start:end], "\n"))
		if body == "" {
			if end >= len(lines) {
				break
			}
			continue
		}
		heading := headingBefore(lines, end)
		meta, _ := json.Marshal(chunkMeta{
			Root:    f.root,
			Path:    f.rel,
			Heading: heading,
			Start:   start + 1, // 1-based, inclusive
			End:     end,
			Text:    body,
		})
		id := chunkID(f.abs, start+1)
		// Embed the path + heading alongside the body so file/section names
		// contribute to relevance, mirroring codeindex's path-prefixed text.
		text := f.rel
		if heading != "" {
			text += " " + heading
		}
		text += "\n" + body
		items = append(items, semindex.Item{ID: id, Text: text, Meta: meta})
		ids = append(ids, id)
		if end >= len(lines) {
			break
		}
	}
	return items, ids
}

// Reindex brings the index up to date with the doc roots. Only changed/new
// files are re-embedded; chunks of changed or deleted files are removed first.
// It is a cheap no-op when nothing changed, so it is safe to call at every
// startup. Returns the count of files (re)indexed and removed.
func (i *Index) Reindex(ctx context.Context) (indexed, removed int, err error) {
	if i == nil {
		return 0, 0, embed.ErrNoEmbedder
	}
	files := i.listFiles()

	i.mu.Lock()
	defer i.mu.Unlock()

	seen := make(map[string]bool, len(files))
	for _, f := range files {
		seen[f.abs] = true
		content, rerr := os.ReadFile(f.abs)
		if rerr != nil || len(content) > maxFileBytes {
			continue
		}
		sum := sha256.Sum256(content)
		hash := hex.EncodeToString(sum[:])
		if rec, ok := i.files[f.abs]; ok && rec.Hash == hash {
			continue // unchanged
		}
		if rec, ok := i.files[f.abs]; ok && len(rec.IDs) > 0 {
			_ = i.store.Remove(rec.IDs...)
		}
		items, ids := f.chunkFile(content)
		if len(items) == 0 {
			delete(i.files, f.abs)
			continue
		}
		if uerr := i.store.Upsert(ctx, items); uerr != nil {
			return indexed, removed, uerr
		}
		i.files[f.abs] = fileRecord{Hash: hash, IDs: ids}
		indexed++
	}
	// Remove chunks for files that disappeared.
	for abs, rec := range i.files {
		if seen[abs] {
			continue
		}
		_ = i.store.Remove(rec.IDs...)
		delete(i.files, abs)
		removed++
	}

	if err := i.store.Save(); err != nil {
		return indexed, removed, err
	}
	return indexed, removed, i.saveFiles()
}

// Hit is one documentation search result. Text is the quotable passage.
type Hit struct {
	Root    string  `json:"root"`
	Path    string  `json:"path"`
	Heading string  `json:"heading,omitempty"`
	Start   int     `json:"start"`
	End     int     `json:"end"`
	Text    string  `json:"text"`
	Score   float32 `json:"score"`
}

// Search returns the top-k passages most relevant to the query. It lazily
// builds the index on the first call when empty.
func (i *Index) Search(ctx context.Context, query string, k int) ([]Hit, error) {
	if i == nil {
		return nil, embed.ErrNoEmbedder
	}
	if i.store.Len() == 0 {
		if _, _, err := i.Reindex(ctx); err != nil {
			return nil, err
		}
	}
	if k <= 0 {
		k = 6
	}
	hits, err := i.store.Query(ctx, query, k)
	if err != nil {
		return nil, err
	}
	out := make([]Hit, 0, len(hits))
	for _, h := range hits {
		var m chunkMeta
		_ = json.Unmarshal(h.Meta, &m)
		out = append(out, Hit{
			Root:    m.Root,
			Path:    m.Path,
			Heading: m.Heading,
			Start:   m.Start,
			End:     m.End,
			Text:    m.Text,
			Score:   h.Score,
		})
	}
	return out, nil
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
	Removed int `json:"removed"`
	Total   int `json:"total_chunks"`
}

// Tools returns the search_docs + reindex_docs tools. Returns nil when the
// index is unavailable (no embedder); callers fall back to the always-mounted
// NewNavTools (list_docs / read_doc / grep_docs).
func (i *Index) Tools() []tool.Tool {
	if i == nil {
		return nil
	}
	search, err := functiontool.New(functiontool.Config{
		Name: SearchToolName,
		Description: "Answer a question about yoke from its own documentation. Returns the most relevant " +
			"documentation passages — each with its source `path`, `heading`, line range, and the quoted " +
			"`text` — ranked by meaning, not keyword. Use this to ground answers about yoke's features, " +
			"configuration, and behaviour, then quote the returned text. Arguments: `query` (string, " +
			"required); `k` (int, optional, default 6).",
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
		Description: "Rebuild the semantic documentation index (incremental: only changed files are " +
			"re-embedded). Call this if search_docs results look stale after the docs changed. No arguments.",
	}, func(_ tool.Context, _ reindexIn) (reindexOut, error) {
		indexed, removed, err := i.Reindex(context.Background())
		if err != nil {
			return reindexOut{}, err
		}
		return reindexOut{Indexed: indexed, Removed: removed, Total: i.Len()}, nil
	})
	if err != nil {
		return []tool.Tool{search}
	}
	return []tool.Tool{search, reindex}
}

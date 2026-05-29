// Package codeindex builds and queries a semantic index over a project's
// source files so agents can find relevant code by natural-language query
// instead of guessing file names for grep.
//
// v1 chunking is line-window based: each source file is split into overlapping
// ~50-line windows. Re-indexing is content-hash gated per file — only changed
// files are re-embedded, and chunks of deleted files are removed. The index is
// keyed by the repo root path so multiple projects never collide.
package codeindex

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"os"
	"os/exec"
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
	SearchToolName  = "search_code"
	ReindexToolName = "reindex_code"

	chunkLines   = 50 // window size
	chunkOverlap = 10 // overlap between consecutive windows
	maxFileBytes = 1 << 20
)

// sourceExts is the allow-list of indexable text/source file extensions.
var sourceExts = map[string]bool{
	".go": true, ".py": true, ".js": true, ".ts": true, ".jsx": true, ".tsx": true,
	".java": true, ".c": true, ".h": true, ".cc": true, ".cpp": true, ".hpp": true,
	".rs": true, ".rb": true, ".php": true, ".cs": true, ".kt": true, ".swift": true,
	".scala": true, ".sh": true, ".bash": true, ".sql": true, ".proto": true,
	".md": true, ".rst": true, ".txt": true, ".yaml": true, ".yml": true,
	".toml": true, ".json": true, ".html": true, ".css": true, ".scss": true,
}

// chunkMeta is the per-chunk metadata persisted in the index and returned on a
// hit.
type chunkMeta struct {
	Path  string `json:"path"`
	Start int    `json:"start"`
	End   int    `json:"end"`
}

// fileRecord tracks a file's content hash and the chunk ids derived from it, so
// stale chunks can be removed when the file changes or is deleted.
type fileRecord struct {
	Hash string   `json:"hash"`
	IDs  []uint64 `json:"ids"`
}

// Index is a per-repo code search index.
type Index struct {
	repo      string
	store     *semindex.Store
	emb       embed.Embedder
	filesPath string // path to codebase.files.json sidecar

	mu    sync.Mutex
	files map[string]fileRecord // repo-relative path → record
}

// Open opens (or creates) the code index for repoRoot, backed by emb. Returns
// (nil, nil) when emb is nil so callers skip mounting the tools.
func Open(repoRoot string, emb embed.Embedder) (*Index, error) {
	if emb == nil {
		return nil, nil
	}
	abs, err := filepath.Abs(repoRoot)
	if err != nil {
		abs = repoRoot
	}
	h := sha256.Sum256([]byte(abs))
	dir := filepath.Join(paths.IndexDir(), hex.EncodeToString(h[:])[:16])
	store, err := semindex.Open(filepath.Join(dir, "codebase"), emb)
	if err != nil {
		return nil, err
	}
	idx := &Index{repo: abs, store: store, emb: emb, files: map[string]fileRecord{}}
	idx.loadFiles(filepath.Join(dir, "codebase.files.json"))
	idx.filesPath = filepath.Join(dir, "codebase.files.json")
	return idx, nil
}

// filesPath is stored separately so loadFiles can run before it is set.
func (i *Index) loadFiles(path string) {
	b, err := os.ReadFile(path)
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

func chunkID(relPath string, start int) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(relPath))
	_, _ = h.Write([]byte{0})
	_, _ = fmt.Fprintf(h, "%d", start)
	return h.Sum64()
}

// listFiles returns the repo-relative source files to index. It prefers
// `git ls-files` (which honours .gitignore) and falls back to a filtered walk.
func (i *Index) listFiles() ([]string, error) {
	if rel, err := i.gitFiles(); err == nil {
		return rel, nil
	}
	return i.walkFiles()
}

func (i *Index) gitFiles() ([]string, error) {
	cmd := exec.Command("git", "-C", i.repo, "ls-files", "-z")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var rel []string
	for _, p := range strings.Split(string(out), "\x00") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if sourceExts[strings.ToLower(filepath.Ext(p))] {
			rel = append(rel, p)
		}
	}
	sort.Strings(rel)
	return rel, nil
}

var skipDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true, "dist": true,
	"bin": true, ".yoke": true, ".agents": true, "tmp": true,
}

func (i *Index) walkFiles() ([]string, error) {
	var rel []string
	err := filepath.Walk(i.repo, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if skipDirs[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !sourceExts[strings.ToLower(filepath.Ext(path))] {
			return nil
		}
		if info.Size() > maxFileBytes {
			return nil
		}
		if r, rerr := filepath.Rel(i.repo, path); rerr == nil {
			rel = append(rel, r)
		}
		return nil
	})
	sort.Strings(rel)
	return rel, err
}

// chunkFile splits a file's content into overlapping line windows and returns
// the semindex items + the chunk ids produced.
func chunkFile(relPath string, content []byte) ([]semindex.Item, []uint64) {
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
		// 1-based, inclusive line numbers for human-friendly meta.
		meta, _ := json.Marshal(chunkMeta{Path: relPath, Start: start + 1, End: end})
		id := chunkID(relPath, start+1)
		text := relPath + "\n" + body
		items = append(items, semindex.Item{ID: id, Text: text, Meta: meta})
		ids = append(ids, id)
		if end >= len(lines) {
			break
		}
	}
	return items, ids
}

// Reindex brings the index up to date with the repo. Only changed/new files
// are re-embedded; chunks of changed or deleted files are removed first.
// Returns the count of files (re)indexed and removed.
func (i *Index) Reindex(ctx context.Context) (indexed, removed int, err error) {
	if i == nil {
		return 0, 0, embed.ErrNoEmbedder
	}
	files, err := i.listFiles()
	if err != nil {
		return 0, 0, fmt.Errorf("codeindex: list files: %w", err)
	}

	i.mu.Lock()
	defer i.mu.Unlock()

	seen := make(map[string]bool, len(files))
	for _, rel := range files {
		seen[rel] = true
		content, rerr := os.ReadFile(filepath.Join(i.repo, rel))
		if rerr != nil || len(content) > maxFileBytes {
			continue
		}
		sum := sha256.Sum256(content)
		hash := hex.EncodeToString(sum[:])
		if rec, ok := i.files[rel]; ok && rec.Hash == hash {
			continue // unchanged
		}
		// Changed or new: drop old chunks (if any), re-chunk + embed.
		if rec, ok := i.files[rel]; ok && len(rec.IDs) > 0 {
			_ = i.store.Remove(rec.IDs...)
		}
		items, ids := chunkFile(rel, content)
		if len(items) == 0 {
			delete(i.files, rel)
			continue
		}
		if uerr := i.store.Upsert(ctx, items); uerr != nil {
			return indexed, removed, uerr
		}
		i.files[rel] = fileRecord{Hash: hash, IDs: ids}
		indexed++
	}
	// Remove chunks for files that disappeared.
	for rel, rec := range i.files {
		if seen[rel] {
			continue
		}
		_ = i.store.Remove(rec.IDs...)
		delete(i.files, rel)
		removed++
	}

	if err := i.store.Save(); err != nil {
		return indexed, removed, err
	}
	return indexed, removed, i.saveFiles()
}

// Hit is one code search result.
type Hit struct {
	Path  string  `json:"path"`
	Start int     `json:"start"`
	End   int     `json:"end"`
	Score float32 `json:"score"`
}

// Search returns the top-k chunks most relevant to the query. It lazily builds
// the index on the first call when empty.
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
		k = 8
	}
	hits, err := i.store.Query(ctx, query, k)
	if err != nil {
		return nil, err
	}
	out := make([]Hit, 0, len(hits))
	for _, h := range hits {
		var m chunkMeta
		_ = json.Unmarshal(h.Meta, &m)
		out = append(out, Hit{Path: m.Path, Start: m.Start, End: m.End, Score: h.Score})
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

// Tools returns the search_code + reindex_code tools. Returns nil when the
// index is unavailable (no embedder).
func (i *Index) Tools() []tool.Tool {
	if i == nil {
		return nil
	}
	search, err := functiontool.New(functiontool.Config{
		Name: SearchToolName,
		Description: "Find code by meaning, not file name. Returns the most relevant file path + line range " +
			"for a natural-language query — use it BEFORE broad grep/file scans when you don't know where " +
			"something lives, then run_read the returned ranges. Arguments: `query` (string, required); " +
			"`k` (int, optional, default 8).",
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
		Description: "Rebuild the semantic code index for the current repository (incremental: only changed " +
			"files are re-embedded). Call this after large edits if search_code seems stale. No arguments.",
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

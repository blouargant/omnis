// Package collectionctx implements per-collection context — omnis's thematic,
// cross-repo memory layer. Where AGENT.md (see internal/agentmd) scopes project
// memory to a working directory, a collection carries persistent context that
// follows a *workstream* across repos: hand-authored instructions plus a
// distilled memory block, injected into the answering root's system instruction
// for every session filed under that collection.
//
// Storage is filesystem-only under $OMNIS_HOME/collections/<name>/:
//
//	instructions.md — hand-edited, stable (imperative guidance)
//	memory.md       — distilled/evolving facts (the Phase-2 target; user-typed today)
//
// The package depends only on internal/paths + stdlib (no agent/sessions import),
// so the agent package can resolve the injected block without an import cycle and
// both the server and any future distiller can read/write the files. With no
// files for a collection (or the virtual General bucket, whose name resolves to
// ""), Resolve returns "" — a zero-cost no-op, so behaviour is unchanged for
// anyone not using the feature.
package collectionctx

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/blouargant/omnis/internal/paths"
)

const (
	instructionsFile = "instructions.md"
	memoryFile       = "memory.md"
	prevMemoryFile   = "memory.prev.md"
)

// baseDir is the parent of every per-collection directory. It sits beside
// collections.json (both under paths.ConfigWriteDir()) so a collection's scalars
// (collections.json) and its prose (here) live in the same state root.
func baseDir() string {
	return filepath.Join(paths.ConfigWriteDir(), "collections")
}

// safeSegment returns a filesystem-safe single path segment for a collection
// name, or "" when the name is unusable as a directory (blank, ".", "..", or
// containing a path separator). ValidCollectionName already rejects separators
// and control chars, but ".."/"." slip through it — this is the defence-in-depth
// that keeps a name that reaches disk from ever escaping baseDir.
func safeSegment(name string) string {
	n := strings.TrimSpace(name)
	if n == "" || n == "." || n == ".." {
		return ""
	}
	if strings.ContainsAny(n, `/\`) || n != filepath.Base(n) {
		return ""
	}
	return n
}

// Dir returns the directory holding a collection's prose, or "" for an unusable
// name. The directory is not created here — the writers create it on demand.
func Dir(name string) string {
	seg := safeSegment(name)
	if seg == "" {
		return ""
	}
	return filepath.Join(baseDir(), seg)
}

// InstructionsPath / MemoryPath return the on-disk path of a collection's two
// prose files, or "" for an unusable name.
func InstructionsPath(name string) string { return filePath(name, instructionsFile) }
func MemoryPath(name string) string       { return filePath(name, memoryFile) }

func filePath(name, leaf string) string {
	dir := Dir(name)
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, leaf)
}

// ReadInstructions / ReadMemory return the raw text of a collection's file, or ""
// when it is missing/unreadable/unusable name.
func ReadInstructions(name string) string { return readFile(InstructionsPath(name)) }
func ReadMemory(name string) string       { return readFile(MemoryPath(name)) }

func readFile(path string) string {
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

// WriteInstructions / WriteMemory replace a collection's file. An empty body
// removes the file (so an emptied editor field leaves no stray file behind). An
// unusable name is an error.
func WriteInstructions(name, text string) error { return writeFile(InstructionsPath(name), text) }
func WriteMemory(name, text string) error       { return writeFile(MemoryPath(name), text) }

// PrevMemoryPath returns the on-disk path of a collection's previous-memory
// snapshot (used by auto-update's revert net), or "" for an unusable name.
func PrevMemoryPath(name string) string { return filePath(name, prevMemoryFile) }

// ReadPrevMemory returns the previous-memory snapshot text, or "" when absent.
func ReadPrevMemory(name string) string { return readFile(PrevMemoryPath(name)) }

// WritePrevMemory replaces the snapshot (an empty body removes it).
func WritePrevMemory(name, text string) error { return writeFile(PrevMemoryPath(name), text) }

// HasPrevMemory reports whether a non-empty snapshot exists (so the UI only
// offers Revert when there is prior memory to restore).
func HasPrevMemory(name string) bool { return strings.TrimSpace(ReadPrevMemory(name)) != "" }

// RemovePrevMemory drops the snapshot; a missing file is a no-op.
func RemovePrevMemory(name string) error {
	p := PrevMemoryPath(name)
	if p == "" {
		return nil
	}
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func writeFile(path, text string) error {
	if path == "" {
		return fmt.Errorf("collectionctx: invalid collection name")
	}
	if strings.TrimSpace(text) == "" {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".ccwrite_*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write([]byte(text)); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, path)
}

// RenameDir migrates a collection's prose directory when the collection is
// renamed, so its instructions/memory follow the new name. Best-effort: a
// missing source (no prose yet) or a name change that only differs in case is a
// no-op, and it never clobbers an existing destination.
func RenameDir(oldName, newName string) error {
	src, dst := Dir(oldName), Dir(newName)
	if src == "" || dst == "" || src == dst {
		return nil
	}
	if _, err := os.Stat(src); err != nil {
		return nil // nothing to migrate
	}
	if _, err := os.Stat(dst); err == nil {
		return nil // don't overwrite an existing destination
	}
	if err := os.MkdirAll(baseDir(), 0o755); err != nil {
		return err
	}
	return os.Rename(src, dst)
}

// RemoveDir deletes a collection's prose directory when the collection is
// deleted. A missing directory is a no-op.
func RemoveDir(name string) error {
	dir := Dir(name)
	if dir == "" {
		return nil
	}
	if err := os.RemoveAll(dir); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// HasContext reports whether a collection has any non-empty prose (instructions
// or memory) — used to badge configured collections in the UI without shipping
// the full text.
func HasContext(name string) bool {
	return strings.TrimSpace(ReadInstructions(name)) != "" ||
		strings.TrimSpace(ReadMemory(name)) != ""
}

// Resolve returns the rendered context block for a collection, ready to prepend
// to a system instruction, or "" when the collection has no prose (or the name
// is the empty General bucket / unusable). Cached per collection and re-rendered
// only when a contributing file's size or mtime changes, so calling it on every
// turn is cheap.
func Resolve(name string) string {
	if safeSegment(name) == "" {
		return ""
	}
	instr := InstructionsPath(name)
	mem := MemoryPath(name)
	sig := signature(name, instr, mem)
	if cached, ok := cacheGet(name, sig); ok {
		return cached
	}
	out := render(name, readFile(instr), readFile(mem))
	cachePut(name, sig, out)
	return out
}

// render wraps a collection's instructions + memory in a stable container so the
// model can tell workstream context apart from its own instruction and from
// AGENT.md project memory. Empty sections are omitted; both empty ⇒ "".
func render(name, instructions, memory string) string {
	instructions = strings.TrimSpace(instructions)
	memory = strings.TrimSpace(memory)
	if instructions == "" && memory == "" {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "<collection-context name=%q>\n", name)
	b.WriteString("The following is persistent context for this collection " +
		"(a thematic group of chats). Treat it as authoritative guidance for " +
		"this workstream.\n")
	if instructions != "" {
		fmt.Fprintf(&b, "<instructions>\n%s\n</instructions>\n", instructions)
	}
	if memory != "" {
		fmt.Fprintf(&b, "<memory>\n%s\n</memory>\n", memory)
	}
	b.WriteString("</collection-context>")
	return b.String()
}

// signature builds a cheap change key from the two files' sizes and mtimes, plus
// the name (so a rename re-renders the header). A missing file contributes a
// stable marker so its later creation invalidates the cache.
func signature(name, instr, mem string) string {
	var b strings.Builder
	b.WriteString(name)
	b.WriteByte('\n')
	for _, p := range []string{instr, mem} {
		if st, err := os.Stat(p); err == nil {
			fmt.Fprintf(&b, "%s|%d|%d\n", p, st.Size(), st.ModTime().UnixNano())
		} else {
			fmt.Fprintf(&b, "%s|-\n", p)
		}
	}
	return b.String()
}

// ── per-collection render cache ─────────────────────────────────────────────

type cacheEntry struct {
	sig  string
	text string
}

var (
	cacheMu sync.RWMutex
	cache   = map[string]cacheEntry{}
)

func cacheGet(name, sig string) (string, bool) {
	cacheMu.RLock()
	e, ok := cache[name]
	cacheMu.RUnlock()
	if ok && e.sig == sig {
		return e.text, true
	}
	return "", false
}

func cachePut(name, sig, text string) {
	cacheMu.Lock()
	cache[name] = cacheEntry{sig: sig, text: text}
	cacheMu.Unlock()
}

// Package skills wraps ADK's skilltoolset with a registry-based source.
// Each skill lives in `<registryDir>/<name>/SKILL.md` with YAML front matter.
// Agents specify which skills they need by name; only those are exposed.
package skills

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path"
	"strings"
	"time"

	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/skilltoolset"
	"google.golang.org/adk/v2/tool/skilltoolset/skill"

	"github.com/blouargant/omnis/internal/paths"
)

// skillInstruction is the skill toolset's system instruction; ADK places it
// right before the <available_skills> list. It is the ONE place the skill
// discipline is stated: it used to be spread over a LoaderProtocol prepended
// to the agent instruction plus this text (~540 tokens on every call of 16
// agents). Every rule is kept: load every matching skill (the k8s audit
// precision depends on it), the parameter is `name`, a skill's files are read
// with load_skill_resource, never guess what a skill does not document.
const skillInstruction = `Skills are authored playbooks, listed with their descriptions in the <available_skills> block below (` + "`list_skills`" + ` returns the same list: call it only if the block is missing or a skill was installed meanwhile).

MANDATORY — before your first other tool call, call ` + "`load_skill`" + ` with ` + "`name=\"<skill>\"`" + ` (the parameter is ` + "`name`" + `, not ` + "`skill_name`" + `) for EVERY listed skill whose description overlaps any aspect of the task: its technology (kubernetes, postgres…), artefact (pod, table…) or symptom (crash, error, slow…). Load all matches, in one step if you like; skills of the same domain are complementary, never alternatives, and loading one too many beats missing one. A loaded skill overrides your default behaviour: follow its steps, all of them, in order, before replying.

A skill's other files: ` + "`load_skill_resource`" + ` with ` + "`skill_name`" + ` and ` + "`resource_path`" + ` exactly as SKILL.md names it (e.g. ` + "`projects.md`, `references/guide.md`" + `); do not read them with other tools. Load the detail file for your part of the task before acting, and never guess a command, sub-command or option the skill's files do not document.
`

// Toolset returns an ADK tool.Toolset that exposes skills merged across all
// search directories (paths.SkillsAllSearchDirs). When skillNames is nil or
// empty all discovered skills are visible; when non-empty only the named
// skills are exposed.
func Toolset(ctx context.Context, skillNames []string) (tool.Toolset, error) {
	// Ensure the primary write-target directory exists so the web UI can install
	// into it even when no skill is present yet.
	if err := os.MkdirAll(paths.SkillsRegistryWriteDir(), 0o755); err != nil {
		return nil, err
	}
	base := newMultiDirSkillsFS(paths.SkillsAllSearchDirs())
	fsys := base
	if len(skillNames) != 0 {
		fsys = newFilteredSkillsFS(base, skillNames)
	}
	// Wrap the upstream filesystem source so a single malformed skill (bad
	// frontmatter, or a name that doesn't match its directory) is skipped
	// instead of failing the whole toolset — see resilientSource.
	src := resilientSource{Source: skill.NewFileSystemSource(fsys), fsys: fsys}
	ts, err := skilltoolset.New(ctx, skilltoolset.Config{
		Source:            src,
		SystemInstruction: skillInstruction,
	})
	if err != nil {
		return nil, err
	}
	// When a process-wide dependency gate is installed, decorate load_skill so a
	// loaded skill's declared `requires:` are checked/installed before the model
	// proceeds. With no gate this is the byte-identical original behaviour.
	if g := depGate(); g != nil {
		return &gatedSkillToolset{SkillToolset: ts, gate: g}, nil
	}
	return ts, nil
}

// ── resilientSource ───────────────────────────────────────────────────────

// resilientSource wraps an ADK skill.Source so a single malformed skill does
// not take down every agent that uses skills. ADK's fileSystemSource.
// ListFrontmatters fails atomically: it silently ignores directories without a
// SKILL.md, but a directory whose SKILL.md has bad frontmatter — or a name that
// doesn't match its directory — returns a hard error that propagates out of
// SkillToolset.ProcessRequest and aborts the whole agent turn. This wrapper
// extends ADK's "silently ignore non-skill directories" behaviour to also skip
// (with a logged warning) any directory that fails to load, so one broken skill
// in the registry is merely invisible rather than fatal.
//
// ListFrontmatters' fallback only runs when the upstream scan fails, so it is
// byte-identical when every skill is valid. LoadResource/ListResources are
// replaced (see "resource access anywhere inside the skill" below); the other
// methods delegate unchanged via the embedded Source.
type resilientSource struct {
	skill.Source
	fsys fs.FS
}

func (r resilientSource) ListFrontmatters(ctx context.Context) ([]*skill.Frontmatter, error) {
	// Fast path: the upstream scan succeeds whenever every skill is valid.
	if fms, err := r.Source.ListFrontmatters(ctx); err == nil {
		return fms, nil
	}
	// Slow path: re-derive the listing directory-by-directory, dropping any
	// skill that fails to load. Enumerate the same fs.FS the inner source reads
	// from so layer precedence / name filtering stay identical.
	entries, err := fs.ReadDir(r.fsys, ".")
	if err != nil {
		// Can't even enumerate — surface the original upstream error.
		fms, lerr := r.Source.ListFrontmatters(ctx)
		if lerr != nil {
			return nil, lerr
		}
		return fms, nil
	}
	var out []*skill.Frontmatter
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		fm, lerr := r.Source.LoadFrontmatter(ctx, e.Name())
		if lerr != nil {
			// ErrSkillNotFound (no SKILL.md) is an ordinary non-skill dir; ADK
			// already ignores those silently. Anything else is a real defect
			// (bad frontmatter, name mismatch) worth surfacing in the log.
			if !errors.Is(lerr, skill.ErrSkillNotFound) {
				log.Printf("skills: skipping invalid skill %q: %v", e.Name(), lerr)
			}
			continue
		}
		out = append(out, fm)
	}
	return out, nil
}

// ── resource access anywhere inside the skill ────────────────────────────

// ADK's fileSystemSource only serves resources under references/, assets/ and
// scripts/. Many real skills (including Anthropic's own and the iaparc CLI
// skill) keep their detail files at the skill ROOT or in other subfolders and
// point to them from SKILL.md — with ADK's rule an agent can read SKILL.md but
// none of the files it names, and ends up guessing (observed: an invented
// `iapcli projects list -o json`). resilientSource therefore serves any file
// inside the skill directory; the only guarantee that matters — nothing
// OUTSIDE the skill is reachable — is kept by cleanResourcePath.

// cleanResourcePath returns resourcePath cleaned and relative to the skill
// root, or false when it is empty, absolute, the skill root itself, or climbs
// out of it ("..").
func cleanResourcePath(resourcePath string) (string, bool) {
	if resourcePath == "" || strings.HasPrefix(resourcePath, "/") || strings.Contains(resourcePath, `\`) {
		return "", false
	}
	c := path.Clean(resourcePath)
	if c == "." || !fs.ValidPath(c) {
		return "", false
	}
	return c, true
}

// validSkillDir reports an error unless name is a single path segment naming a
// loadable skill.
func (r resilientSource) validSkillDir(ctx context.Context, name string) error {
	if name == "" || strings.ContainsAny(name, `/\`) || !fs.ValidPath(name) {
		return fmt.Errorf("%w: %q", skill.ErrInvalidSkillName, name)
	}
	_, err := r.Source.LoadFrontmatter(ctx, name)
	return err
}

// LoadResource opens any file inside the named skill's directory.
func (r resilientSource) LoadResource(ctx context.Context, name, resourcePath string) (io.ReadCloser, error) {
	if err := r.validSkillDir(ctx, name); err != nil {
		return nil, err
	}
	rel, ok := cleanResourcePath(resourcePath)
	if !ok {
		return nil, fmt.Errorf("%w: %q must be a relative path inside the skill directory", skill.ErrInvalidResourcePath, resourcePath)
	}
	f, err := r.fsys.Open(path.Join(name, rel))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w: %q", skill.ErrResourceNotFound, rel)
		}
		return nil, fmt.Errorf("open resource %q of skill %q: %w", rel, name, err)
	}
	if st, serr := f.Stat(); serr == nil && st.IsDir() {
		_ = f.Close()
		return nil, fmt.Errorf("%w: %q is a directory", skill.ErrInvalidResourcePath, rel)
	}
	return f, nil
}

// ListResources lists every file of the skill under subpath ("" or "." = the
// whole skill), relative to the skill root, excluding SKILL.md itself.
func (r resilientSource) ListResources(ctx context.Context, name, subpath string) ([]string, error) {
	if err := r.validSkillDir(ctx, name); err != nil {
		return nil, err
	}
	start := "."
	if subpath != "" && subpath != "." {
		rel, ok := cleanResourcePath(subpath)
		if !ok {
			return nil, fmt.Errorf("%w: %q must be a relative path inside the skill directory", skill.ErrInvalidResourcePath, subpath)
		}
		start = rel
	}
	skillFS, err := fs.Sub(r.fsys, name)
	if err != nil {
		return nil, fmt.Errorf("sub-filesystem for skill %q: %w", name, err)
	}
	if _, err := fs.Stat(skillFS, start); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w: %q", skill.ErrResourceNotFound, start)
		}
		return nil, fmt.Errorf("stat %q: %w", start, err)
	}
	var out []string
	err = fs.WalkDir(skillFS, start, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if !d.IsDir() && p != "SKILL.md" {
			out = append(out, p)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk %q of skill %q: %w", start, name, err)
	}
	return out, nil
}

// ── filteredSkillsFS ──────────────────────────────────────────────────────

// filteredSkillsFS wraps a base fs.FS and only exposes specific top-level
// skill directories. The root listing is restricted to the named skills;
// reads within allowed skill directories pass through unchanged.
type filteredSkillsFS struct {
	base    fs.FS
	allowed map[string]bool
}

func newFilteredSkillsFS(base fs.FS, skillNames []string) fs.FS {
	allowed := make(map[string]bool, len(skillNames))
	for _, n := range skillNames {
		n = strings.TrimSpace(n)
		if n != "" {
			allowed[n] = true
		}
	}
	return filteredSkillsFS{base: base, allowed: allowed}
}

func (f filteredSkillsFS) Open(name string) (fs.File, error) {
	clean := path.Clean(name)
	if clean == "." {
		return &filteredRootDir{base: f.base, allowed: f.allowed}, nil
	}
	// Only allow access inside a permitted top-level directory.
	top := clean
	if i := strings.IndexByte(clean, '/'); i >= 0 {
		top = clean[:i]
	}
	if !f.allowed[top] {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	return f.base.Open(name)
}

// filteredRootDir is the virtual root returned by filteredSkillsFS.Open(".").
type filteredRootDir struct {
	base    fs.FS
	allowed map[string]bool
	entries []fs.DirEntry
	pos     int
	loaded  bool
}

func (d *filteredRootDir) Stat() (fs.FileInfo, error) { return syntheticDirInfo("."), nil }
func (d *filteredRootDir) Read([]byte) (int, error) {
	return 0, &fs.PathError{Op: "read", Path: ".", Err: fs.ErrInvalid}
}
func (d *filteredRootDir) Close() error { return nil }

func (d *filteredRootDir) ReadDir(n int) ([]fs.DirEntry, error) {
	if !d.loaded {
		all, _ := fs.ReadDir(d.base, ".")
		for _, e := range all {
			if e.IsDir() && d.allowed[e.Name()] {
				d.entries = append(d.entries, e)
			}
		}
		d.loaded = true
	}
	if n <= 0 {
		return d.entries[d.pos:], nil
	}
	if d.pos >= len(d.entries) {
		return nil, io.EOF
	}
	end := d.pos + n
	if end > len(d.entries) {
		end = len(d.entries)
	}
	res := d.entries[d.pos:end]
	d.pos = end
	return res, nil
}

// ── multiDirSkillsFS ──────────────────────────────────────────────────────

// multiDirSkillsFS merges several skill directories into a single fs.FS.
// The first directory (in the slice order) wins for duplicate skill names,
// matching the high-to-low precedence contract of the config search chain.
type multiDirSkillsFS struct {
	bases []fs.FS
}

// newMultiDirSkillsFS builds an FS from the subset of dirs that actually exist.
func newMultiDirSkillsFS(dirs []string) fs.FS {
	var bases []fs.FS
	for _, d := range dirs {
		if st, err := os.Stat(d); err == nil && st.IsDir() {
			bases = append(bases, os.DirFS(d))
		}
	}
	if len(bases) == 0 {
		return multiDirSkillsFS{bases: nil}
	}
	return multiDirSkillsFS{bases: bases}
}

func (m multiDirSkillsFS) Open(name string) (fs.File, error) {
	clean := path.Clean(name)
	if clean == "." {
		return &multiDirRoot{bases: m.bases}, nil
	}
	// Determine the top-level skill directory name so we can route to the
	// base that actually owns it.
	top := clean
	if i := strings.IndexByte(clean, '/'); i >= 0 {
		top = clean[:i]
	}
	for _, base := range m.bases {
		if _, err := base.Open(top); err == nil {
			return base.Open(name)
		}
	}
	return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
}

// multiDirRoot is the virtual root directory for multiDirSkillsFS.
// It lists every skill from all bases, with first-occurrence-wins deduplication.
type multiDirRoot struct {
	bases   []fs.FS
	entries []fs.DirEntry
	pos     int
	loaded  bool
}

func (d *multiDirRoot) Stat() (fs.FileInfo, error) { return syntheticDirInfo("."), nil }
func (d *multiDirRoot) Read([]byte) (int, error) {
	return 0, &fs.PathError{Op: "read", Path: ".", Err: fs.ErrInvalid}
}
func (d *multiDirRoot) Close() error { return nil }

func (d *multiDirRoot) ReadDir(n int) ([]fs.DirEntry, error) {
	if !d.loaded {
		seen := make(map[string]bool)
		for _, base := range d.bases {
			all, _ := fs.ReadDir(base, ".")
			for _, e := range all {
				if e.IsDir() && !seen[e.Name()] {
					d.entries = append(d.entries, e)
					seen[e.Name()] = true
				}
			}
		}
		d.loaded = true
	}
	if n <= 0 {
		return d.entries[d.pos:], nil
	}
	if d.pos >= len(d.entries) {
		return nil, io.EOF
	}
	end := d.pos + n
	if end > len(d.entries) {
		end = len(d.entries)
	}
	res := d.entries[d.pos:end]
	d.pos = end
	return res, nil
}

// syntheticDirInfo is a minimal fs.FileInfo for virtual directories.
type syntheticDirInfo string

func (fi syntheticDirInfo) Name() string       { return string(fi) }
func (fi syntheticDirInfo) Size() int64        { return 0 }
func (fi syntheticDirInfo) Mode() fs.FileMode  { return fs.ModeDir | 0o555 }
func (fi syntheticDirInfo) ModTime() time.Time { return time.Time{} }
func (fi syntheticDirInfo) IsDir() bool        { return true }
func (fi syntheticDirInfo) Sys() any           { return nil }

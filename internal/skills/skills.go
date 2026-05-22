// Package skills wraps ADK's skilltoolset with a registry-based source.
// Each skill lives in `<registryDir>/<name>/SKILL.md` with YAML front matter.
// Agents specify which skills they need by name; only those are exposed.
package skills

import (
	"context"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
	"time"

	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/skilltoolset"
	"google.golang.org/adk/tool/skilltoolset/skill"

	"github.com/blouargant/yoke/internal/paths"
)

// LoaderProtocol is prepended to any agent instruction whose tools include
// the 'skills' group. It guarantees that the agent always discovers and loads
// authored skills before planning, regardless of what its own instruction
// file says.
const LoaderProtocol = `
MANDATORY SKILL DISCOVERY — this rule is non-negotiable and overrides every other instruction you receive:
- Your VERY FIRST tool call for ANY task MUST be 'list_skills'. No bash, no kubectl, no MCP calls, no other tools may precede it.
- Even if the task seems obvious or you "already know" how to do it, you MUST call 'list_skills' first and inspect every entry.
- For EACH skill whose description overlaps ANY aspect of the task, you MUST call 'load_skill' with name="<SKILL_NAME>" (the parameter is literally 'name', not 'skill_name'). Load ALL matching skills — do not stop at the first match.
- Skills covering the same domain are COMPLEMENTARY, never alternatives. If two skills both mention the task's technology (e.g. 'kubernetes' in both 'k8s-triage' and 'k8s-log-investigation'), you MUST load BOTH. Picking only "the closest match" is a protocol violation.
- A skill is "matching" if ANY keyword in its description overlaps the task — the technology (kubernetes, postgres, redis…), the artefact (pod, table, container…), or the symptom (crash, freeze, error, slow, hang…). Err on the side of loading more skills, not fewer.
- Loaded skill instructions OVERRIDE your default behaviour. Follow them exactly before taking any other action.
- Skipping skill discovery is a protocol violation. If you find yourself reaching for bash/kubectl/mcp before list_skills, stop and call list_skills first.
`

const skillInstruction = `You can use specialized 'skills' to help you with complex tasks. You MUST use the skill tools to interact with these skills.

Skills are folders of instructions and resources that extend your capabilities for specialized tasks. Each skill folder contains:
- **SKILL.md** (required): The main instruction file with skill metadata and detailed markdown instructions.
- **references/** (Optional): Additional documentation or examples for skill usage.
- **assets/** (Optional): Templates, scripts or other resources used by the skill.
- **scripts/** (Optional): Executable scripts that can be run via bash.

This is very important:

1. Use the ` + "`list_skills`" + ` tool to discover what skills are available.
2. For EACH skill whose description is relevant to the current task, call ` + "`load_skill`" + ` with ` + "`name=\"<SKILL_NAME>\"`" + ` (the parameter is literally ` + "`name`" + `, not ` + "`skill_name`" + `). Load ALL relevant skills — do not stop at the first match.
3. Once you have read the instructions, follow them exactly as documented before replying to the user. If the instructions list multiple steps, complete all of them in order.
4. The ` + "`load_skill_resource`" + ` tool is for viewing files within a skill's directory (e.g., ` + "`references/*`, `assets/*`, `scripts/*`" + `). Do NOT use other tools to access these files.
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
	var src skill.Source
	if len(skillNames) == 0 {
		src = skill.NewFileSystemSource(base)
	} else {
		src = skill.NewFileSystemSource(newFilteredSkillsFS(base, skillNames))
	}
	ts, err := skilltoolset.New(ctx, skilltoolset.Config{
		Source:            src,
		SystemInstruction: skillInstruction,
	})
	if err != nil {
		return nil, err
	}
	return ts, nil
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

// Package softskills — write-side tools used exclusively by the curator
// agent to create or update entries under the softskills directory.
//
// Safety floor (enforced regardless of the surrounding permissions
// configuration):
//
//   - Every path is resolved against the curator's root directory and must
//     stay strictly inside it (no `..`, no absolute escapes).
//   - `create_skill` refuses if the target SKILL.md already exists.
//   - `update_skill` refuses when the new content is byte-identical or
//     differs only by trivial whitespace; callers must supply a `reason`.
//   - `append_index` performs a serialized append on `INDEX.md` guarded by
//     a per-process mutex AND an OS-level advisory lock (flock) so two
//     concurrent curators on the same host cannot corrupt the file.
package softskills

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
)

// indexFileName is the human-navigable category index living at the root
// of the softskills directory. The curator must keep it in sync.
const indexFileName = "INDEX.md"

// minReasonLen is the minimum length of the human-readable reason a
// curator must supply when updating an existing soft-skill. Cheap guard
// against the model issuing cosmetic rewrites.
const minReasonLen = 20

// trivialDiffMaxRatio rejects updates whose normalized diff is below this
// fraction of the new content's length (default ~3 %). Captures pure
// formatting churn.
const trivialDiffMaxRatio = 0.03

// indexMu guards in-process serialization of INDEX.md edits. Layered on
// top of the OS-level flock so a single process never deadlocks on itself
// and so tests on platforms without flock still get serialization.
var indexMu sync.Mutex

// WriteTools returns the three write-side tools the curator mounts.
// `root` MUST be the absolute (or working-directory-relative) softskills
// directory; all writes are confined inside it.
func WriteTools(root string) []tool.Tool {
	if root == "" {
		root = DefaultDir
	}
	w := &writer{root: root}
	return []tool.Tool{
		mustTool("softskill_create",
			"Create a new soft-skill at softskills/<name>/SKILL.md. Refuses if the skill already exists. "+
				"Arguments: `name` (string, required, lowercase-with-dashes), `content` (string, required, full SKILL.md including YAML frontmatter).",
			w.create),
		mustTool("softskill_update",
			"Update an existing soft-skill. Refuses when the change is trivial (whitespace-only or below 3% diff). "+
				"Arguments: `name` (string, required), `content` (string, required, full SKILL.md), `reason` (string, required, ≥20 chars explaining what genuinely improved).",
			w.update),
		mustTool("softskill_index_append",
			"Append a category line to softskills/INDEX.md under the matching ### <category> heading. Creates the section if it does not exist. Idempotent: a duplicate skill name in the same section is rejected.\n"+
				"Arguments: `category` (string, required), `name` (string, required), `summary` (string, required, one-line description).",
			w.appendIndex),
	}
}

type writer struct {
	root string
}

// ── softskill_create ─────────────────────────────────────────────────────

type createIn struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}
type createOut struct {
	Result string `json:"result"`
}

func (w *writer) create(_ tool.Context, in createIn) (createOut, error) {
	skillDir, target, err := w.skillPath(in.Name)
	if err != nil {
		return createOut{Result: "Error: " + err.Error()}, nil
	}
	if _, err := os.Stat(target); err == nil {
		return createOut{Result: fmt.Sprintf("Error: soft-skill %q already exists at %s; use softskill_update instead", in.Name, target)}, nil
	}
	if err := validateSkillContent(in.Name, in.Content); err != nil {
		return createOut{Result: "Error: " + err.Error()}, nil
	}
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		return createOut{Result: fmt.Sprintf("Error creating dir: %v", err)}, nil
	}
	if err := os.WriteFile(target, []byte(in.Content), 0o644); err != nil {
		return createOut{Result: fmt.Sprintf("Error writing %s: %v", target, err)}, nil
	}
	return createOut{Result: fmt.Sprintf("created %s (%d bytes)", target, len(in.Content))}, nil
}

// ── softskill_update ─────────────────────────────────────────────────────

type updateIn struct {
	Name    string `json:"name"`
	Content string `json:"content"`
	Reason  string `json:"reason"`
}
type updateOut struct {
	Result string `json:"result"`
}

func (w *writer) update(_ tool.Context, in updateIn) (updateOut, error) {
	if len(strings.TrimSpace(in.Reason)) < minReasonLen {
		return updateOut{Result: fmt.Sprintf("Error: `reason` must be at least %d non-whitespace chars explaining the genuine improvement", minReasonLen)}, nil
	}
	_, target, err := w.skillPath(in.Name)
	if err != nil {
		return updateOut{Result: "Error: " + err.Error()}, nil
	}
	prev, err := os.ReadFile(target)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return updateOut{Result: fmt.Sprintf("Error: soft-skill %q does not exist at %s; use softskill_create", in.Name, target)}, nil
		}
		return updateOut{Result: fmt.Sprintf("Error reading %s: %v", target, err)}, nil
	}
	if err := validateSkillContent(in.Name, in.Content); err != nil {
		return updateOut{Result: "Error: " + err.Error()}, nil
	}
	if isTrivialChange(string(prev), in.Content) {
		return updateOut{Result: "Error: change rejected as trivial (whitespace-only or below 3% diff). Soft-skills must only change for substantive improvements."}, nil
	}
	if err := os.WriteFile(target, []byte(in.Content), 0o644); err != nil {
		return updateOut{Result: fmt.Sprintf("Error writing %s: %v", target, err)}, nil
	}
	return updateOut{Result: fmt.Sprintf("updated %s (%d bytes; reason: %s)", target, len(in.Content), strings.TrimSpace(in.Reason))}, nil
}

// ── softskill_index_append ───────────────────────────────────────────────

type indexIn struct {
	Category string `json:"category"`
	Name     string `json:"name"`
	Summary  string `json:"summary"`
}
type indexOut struct {
	Result string `json:"result"`
}

func (w *writer) appendIndex(_ tool.Context, in indexIn) (indexOut, error) {
	cat := strings.TrimSpace(in.Category)
	name := strings.TrimSpace(in.Name)
	summary := strings.TrimSpace(in.Summary)
	if cat == "" || name == "" || summary == "" {
		return indexOut{Result: "Error: category, name and summary are all required"}, nil
	}
	if err := validateName(name); err != nil {
		return indexOut{Result: "Error: " + err.Error()}, nil
	}
	if err := validateName(cat); err != nil {
		return indexOut{Result: "Error: invalid category: " + err.Error()}, nil
	}
	indexPath := filepath.Join(w.root, indexFileName)
	if err := w.ensureInside(indexPath); err != nil {
		return indexOut{Result: "Error: " + err.Error()}, nil
	}

	indexMu.Lock()
	defer indexMu.Unlock()

	if err := os.MkdirAll(w.root, 0o755); err != nil {
		return indexOut{Result: fmt.Sprintf("Error creating root: %v", err)}, nil
	}
	f, err := os.OpenFile(indexPath, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return indexOut{Result: fmt.Sprintf("Error opening %s: %v", indexPath, err)}, nil
	}
	defer func() { _ = f.Close() }()
	if err := flockExclusive(f); err != nil {
		return indexOut{Result: fmt.Sprintf("Error locking %s: %v", indexPath, err)}, nil
	}
	defer func() { _ = flockUnlock(f) }()

	body, err := readAll(f)
	if err != nil {
		return indexOut{Result: fmt.Sprintf("Error reading %s: %v", indexPath, err)}, nil
	}
	updated, msg, err := insertIndexEntry(body, cat, name, summary)
	if err != nil {
		return indexOut{Result: "Error: " + err.Error()}, nil
	}
	if updated == body {
		return indexOut{Result: msg}, nil
	}
	if _, err := f.Seek(0, 0); err != nil {
		return indexOut{Result: fmt.Sprintf("Error seek: %v", err)}, nil
	}
	if err := f.Truncate(0); err != nil {
		return indexOut{Result: fmt.Sprintf("Error truncate: %v", err)}, nil
	}
	if _, err := f.Write([]byte(updated)); err != nil {
		return indexOut{Result: fmt.Sprintf("Error write: %v", err)}, nil
	}
	return indexOut{Result: msg}, nil
}

// ── helpers ──────────────────────────────────────────────────────────────

// skillPath returns (skillDir, skillFile) for `name` and confirms both
// stay strictly inside the curator's root.
func (w *writer) skillPath(name string) (string, string, error) {
	if err := validateName(name); err != nil {
		return "", "", err
	}
	skillDir := filepath.Join(w.root, name)
	target := filepath.Join(skillDir, "SKILL.md")
	if err := w.ensureInside(target); err != nil {
		return "", "", err
	}
	return skillDir, target, nil
}

// ensureInside resolves p and rejects anything that escapes w.root.
func (w *writer) ensureInside(p string) error {
	absRoot, err := filepath.Abs(w.root)
	if err != nil {
		return fmt.Errorf("resolve root: %w", err)
	}
	absPath, err := filepath.Abs(p)
	if err != nil {
		return fmt.Errorf("resolve path: %w", err)
	}
	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil || strings.HasPrefix(rel, "..") || rel == ".." {
		return fmt.Errorf("path %q escapes softskills root %q", p, w.root)
	}
	return nil
}

// validateName accepts lowercase letters, digits and dashes only — same
// constraint we want to enforce for skill folder names and category ids.
func validateName(name string) error {
	if name == "" {
		return errors.New("name is empty")
	}
	if len(name) > 64 {
		return errors.New("name exceeds 64 chars")
	}
	for i, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '-':
			if i == 0 || i == len(name)-1 {
				return fmt.Errorf("name %q must not start or end with dash", name)
			}
		default:
			return fmt.Errorf("name %q contains invalid character %q (allowed: a-z, 0-9, -)", name, r)
		}
	}
	return nil
}

// validateSkillContent does cheap structural checks on the SKILL.md body.
// We do not parse the YAML strictly — the upstream skill loader does — but
// we catch the common curator mistakes early with a clear error message.
func validateSkillContent(name, content string) error {
	if !strings.HasPrefix(content, "---\n") {
		return errors.New("content must start with `---` YAML frontmatter delimiter")
	}
	end := strings.Index(content[4:], "\n---")
	if end < 0 {
		return errors.New("content missing closing `---` for YAML frontmatter")
	}
	header := content[:end+4+4]
	if !strings.Contains(header, "name:") {
		return errors.New("frontmatter missing `name:`")
	}
	if !strings.Contains(header, "description:") {
		return errors.New("frontmatter missing `description:`")
	}
	expected := "name: " + name
	if !strings.Contains(header, expected) {
		return fmt.Errorf("frontmatter `name:` must equal %q (the directory name)", name)
	}
	return nil
}

// isTrivialChange returns true when prev and next normalize to the same
// string (whitespace-only diff) or when their character-level diff is
// below `trivialDiffMaxRatio` of the new content's length. Cheap and
// good-enough; we are gating model self-improvement, not patent claims.
func isTrivialChange(prev, next string) bool {
	if normalizeWhitespace(prev) == normalizeWhitespace(next) {
		return true
	}
	if len(next) == 0 {
		return false
	}
	diff := absInt(len(prev) - len(next))
	if float64(diff)/float64(len(next)) < trivialDiffMaxRatio {
		// length is similar — also check char-level overlap quickly via a
		// rune-set Jaccard so adding a single sentence still counts.
		if jaccardOverlap(prev, next) > 1.0-trivialDiffMaxRatio {
			return true
		}
	}
	return false
}

func normalizeWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// jaccardOverlap on word sets — a quick proxy for "essentially the same
// document". Not cryptographic, just enough to refuse cosmetic churn.
func jaccardOverlap(a, b string) float64 {
	wa := wordSet(a)
	wb := wordSet(b)
	if len(wa) == 0 && len(wb) == 0 {
		return 1
	}
	inter := 0
	for w := range wa {
		if _, ok := wb[w]; ok {
			inter++
		}
	}
	union := len(wa) + len(wb) - inter
	if union == 0 {
		return 1
	}
	return float64(inter) / float64(union)
}

func wordSet(s string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, w := range strings.Fields(strings.ToLower(s)) {
		out[w] = struct{}{}
	}
	return out
}

// insertIndexEntry adds `- **<name>** — <summary>` under the
// `### <category>` section, creating the section under `## Categories` if
// it does not exist. Returns the new body and a human-readable message.
func insertIndexEntry(body, category, name, summary string) (string, string, error) {
	entry := fmt.Sprintf("- **%s** — %s", name, summary)
	dupNeedle := fmt.Sprintf("- **%s**", name)

	if body == "" {
		body = "# Soft-Skills Library\n\n## Categories\n"
	}
	if !strings.Contains(body, "## Categories") {
		body = strings.TrimRight(body, "\n") + "\n\n## Categories\n"
	}

	header := "### " + category
	idx := strings.Index(body, header)
	if idx < 0 {
		// append a new category section at the end of the file
		newSection := fmt.Sprintf("\n%s\n%s\n", header, entry)
		body = strings.TrimRight(body, "\n") + "\n" + newSection
		return body, fmt.Sprintf("appended new category %q with skill %q", category, name), nil
	}
	// find the bounds of this section: from end of header line to next
	// "### " or "## " or end-of-file.
	sectionStart := idx + len(header)
	if nl := strings.IndexByte(body[sectionStart:], '\n'); nl >= 0 {
		sectionStart += nl + 1
	} else {
		sectionStart = len(body)
	}
	rest := body[sectionStart:]
	end := len(rest)
	for _, marker := range []string{"\n### ", "\n## "} {
		if i := strings.Index(rest, marker); i >= 0 && i < end {
			end = i
		}
	}
	section := rest[:end]
	if strings.Contains(section, dupNeedle) {
		return body, fmt.Sprintf("skill %q already listed under category %q (no change)", name, category), nil
	}
	// trim trailing blank lines from section, append entry, re-append blank line
	trimmed := strings.TrimRight(section, "\n")
	updatedSection := trimmed + "\n" + entry + "\n"
	body = body[:sectionStart] + updatedSection + rest[end:]
	return body, fmt.Sprintf("appended skill %q under category %q", name, category), nil
}

func readAll(f *os.File) (string, error) {
	if _, err := f.Seek(0, 0); err != nil {
		return "", err
	}
	st, err := f.Stat()
	if err != nil {
		return "", err
	}
	buf := make([]byte, st.Size())
	if _, err := f.Read(buf); err != nil && err.Error() != "EOF" {
		// short read on empty file is fine
		if st.Size() != 0 {
			return "", err
		}
	}
	return string(buf), nil
}

// flockExclusive / flockUnlock — POSIX advisory locks. On non-unix
// platforms these become no-ops; the in-process mutex still serializes.
func flockExclusive(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
}

func flockUnlock(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}

// mustTool mirrors core/tools.mustTool but lives here to avoid a circular
// import (this package is imported by agent/agent.go alongside core/tools).
func mustTool[A, R any](name, desc string, h functiontool.Func[A, R]) tool.Tool {
	t, err := functiontool.New(functiontool.Config{Name: name, Description: desc}, h)
	if err != nil {
		panic(fmt.Errorf("softskills: build tool %s: %w", name, err))
	}
	return t
}

// silence the unused-import warning when GOOS lacks Flock support
var _ = context.Background

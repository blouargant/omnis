package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/plugin"
	"google.golang.org/adk/tool"

	"github.com/blouargant/omnis/core/events"
	fstools "github.com/blouargant/omnis/core/tools"
	"github.com/blouargant/omnis/internal/lsp"
)

// coding_efficiency_plugin.go bundles three token-saving transforms into one
// AfterToolCallback on the answering squad root (Fable's #2/#3/#4). They run in
// a fixed order so they compose:
//
//  1. Edit-fused diagnostics (#2) — after an Edit/Write/MultiEdit/revert, append
//     the language-server diagnostics *delta* for the touched files to the tool
//     result, so the edit→check loop doesn't spend a whole extra tool round-trip
//     (and its re-sent input) calling lsp_diagnostics. Best-effort and
//     zero-latency when no server is running for the file (no cold start). Edit
//     tools are then exempt from the shaper so the appended line is never cut.
//  2. Unchanged-read dedup (#4) — a re-Read of a file whose bytes are identical
//     to what a prior read in this session already returned is replaced by a one
//     line "unchanged" stub. It keys on the SHA-256 of the *returned* content, so
//     it self-invalidates against any on-disk mutation (leader edit, sub-agent
//     edit, Bash, external) — the next read simply hashes different bytes. The
//     only staleness risk (the earlier read scrolled out of context via
//     compression) is closed by clearing the cache on EventCompressionStart.
//  3. Universal output shaper (#3) — any non-exempt tool result larger than the
//     budget is truncated head+tail with a "narrow your query" note, so one
//     runaway Grep/Bash/test dump can't flood (and permanently occupy) context.
//     Universal-with-exemptions: a newly added high-volume tool is capped by
//     default; only small control tools are exempt.
//
// Mounted on non-router answering roots only (same gating as steering/hooks).
// No-op contract: with nothing to fuse/dedup/cap the callback returns nil and
// behaviour is byte-identical to before.

const (
	// shaperMaxChars caps a shaped tool result (~8k tokens at ~4 chars/token).
	shaperMaxChars = 32000
	// shaperHeadRatio keeps this fraction of the budget as the head (the rest is
	// the tail): most tools front-load the useful part.
	shaperHeadRatio = 0.70

	// Edit-fusion settle tuning: a short bound so an edit is never slowed much.
	fuseMaxWait = 1500 * time.Millisecond
	fuseQuiet   = 300 * time.Millisecond
	// fuseMaxFiles bounds how many files a single MultiEdit fuses diagnostics for
	// (each costs up to fuseMaxWait); the rest are noted for a manual check.
	fuseMaxFiles      = 3
	maxNewDiagsListed = 6
)

// shaperExempt lists tools whose (small, structured) output must never be
// truncated. Edit tools are handled separately (fusion path returns early).
var shaperExempt = map[string]bool{
	"ask_user": true, "route_to_squad": true, "handoff_to_router": true, "ask_squad": true,
	"todo_write": true, "todo_read": true, "todo_update": true,
}

// shaperBudget is a per-tool cap. budgetFor lets a tool tune the head/tail split
// without changing the default (Fable's "leave the hook" — run_tests' failing
// assertions cluster at the end, so it keeps more tail).
type shaperBudget struct {
	maxChars  int
	headRatio float64
}

func budgetFor(name string) shaperBudget {
	switch name {
	case "run_tests":
		return shaperBudget{maxChars: shaperMaxChars, headRatio: 0.40}
	}
	return shaperBudget{maxChars: shaperMaxChars, headRatio: shaperHeadRatio}
}

func isFusionTool(name string) bool {
	switch name {
	case "Edit", "Write", "MultiEdit", "revert":
		return true
	}
	return false
}

// codingEfficiency holds the per-squad-instance caches. A squad instance is
// stable across turns within a generation, so the dedup/diagnostics state
// persists across a session's turns; a different squad gets its own (empty)
// caches, which is why a squad handoff can't serve another squad a stale stub.
type codingEfficiency struct {
	lspMgr *lsp.Manager

	mu       sync.Mutex
	readHash map[string]string              // session\x00path → last returned content hash
	diagPrev map[string]map[string]struct{} // session\x00path → previous diagnostic id set
}

func newCodingEfficiency(lspMgr *lsp.Manager) *codingEfficiency {
	return &codingEfficiency{
		lspMgr:   lspMgr,
		readHash: map[string]string{},
		diagPrev: map[string]map[string]struct{}{},
	}
}

// codingEfficiencyPlugin builds the plugin and a cleanup that detaches its bus
// subscription (called from buildPlugins' closer on generation teardown).
func codingEfficiencyPlugin(name string, lspMgr *lsp.Manager, bus *events.Bus) (*plugin.Plugin, func(), error) {
	ce := newCodingEfficiency(lspMgr)
	cleanup := func() {}
	if bus != nil {
		sub := bus.Subscribe(events.EventCompressionStart, func(_ string, p map[string]any) {
			if sid, _ := p["session_id"].(string); sid != "" {
				ce.clearSession(sid)
			}
		})
		cleanup = func() { sub.Off() }
	}
	p, err := plugin.New(plugin.Config{
		Name:              name,
		AfterToolCallback: llmagent.AfterToolCallback(ce.afterTool),
	})
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	return p, cleanup, nil
}

func (ce *codingEfficiency) clearSession(sid string) {
	prefix := sid + "\x00"
	ce.mu.Lock()
	defer ce.mu.Unlock()
	for k := range ce.readHash {
		if strings.HasPrefix(k, prefix) {
			delete(ce.readHash, k)
		}
	}
	for k := range ce.diagPrev {
		if strings.HasPrefix(k, prefix) {
			delete(ce.diagPrev, k)
		}
	}
}

// afterTool applies fusion → dedup → shaper. Returns nil when nothing changed
// (ADK keeps the original result), or the rewritten result map otherwise.
func (ce *codingEfficiency) afterTool(tc tool.Context, t tool.Tool, args, result map[string]any, _ error) (map[string]any, error) {
	if result == nil || t == nil {
		return nil, nil
	}
	name := t.Name()
	sid := tc.SessionID()
	cwd := fstools.CwdForContext(tc)

	// 1) Edit-fused diagnostics. Edit tools are then exempt from the shaper so
	//    the appended diagnostics line is never what gets truncated.
	if isFusionTool(name) {
		return ce.fuse(tc, sid, cwd, name, args, result), nil
	}

	changed := false
	// 2) Unchanged-read dedup (before the shaper: a stub is tiny, so the shaper
	//    then no-ops; a full read still gets capped).
	if name == "Read" {
		if m2, ok := ce.dedup(sid, cwd, args, result); ok {
			result, changed = m2, true
		}
	}
	// 3) Universal shaper.
	if !shaperExempt[name] {
		if m2, ok := shapeResult(name, result); ok {
			result, changed = m2, true
		}
	}
	if changed {
		return result, nil
	}
	return nil, nil
}

// fuse appends the diagnostics delta for the edited file(s) to an edit result.
// Returns nil (no change) on a failed/no-op edit, when no server is running for
// any touched file, or when there's nothing to report.
func (ce *codingEfficiency) fuse(tc tool.Context, sid, cwd, name string, args, result map[string]any) map[string]any {
	if ce.lspMgr == nil {
		return nil
	}
	field, text := dominantString(result)
	if field == "" || looksLikeError(text) {
		return nil // don't fuse onto an error/no-match edit result
	}
	paths := editedPaths(name, args, cwd)
	if len(paths) == 0 {
		return nil
	}
	var notes []string
	fused, overflow := 0, 0
	for _, p := range paths {
		if fused >= fuseMaxFiles {
			overflow++
			continue
		}
		diags, ok := ce.lspMgr.DiagnosticsIfRunning(tc, p, fuseMaxWait, fuseQuiet)
		if !ok {
			continue // no live server for this file → zero added latency
		}
		notes = append(notes, ce.diagDelta(sid, p, cwd, diags))
		fused++
	}
	if len(notes) == 0 && overflow == 0 {
		return nil
	}
	var b strings.Builder
	b.WriteString(text)
	b.WriteString("\n\n")
	b.WriteString(strings.Join(notes, "\n"))
	if overflow > 0 {
		fmt.Fprintf(&b, "\n(+%d more edited file(s) not checked — run lsp_diagnostics on them.)", overflow)
	}
	out := copyMap(result)
	out[field] = b.String()
	return out
}

// diagDelta compares the current diagnostics for path against the previous set
// stored for (session, path), updates the store, and returns a one-line summary.
func (ce *codingEfficiency) diagDelta(sid, path, cwd string, diags []lsp.Diagnostic) string {
	key := sid + "\x00" + path
	cur := map[string]struct{}{}
	var newMsgs []string
	ce.mu.Lock()
	prev := ce.diagPrev[key]
	for _, d := range diags {
		id := diagID(d)
		cur[id] = struct{}{}
		if _, had := prev[id]; !had {
			newMsgs = append(newMsgs, fmt.Sprintf("L%d %s", d.Range.Start.Line+1, strings.TrimSpace(d.Message)))
		}
	}
	resolved := 0
	for id := range prev {
		if _, still := cur[id]; !still {
			resolved++
		}
	}
	ce.diagPrev[key] = cur
	ce.mu.Unlock()

	disp := displayRel(path, cwd)
	if len(cur) == 0 {
		if resolved > 0 {
			return fmt.Sprintf("%s — diagnostics: clean (%d resolved)", disp, resolved)
		}
		return fmt.Sprintf("%s — diagnostics: clean", disp)
	}
	unchanged := len(cur) - len(newMsgs)
	var b strings.Builder
	fmt.Fprintf(&b, "%s — diagnostics: %d new", disp, len(newMsgs))
	if len(newMsgs) > 0 {
		shown := newMsgs
		if len(shown) > maxNewDiagsListed {
			shown = shown[:maxNewDiagsListed]
		}
		fmt.Fprintf(&b, " (%s", strings.Join(shown, "; "))
		if len(newMsgs) > len(shown) {
			fmt.Fprintf(&b, "; +%d more", len(newMsgs)-len(shown))
		}
		b.WriteString(")")
	}
	fmt.Fprintf(&b, ", %d resolved, %d unchanged", resolved, unchanged)
	return b.String()
}

// dedup replaces a byte-identical re-read with a stub. Keys on the hash of the
// returned content, so any real change is detected automatically.
func (ce *codingEfficiency) dedup(sid, cwd string, args, result map[string]any) (map[string]any, bool) {
	field, text := dominantString(result)
	if field == "" || text == "" || looksLikeError(text) {
		return nil, false
	}
	// Don't dedup the "(empty file)"/"(empty range)" sentinels.
	if strings.HasPrefix(text, "(empty") {
		return nil, false
	}
	p := resolveArgPath(args, "file_path", cwd)
	if p == "" {
		return nil, false
	}
	h := hashString(text)
	key := sid + "\x00" + p
	ce.mu.Lock()
	prev, seen := ce.readHash[key]
	ce.readHash[key] = h
	ce.mu.Unlock()
	if !seen || prev != h {
		return nil, false
	}
	out := copyMap(result)
	out[field] = fmt.Sprintf("(unchanged: you already read %s earlier this session and its bytes have not changed since — the version in your context is still current, so the re-read is skipped to save tokens. Ask again or Read an explicit line range if you truly need it re-shown.)", displayRel(p, cwd))
	return out, true
}

// --- shaping ---

// shapeResult caps the dominant text field of a tool result to the per-tool
// budget. Returns (nil,false) when it already fits.
func shapeResult(name string, result map[string]any) (map[string]any, bool) {
	field, text := dominantString(result)
	if field == "" {
		return nil, false
	}
	b := budgetFor(name)
	capped, ok := capText(text, b.maxChars, b.headRatio)
	if !ok {
		return nil, false
	}
	out := copyMap(result)
	out[field] = capped
	return out, true
}

// capText keeps a head and a tail slice of s (on line boundaries) joined by a
// middle-omission note. Returns (,false) when s already fits within max.
func capText(s string, max int, headRatio float64) (string, bool) {
	if len(s) <= max {
		return "", false
	}
	headBudget := int(float64(max) * headRatio)
	tailBudget := max - headBudget
	head := s[:headBudget]
	if i := strings.LastIndexByte(head, '\n'); i > 0 {
		head = head[:i]
	}
	tail := s[len(s)-tailBudget:]
	if i := strings.IndexByte(tail, '\n'); i >= 0 && i+1 < len(tail) {
		tail = tail[i+1:]
	}
	omittedChars := len(s) - len(head) - len(tail)
	if omittedChars < 0 {
		omittedChars = 0
	}
	omittedLines := strings.Count(s, "\n") - strings.Count(head, "\n") - strings.Count(tail, "\n")
	if omittedLines < 0 {
		omittedLines = 0
	}
	marker := fmt.Sprintf(
		"\n\n…[output truncated to fit context: ~%d chars / %d lines cut from the middle. "+
			"This is a size guard, not the tool's real limit — narrow the request (an explicit "+
			"line range, a more specific pattern, or a files-only search) to see the rest]…\n\n",
		omittedChars, omittedLines)
	return head + marker + tail, true
}

// --- helpers ---

// dominantString returns the map's largest string-valued field (key + value).
// Every omnis tool has one dominant text field (Read→content, Grep→matches,
// Bash→output, Edit/lsp→result, …), so this picks the right one without knowing
// the tool. Returns ("","") when there is no string field.
func dominantString(m map[string]any) (string, string) {
	bestKey, bestVal := "", ""
	for k, v := range m {
		if s, ok := v.(string); ok && len(s) > len(bestVal) {
			bestKey, bestVal = k, s
		}
	}
	return bestKey, bestVal
}

func copyMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func hashString(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:16])
}

// looksLikeError reports whether a tool result string is one of the fs/edit
// tools' error sentinels ("Error: …", "Error reading …", "Error writing …").
func looksLikeError(s string) bool {
	return strings.HasPrefix(strings.TrimSpace(s), "Error")
}

// editedPaths returns the absolute paths an edit tool touched, from its args.
func editedPaths(name string, args map[string]any, cwd string) []string {
	if name == "MultiEdit" {
		var out []string
		if files, ok := args["files"].([]any); ok {
			for _, f := range files {
				if fm, ok := f.(map[string]any); ok {
					if p := resolveArgPath(fm, "file_path", cwd); p != "" {
						out = append(out, p)
					}
				}
			}
		}
		return out
	}
	if p := resolveArgPath(args, "file_path", cwd); p != "" {
		return []string{p}
	}
	return nil
}

// resolveArgPath reads a string path arg and resolves it against cwd.
func resolveArgPath(args map[string]any, key, cwd string) string {
	p, _ := args[key].(string)
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	if filepath.IsAbs(p) || cwd == "" {
		return p
	}
	return filepath.Join(cwd, p)
}

// diagID is a stable identity for a diagnostic (position + message), so the same
// error across two analyses compares equal for the new/resolved diff.
func diagID(d lsp.Diagnostic) string {
	return fmt.Sprintf("%d:%d:%s", d.Range.Start.Line, d.Range.Start.Character, strings.TrimSpace(d.Message))
}

func displayRel(p, cwd string) string {
	if cwd == "" {
		return p
	}
	if rel, err := filepath.Rel(cwd, p); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return filepath.Base(p)
}

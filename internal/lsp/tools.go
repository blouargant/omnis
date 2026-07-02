// tools.go — the agent-facing lsp_* tool group. Every tool takes name-based
// inputs (a file plus a symbol name, or a query) rather than line/character
// coordinates: the LLM is unreliable at positions and LSP columns are UTF-16
// offsets, so the manager resolves names to positions internally (see
// position.go / server.go). File paths are resolved against the session working
// directory, matching the fs tools. Results are compact text (file:line:col),
// and a missing/unconfigured server returns a clean "fall back to Grep/Read"
// message instead of an error, honouring the additive recall contract.
package lsp

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"

	fstools "github.com/blouargant/omnis/core/tools"
)

// toolTimeout bounds a single LSP tool call. It must comfortably exceed cold
// server start (initTimeout) so the first query on a big repo isn't cut short.
const toolTimeout = 90 * time.Second

// Diagnostics wait tuning: how long to wait for a publish after syncing the
// file, and the quiet window for follow-up publishes (empty-then-real).
const (
	diagMaxWait = 8 * time.Second
	diagQuiet   = 400 * time.Millisecond
)

// Tools returns the lsp_* tool set bound to manager m. Mounted by the "lsp"
// tool group (M6).
func Tools(m *Manager) []tool.Tool {
	lt := &lspTools{m: m}
	return []tool.Tool{
		lt.documentSymbols(),
		lt.readSymbol(),
		lt.workspaceSymbol(),
		lt.definition(),
		lt.references(),
		lt.hover(),
		lt.diagnostics(),
		lt.rename(),
		lt.codeAction(),
	}
}

type lspTools struct{ m *Manager }

type fileIn struct {
	File string `json:"file"`
}
type queryIn struct {
	Query string `json:"query"`
	File  string `json:"file,omitempty"`
}
type symbolIn struct {
	File   string `json:"file"`
	Symbol string `json:"symbol"`
}
type lspOut struct {
	Result string `json:"result"`
}

func (lt *lspTools) documentSymbols() tool.Tool {
	return newLSPTool("lsp_document_symbols",
		"Outline a source file's symbols (functions, types, methods, constants) via the language server — "+
			"precise and far cheaper than reading the whole file. "+
			"Arguments: `file` (string, required) — path to a source file, relative to the session directory or absolute.",
		func(ctx tool.Context, in fileIn) (lspOut, error) {
			qctx, cancel := toolCtx(ctx)
			defer cancel()
			path := resolveFile(ctx, in.File)
			ls, err := lt.m.ResolveServer(qctx, path)
			if err != nil {
				return lspOut{Result: resolveErrMsg(err)}, nil
			}
			syms, err := ls.DocumentSymbols(qctx, path)
			if err != nil {
				return lspOut{Result: "lsp: " + err.Error()}, nil
			}
			if len(syms) == 0 {
				return lspOut{Result: "(no symbols)"}, nil
			}
			return lspOut{Result: formatSymbols(syms)}, nil
		})
}

type readSymbolIn struct {
	Symbol string `json:"symbol"`
	File   string `json:"file,omitempty"`
}

func (lt *lspTools) readSymbol() tool.Tool {
	return newLSPTool("lsp_read_symbol",
		"Read the full source of ONE symbol — a function, method, type, or class — by name, instead of reading the "+
			"whole file to see it. Returns just that symbol's body (with its doc comment) and line numbers, so you spend "+
			"tokens on the 20 lines you need, not an 800-line file. Prefer this — or lsp_document_symbols for a file's "+
			"outline — as the default way to look at code; Read the whole file only when you genuinely need the surrounding "+
			"context. Arguments: `symbol` (string, required) — the symbol's name (a bare name like `Foo` or a qualified "+
			"one like `(*Client).Call`); `file` (string, optional) — the file it's declared in; omit `file` to let the "+
			"language server locate it project-wide.",
		func(ctx tool.Context, in readSymbolIn) (lspOut, error) {
			qctx, cancel := toolCtx(ctx)
			defer cancel()
			symbol := strings.TrimSpace(in.Symbol)
			if symbol == "" {
				return lspOut{Result: "lsp_read_symbol: `symbol` is required."}, nil
			}
			cwd := fstools.CwdForContext(ctx)

			// Resolve the declaring file: an explicit file wins; otherwise ask the
			// server's project-wide index where the symbol lives.
			path := ""
			if in.File != "" {
				path = resolveFile(ctx, in.File)
			}
			if path == "" {
				anchor := lt.anchorFile(ctx, "")
				if anchor == "" {
					return lspOut{Result: resolveErrMsg(ErrNoServer)}, nil
				}
				ls, err := lt.m.ResolveServer(qctx, anchor)
				if err != nil {
					return lspOut{Result: resolveErrMsg(err)}, nil
				}
				syms, err := ls.WorkspaceSymbols(qctx, symbol)
				if err != nil {
					return lspOut{Result: "lsp: " + err.Error()}, nil
				}
				p, ok := pickSymbolFile(syms, symbol)
				if !ok {
					return lspOut{Result: fmt.Sprintf("Symbol %q is not in the project index — try lsp_workspace_symbol, or pass `file`.", symbol)}, nil
				}
				path = p
			}

			ls, err := lt.m.ResolveServer(qctx, path)
			if err != nil {
				return lspOut{Result: resolveErrMsg(err)}, nil
			}
			sym, ok, err := ls.SymbolExtent(qctx, path, symbol)
			if err != nil {
				return lspOut{Result: "lsp: " + err.Error()}, nil
			}
			if !ok {
				return lspOut{Result: fmt.Sprintf("Symbol %q not found in %s — use lsp_document_symbols to list what's there, or Read the file.", symbol, displayPath(path, cwd))}, nil
			}
			body, err := sliceSymbol(path, sym.Range, sym.Kind, cwd)
			if err != nil {
				return lspOut{Result: "lsp: " + err.Error()}, nil
			}
			return lspOut{Result: body}, nil
		})
}

func (lt *lspTools) workspaceSymbol() tool.Tool {
	return newLSPTool("lsp_workspace_symbol",
		"Find where a symbol is defined anywhere in the project by name, using the language server's index "+
			"(answers 'where is X?'). Arguments: `query` (string, required) — a symbol name or fragment; "+
			"`file` (string, optional) — any source file in the target project, used to pick the language; "+
			"inferred from the session directory when omitted.",
		func(ctx tool.Context, in queryIn) (lspOut, error) {
			qctx, cancel := toolCtx(ctx)
			defer cancel()
			cwd := fstools.CwdForContext(ctx)
			anchor := lt.anchorFile(ctx, in.File)
			if anchor == "" {
				return lspOut{Result: resolveErrMsg(ErrNoServer)}, nil
			}
			ls, err := lt.m.ResolveServer(qctx, anchor)
			if err != nil {
				return lspOut{Result: resolveErrMsg(err)}, nil
			}
			syms, err := ls.WorkspaceSymbols(qctx, in.Query)
			if err != nil {
				return lspOut{Result: "lsp: " + err.Error()}, nil
			}
			return lspOut{Result: formatWorkspaceSymbols(syms, cwd)}, nil
		})
}

func (lt *lspTools) definition() tool.Tool {
	return newLSPTool("lsp_definition",
		"Go to the definition of a symbol that appears in a file. Give the symbol by name; the language server "+
			"resolves it across files. Arguments: `file` (string, required) — a file where the symbol appears; "+
			"`symbol` (string, required) — the symbol's name as written in the file.",
		func(ctx tool.Context, in symbolIn) (lspOut, error) {
			return lt.runLocate(ctx, in, func(qctx context.Context, ls *langServer, path, cwd string) (string, error) {
				locs, err := ls.Definition(qctx, path, in.Symbol)
				if err != nil {
					return "", err
				}
				return formatLocations(locs, cwd), nil
			})
		})
}

func (lt *lspTools) references() tool.Tool {
	return newLSPTool("lsp_references",
		"List every place a symbol is used across the project (call sites, usages) — essential before changing or "+
			"removing it. Includes the declaration. Arguments: `file` (string, required) — a file where the symbol "+
			"appears; `symbol` (string, required).",
		func(ctx tool.Context, in symbolIn) (lspOut, error) {
			return lt.runLocate(ctx, in, func(qctx context.Context, ls *langServer, path, cwd string) (string, error) {
				locs, err := ls.References(qctx, path, in.Symbol, true)
				if err != nil {
					return "", err
				}
				return formatLocations(locs, cwd), nil
			})
		})
}

func (lt *lspTools) hover() tool.Tool {
	return newLSPTool("lsp_hover",
		"Show a symbol's type signature and documentation (hover info) from the language server. "+
			"Arguments: `file` (string, required) — a file where the symbol appears; `symbol` (string, required).",
		func(ctx tool.Context, in symbolIn) (lspOut, error) {
			return lt.runLocate(ctx, in, func(qctx context.Context, ls *langServer, path, cwd string) (string, error) {
				txt, err := ls.Hover(qctx, path, in.Symbol)
				if err != nil {
					return "", err
				}
				if strings.TrimSpace(txt) == "" {
					return "(no hover information)", nil
				}
				return txt, nil
			})
		})
}

func (lt *lspTools) diagnostics() tool.Tool {
	return newLSPTool("lsp_diagnostics",
		"Report compiler/type errors and warnings for a file from the language server — the ground-truth check "+
			"after an edit. It re-reads the file from disk and re-analyses, so call it right after editing to see "+
			"exactly what broke (and call it again after fixing to confirm it's clean). "+
			"Arguments: `file` (string, required).",
		func(ctx tool.Context, in fileIn) (lspOut, error) {
			qctx, cancel := toolCtx(ctx)
			defer cancel()
			path := resolveFile(ctx, in.File)
			cwd := fstools.CwdForContext(ctx)
			ls, err := lt.m.ResolveServer(qctx, path)
			if err != nil {
				return lspOut{Result: resolveErrMsg(err)}, nil
			}
			diags, err := ls.Diagnostics(qctx, path, diagMaxWait, diagQuiet)
			if err != nil {
				return lspOut{Result: "lsp: " + err.Error()}, nil
			}
			return lspOut{Result: formatDiagnostics(path, diags, cwd)}, nil
		})
}

type renameIn struct {
	File    string `json:"file"`
	Symbol  string `json:"symbol"`
	NewName string `json:"new_name"`
}

func (lt *lspTools) rename() tool.Tool {
	return newLSPTool("lsp_rename",
		"Safely rename a symbol across the entire project using the language server: it updates every reference "+
			"in every file, far more reliably than find-and-replace. Changes are written to disk and are revertible. "+
			"Arguments: `file` (string, required) — a file where the symbol appears; `symbol` (string, required) — "+
			"its current name; `new_name` (string, required) — the new name.",
		func(ctx tool.Context, in renameIn) (lspOut, error) {
			qctx, cancel := toolCtx(ctx)
			defer cancel()
			path := resolveFile(ctx, in.File)
			cwd := fstools.CwdForContext(ctx)
			ls, err := lt.m.ResolveServer(qctx, path)
			if err != nil {
				return lspOut{Result: resolveErrMsg(err)}, nil
			}
			edits, err := ls.Rename(qctx, path, in.Symbol, in.NewName)
			if err != nil {
				return lspOut{Result: "lsp: " + err.Error()}, nil
			}
			if len(edits) == 0 {
				return lspOut{Result: "Rename produced no changes (symbol not found or not renameable here)."}, nil
			}
			summary, err := applyWorkspaceEdit(edits, cwd)
			if err != nil {
				return lspOut{Result: "lsp rename: " + err.Error()}, nil
			}
			// Keep each touched server's view current with the rewritten files.
			for uri := range edits {
				lt.m.NotifyChange(URIToPath(uri))
			}
			return lspOut{Result: summary}, nil
		})
}

// writeEditMap applies a per-file edit map to disk through the fs Write path
// (which snapshots each file, so the change is revertible), in a deterministic
// file order, skipping any file whose content is unchanged. It returns the
// per-file summary lines plus the count of files and total edits applied. Shared
// by lsp_rename and lsp_code_action, which wrap it with their own headers.
func writeEditMap(edits map[DocumentURI][]TextEdit, cwd string) (lines string, files, total int, err error) {
	uris := make([]DocumentURI, 0, len(edits))
	for uri := range edits {
		uris = append(uris, uri)
	}
	sort.Slice(uris, func(i, j int) bool { return uris[i] < uris[j] })

	var b strings.Builder
	for _, uri := range uris {
		path := URIToPath(uri)
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return "", 0, 0, fmt.Errorf("%s: %w", path, rerr)
		}
		updated := applyTextEdits(string(data), edits[uri])
		if updated == string(data) {
			continue
		}
		if _, werr := fstools.RunWrite(context.Background(), fstools.WriteIn{Path: path, Content: updated}); werr != nil {
			return "", 0, 0, fmt.Errorf("write %s: %w", path, werr)
		}
		n := len(edits[uri])
		files++
		total += n
		fmt.Fprintf(&b, "%s  (%d edit%s)\n", displayPath(path, cwd), n, plural(n))
	}
	return strings.TrimRight(b.String(), "\n"), files, total, nil
}

// defaultActionKinds is the "clean up this file" superset applied when the tool
// is called without an explicit kind, in dependency order: fix imports, apply
// safe whole-file fixes, then quickfix the remaining diagnostics. Each kind is
// requested and applied in its own round (re-syncing between rounds) so the
// import-block rewrites of organizeImports and fixAll never clash.
var defaultActionKinds = []string{"source.organizeImports", "source.fixAll", "quickfix"}

type codeActionIn struct {
	File string `json:"file"`
	Kind string `json:"kind,omitempty"`
}

func (lt *lspTools) codeAction() tool.Tool {
	return newLSPTool("lsp_code_action",
		"Apply the language server's own fixes to a file — organize/add/remove imports, apply safe fix-alls, and "+
			"quickfix diagnostics — instead of hand-patching them. This is how you clear the errors that lsp_diagnostics "+
			"reports (e.g. a missing or unused import): call it after an edit, then re-run lsp_diagnostics to confirm. "+
			"Changes are written to disk and are revertible. "+
			"Arguments: `file` (string, required) — path to a source file; `kind` (string, optional) — restrict to one "+
			"LSP code-action kind (e.g. `source.organizeImports`, `source.fixAll`, `quickfix`); omitted applies all three.",
		func(ctx tool.Context, in codeActionIn) (lspOut, error) {
			qctx, cancel := toolCtx(ctx)
			defer cancel()
			path := resolveFile(ctx, in.File)
			cwd := fstools.CwdForContext(ctx)
			ls, err := lt.m.ResolveServer(qctx, path)
			if err != nil {
				return lspOut{Result: resolveErrMsg(err)}, nil
			}

			kinds := defaultActionKinds
			if k := strings.TrimSpace(in.Kind); k != "" {
				kinds = []string{k}
			}

			var body strings.Builder
			var commandOnly []string
			totalFiles, totalEdits := 0, 0
			for _, kind := range kinds {
				// Quickfixes need the current diagnostics as context; source.* actions
				// don't. Fetch fresh diagnostics per quickfix round so a fix isn't
				// offered for an error an earlier round already resolved.
				var ctxDiags []Diagnostic
				if strings.HasPrefix(kind, "quickfix") {
					ctxDiags, _ = ls.Diagnostics(qctx, path, diagMaxWait, diagQuiet)
				}
				actions, aerr := ls.RequestCodeActions(qctx, path, []string{kind}, ctxDiags)
				if aerr != nil {
					continue // best-effort per kind
				}
				merged := map[DocumentURI][]TextEdit{}
				var titles []string
				for _, a := range actions {
					if !actionKindMatches(a.Kind, kind) {
						continue
					}
					if a.Edit != nil {
						for uri, edits := range flattenWorkspaceEdit(a.Edit) {
							merged[uri] = append(merged[uri], edits...)
						}
						titles = append(titles, a.Title)
					} else {
						commandOnly = append(commandOnly, a.Title)
					}
				}
				if len(merged) == 0 {
					continue
				}
				lines, files, edits, werr := writeEditMap(merged, cwd)
				if werr != nil {
					return lspOut{Result: "lsp code_action: " + werr.Error()}, nil
				}
				for uri := range merged {
					lt.m.NotifyChange(URIToPath(uri))
				}
				if files == 0 {
					continue
				}
				totalFiles += files
				totalEdits += edits
				fmt.Fprintf(&body, "%s — %s\n%s\n", kind, strings.Join(titles, "; "), lines)
			}

			if totalEdits == 0 {
				msg := "No code actions changed " + displayPath(path, cwd) + " (nothing to fix or organise)."
				if len(commandOnly) > 0 {
					msg += "\nOffered but not auto-applicable (need manual steps): " + strings.Join(dedupe(commandOnly), "; ")
				}
				return lspOut{Result: msg}, nil
			}
			header := fmt.Sprintf("Applied code actions across %d file%s, %d edit%s:\n",
				totalFiles, plural(totalFiles), totalEdits, plural(totalEdits))
			out := header + strings.TrimRight(body.String(), "\n")
			if len(commandOnly) > 0 {
				out += "\n\nOffered but not auto-applicable (need manual steps): " + strings.Join(dedupe(commandOnly), "; ")
			}
			return lspOut{Result: out}, nil
		})
}

// actionKindMatches reports whether a code action of kind actKind falls under
// the requested kind, honouring the LSP kind hierarchy (dot-separated): an
// action "source.organizeImports.foo" matches the request "source.organizeImports",
// and an unkinded action matches nothing (we only apply explicitly-kinded fixes).
func actionKindMatches(actKind, want string) bool {
	if actKind == "" {
		return false
	}
	return actKind == want || strings.HasPrefix(actKind, want+".")
}

// dedupe returns s with duplicate entries removed, preserving first-seen order.
func dedupe(s []string) []string {
	seen := map[string]bool{}
	out := s[:0]
	for _, v := range s {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

// applyWorkspaceEdit writes a rename's per-file edits to disk and returns a
// human-readable summary.
func applyWorkspaceEdit(edits map[DocumentURI][]TextEdit, cwd string) (string, error) {
	lines, files, total, err := writeEditMap(edits, cwd)
	if err != nil {
		return "", err
	}
	if files == 0 {
		return "Rename produced no on-disk changes.", nil
	}
	header := fmt.Sprintf("Renamed across %d file%s, %d edit%s:\n", files, plural(files), total, plural(total))
	return header + lines, nil
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// runLocate is the shared flow for the file+symbol tools: resolve the server,
// run the query, and map errors to clean fallback text.
func (lt *lspTools) runLocate(ctx tool.Context, in symbolIn,
	q func(qctx context.Context, ls *langServer, path, cwd string) (string, error)) (lspOut, error) {
	qctx, cancel := toolCtx(ctx)
	defer cancel()
	path := resolveFile(ctx, in.File)
	cwd := fstools.CwdForContext(ctx)
	ls, err := lt.m.ResolveServer(qctx, path)
	if err != nil {
		return lspOut{Result: resolveErrMsg(err)}, nil
	}
	out, err := q(qctx, ls, path, cwd)
	if err != nil {
		return lspOut{Result: "lsp: " + err.Error()}, nil
	}
	return lspOut{Result: out}, nil
}

// anchorFile picks a source file to select the language for a query not tied to
// one file (workspace/symbol). An explicit hint wins (resolved against cwd);
// otherwise it scans the session directory for the first file whose extension a
// configured server handles, skipping noise dirs and bounding the walk depth.
// Returns "" when nothing matches (caller reports ErrNoServer).
func (lt *lspTools) anchorFile(ctx tool.Context, hint string) string {
	if hint != "" {
		return resolveFile(ctx, hint)
	}
	cwd := fstools.CwdForContext(ctx)
	if cwd == "" {
		return ""
	}
	cfg := lt.m.cfgFn()
	if cfg == nil {
		return ""
	}
	var found string
	_ = filepath.WalkDir(cwd, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != cwd && skipDir(d.Name()) {
				return filepath.SkipDir
			}
			if walkDepth(cwd, p) > 4 {
				return filepath.SkipDir
			}
			return nil
		}
		if _, ok := cfg.ServerForFile(p); ok {
			found = p
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

func skipDir(name string) bool {
	switch name {
	case "node_modules", "vendor", "target", "dist", "build", ".git":
		return true
	}
	return strings.HasPrefix(name, ".")
}

func walkDepth(root, p string) int {
	rel, err := filepath.Rel(root, p)
	if err != nil || rel == "." {
		return 0
	}
	return strings.Count(rel, string(filepath.Separator)) + 1
}

// toolCtx builds a per-call context: a timeout bound plus the active session id,
// which the dependency gate uses to prompt the user on a first server start.
func toolCtx(ctx tool.Context) (context.Context, context.CancelFunc) {
	c, cancel := context.WithTimeout(ctx, toolTimeout)
	return withSession(c, ctx.SessionID()), cancel
}

// resolveFile resolves a tool-supplied path against the session working dir.
func resolveFile(ctx tool.Context, p string) string {
	cwd := fstools.CwdForContext(ctx)
	if cwd == "" || p == "" || filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(cwd, p)
}

// resolveErrMsg turns a ResolveServer error into model-facing guidance.
func resolveErrMsg(err error) string {
	if errors.Is(err, ErrNoServer) {
		return "No language server is configured for this file type — use Grep/Read to navigate instead."
	}
	return "lsp: " + err.Error()
}

func newLSPTool[A, R any](name, desc string, h functiontool.Func[A, R]) tool.Tool {
	t, err := functiontool.New(functiontool.Config{Name: name, Description: desc}, h)
	if err != nil {
		panic(fmt.Errorf("build lsp tool %s: %w", name, err))
	}
	return t
}

// --- formatting ---

// formatSymbols renders a symbol tree as an indented outline. Each line is
// "<name>  <signature>  (<kind>)  L<line>", where the signature is the
// server-provided detail (a function's parameters/returns, a field's type) —
// the most useful part for the model, since it shows how to use a symbol
// without opening the file. The detail is omitted when the server provides none.
func formatSymbols(syms []DocumentSymbol) string {
	var b strings.Builder
	var walk func(s []DocumentSymbol, depth int)
	walk = func(s []DocumentSymbol, depth int) {
		for _, sym := range s {
			b.WriteString(strings.Repeat("  ", depth))
			b.WriteString(sym.Name)
			if d := strings.TrimSpace(sym.Detail); d != "" {
				b.WriteString("  ")
				b.WriteString(d)
			}
			// Anchor line is the name (SelectionRange); append the body's end
			// line so the model knows the symbol's extent and can Read it whole.
			start := sym.SelectionRange.Start.Line + 1
			end := sym.Range.End.Line + 1
			if end > start {
				fmt.Fprintf(&b, "  (%s)  L%d-%d\n", sym.Kind, start, end)
			} else {
				fmt.Fprintf(&b, "  (%s)  L%d\n", sym.Kind, start)
			}
			walk(sym.Children, depth+1)
		}
	}
	walk(syms, 0)
	return strings.TrimRight(b.String(), "\n")
}

// pickSymbolFile chooses which file a project-wide symbol lookup meant: an exact
// name match wins, then a base-name match (so "Call" resolves gopls's
// "(*Client).Call"), then the first hit. Returns false when there are no hits.
func pickSymbolFile(syms []SymbolInformation, name string) (string, bool) {
	var baseMatch string
	for _, s := range syms {
		if s.Name == name {
			return URIToPath(s.Location.URI), true
		}
		if baseMatch == "" && symbolBaseName(s.Name) == name {
			baseMatch = URIToPath(s.Location.URI)
		}
	}
	if baseMatch != "" {
		return baseMatch, true
	}
	if len(syms) > 0 {
		return URIToPath(syms[0].Location.URI), true
	}
	return "", false
}

// sliceSymbol reads path and returns the source of the symbol whose full range
// is rng, with a header (path, line range, kind) and gutter line numbers so the
// model can edit precisely. The start is widened upward over an immediately
// preceding doc comment. Out-of-range lines are clamped.
func sliceSymbol(path string, rng Range, kind SymbolKind, cwd string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1] // drop trailing-newline artefact
	}
	start := rng.Start.Line
	end := rng.End.Line
	if start < 0 {
		start = 0
	}
	if end >= len(lines) {
		end = len(lines) - 1
	}
	if end < start {
		end = start
	}
	start = expandToDocComment(lines, start)

	var b strings.Builder
	fmt.Fprintf(&b, "%s  L%d-%d  (%s)\n", displayPath(path, cwd), start+1, end+1, kind)
	for i := start; i <= end; i++ {
		fmt.Fprintf(&b, "%5d\t%s\n", i+1, lines[i])
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// expandToDocComment walks upward from start over an unbroken run of
// comment-only lines (//, ///, #, or a /* … */ / * block) and returns the first
// such line's index, so a symbol is shown together with its doc comment. Stops
// at the first blank or non-comment line. Language-agnostic and conservative
// (covers Go/Rust/TS/JS //, Python #, and block-comment continuations).
func expandToDocComment(lines []string, start int) int {
	i := start - 1
	for i >= 0 {
		t := strings.TrimSpace(lines[i])
		if t == "" {
			break
		}
		if strings.HasPrefix(t, "//") || strings.HasPrefix(t, "#") ||
			strings.HasPrefix(t, "/*") || strings.HasPrefix(t, "*") ||
			strings.HasSuffix(t, "*/") {
			i--
			continue
		}
		break
	}
	return i + 1
}

// formatLocations renders locations as "file:line:col: <source line>" — the
// actual code at each location (like grep -n), so the model sees the call site
// or definition line without opening every file. Each file is read once
// (cached); long lines are truncated. Falls back to bare file:line:col when the
// line can't be read.
func formatLocations(locs []Location, cwd string) string {
	if len(locs) == 0 {
		return "(no results)"
	}
	cache := map[string][]string{}
	sourceLine := func(path string, line int) string {
		lines, ok := cache[path]
		if !ok {
			if data, err := os.ReadFile(path); err == nil {
				lines = strings.Split(string(data), "\n")
			}
			cache[path] = lines
		}
		if line >= 0 && line < len(lines) {
			return strings.TrimSpace(lines[line])
		}
		return ""
	}
	var b strings.Builder
	for _, l := range locs {
		p := URIToPath(l.URI)
		disp := displayPath(p, cwd)
		line, col := l.Range.Start.Line, l.Range.Start.Character
		if txt := sourceLine(p, line); txt != "" {
			fmt.Fprintf(&b, "%s:%d:%d: %s\n", disp, line+1, col+1, truncateLine(txt, 220))
		} else {
			fmt.Fprintf(&b, "%s:%d:%d\n", disp, line+1, col+1)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// truncateLine caps a source line so a very long line can't dominate the output.
func truncateLine(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

// diagCode renders a diagnostic code (LSP allows an int or a string).
func diagCode(code any) string {
	switch v := code.(type) {
	case string:
		return v
	case float64:
		if v == float64(int64(v)) {
			return strconv.FormatInt(int64(v), 10)
		}
		return strconv.FormatFloat(v, 'g', -1, 64)
	}
	return ""
}

// formatWorkspaceSymbols renders workspace/symbol hits as
// "name  (kind)  file:line  in <container>", including the enclosing container
// (type or package) the server reports so the model can disambiguate same-named
// symbols. workspace/symbol carries no signature (protocol limitation) — use
// lsp_hover on a result for that.
func formatWorkspaceSymbols(syms []SymbolInformation, cwd string) string {
	if len(syms) == 0 {
		return "(no results)"
	}
	var b strings.Builder
	for _, s := range syms {
		fmt.Fprintf(&b, "%s  (%s)  %s:%d",
			s.Name, s.Kind, displayPath(URIToPath(s.Location.URI), cwd), s.Location.Range.Start.Line+1)
		if c := strings.TrimSpace(s.ContainerName); c != "" {
			fmt.Fprintf(&b, "  in %s", c)
		}
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}

// formatDiagnostics renders diagnostics as compiler-style lines, sorted by
// position. An empty set is reported explicitly so the model knows the file is
// clean rather than unanalysed.
func formatDiagnostics(path string, diags []Diagnostic, cwd string) string {
	if len(diags) == 0 {
		return "No diagnostics — the language server reports no errors or warnings."
	}
	sort.Slice(diags, func(i, j int) bool {
		if diags[i].Range.Start.Line != diags[j].Range.Start.Line {
			return diags[i].Range.Start.Line < diags[j].Range.Start.Line
		}
		return diags[i].Range.Start.Character < diags[j].Range.Start.Character
	})
	disp := displayPath(path, cwd)
	var b strings.Builder
	for _, d := range diags {
		line, col := d.Range.Start.Line+1, d.Range.Start.Character+1
		fmt.Fprintf(&b, "%s:%d:%d: %s: %s", disp, line, col, d.Severity, strings.TrimSpace(d.Message))
		if code := diagCode(d.Code); code != "" {
			fmt.Fprintf(&b, " (%s)", code)
		}
		if src := strings.TrimSpace(d.Source); src != "" {
			fmt.Fprintf(&b, " [%s]", src)
		}
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}

// displayPath makes p relative to cwd when it stays within the tree.
func displayPath(p, cwd string) string {
	if cwd == "" {
		return p
	}
	if rel, err := filepath.Rel(cwd, p); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return p
}

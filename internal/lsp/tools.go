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
		lt.workspaceSymbol(),
		lt.definition(),
		lt.references(),
		lt.hover(),
		lt.diagnostics(),
		lt.rename(),
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

// applyWorkspaceEdit writes a rename's per-file edits to disk through the fs
// Write path (which snapshots each file, so the change is revertible) and
// returns a human-readable summary. Files are processed in a deterministic
// order; a file whose content is unchanged is skipped.
func applyWorkspaceEdit(edits map[DocumentURI][]TextEdit, cwd string) (string, error) {
	uris := make([]DocumentURI, 0, len(edits))
	for uri := range edits {
		uris = append(uris, uri)
	}
	sort.Slice(uris, func(i, j int) bool { return uris[i] < uris[j] })

	var b strings.Builder
	files, total := 0, 0
	for _, uri := range uris {
		path := URIToPath(uri)
		data, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("%s: %w", path, err)
		}
		updated := applyTextEdits(string(data), edits[uri])
		if updated == string(data) {
			continue
		}
		if _, err := fstools.RunWrite(context.Background(), fstools.WriteIn{Path: path, Content: updated}); err != nil {
			return "", fmt.Errorf("write %s: %w", path, err)
		}
		n := len(edits[uri])
		files++
		total += n
		fmt.Fprintf(&b, "%s  (%d edit%s)\n", displayPath(path, cwd), n, plural(n))
	}
	if files == 0 {
		return "Rename produced no on-disk changes.", nil
	}
	header := fmt.Sprintf("Renamed across %d file%s, %d edit%s:\n", files, plural(files), total, plural(total))
	return header + strings.TrimRight(b.String(), "\n"), nil
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

// formatSymbols renders a symbol tree as an indented outline with kinds and
// 1-based line numbers.
func formatSymbols(syms []DocumentSymbol) string {
	var b strings.Builder
	var walk func(s []DocumentSymbol, depth int)
	walk = func(s []DocumentSymbol, depth int) {
		for _, sym := range s {
			fmt.Fprintf(&b, "%s%s  (%s)  L%d\n",
				strings.Repeat("  ", depth), sym.Name, sym.Kind, sym.SelectionRange.Start.Line+1)
			walk(sym.Children, depth+1)
		}
	}
	walk(syms, 0)
	return strings.TrimRight(b.String(), "\n")
}

// formatLocations renders locations as compact 1-based file:line:col lines,
// relative to cwd when possible.
func formatLocations(locs []Location, cwd string) string {
	if len(locs) == 0 {
		return "(no results)"
	}
	var b strings.Builder
	for _, l := range locs {
		fmt.Fprintf(&b, "%s:%d:%d\n",
			displayPath(URIToPath(l.URI), cwd), l.Range.Start.Line+1, l.Range.Start.Character+1)
	}
	return strings.TrimRight(b.String(), "\n")
}

// formatWorkspaceSymbols renders workspace/symbol hits as "name  (kind)  file:line".
func formatWorkspaceSymbols(syms []SymbolInformation, cwd string) string {
	if len(syms) == 0 {
		return "(no results)"
	}
	var b strings.Builder
	for _, s := range syms {
		fmt.Fprintf(&b, "%s  (%s)  %s:%d\n",
			s.Name, s.Kind, displayPath(URIToPath(s.Location.URI), cwd), s.Location.Range.Start.Line+1)
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

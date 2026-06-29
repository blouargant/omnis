// methods.go — the LSP query helpers layered on the transport: definition,
// references, hover, and workspace/symbol, plus the polymorphic decoders LSP
// forces on us (definition can be Location | Location[] | LocationLink[]; hover
// content can be a string, a {value} object, or an array; documentSymbol can be
// hierarchical or flat). Keeping the decoding here keeps client.go transport-only.
package lsp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
)

// Definition returns the definition location(s) for the symbol at pos.
func (c *Client) Definition(ctx context.Context, uri DocumentURI, pos Position) ([]Location, error) {
	var raw json.RawMessage
	if err := c.Call(ctx, "textDocument/definition",
		TextDocumentPositionParams{TextDocument: TextDocumentIdentifier{URI: uri}, Position: pos}, &raw); err != nil {
		return nil, err
	}
	return parseLocations(raw), nil
}

// References returns every usage of the symbol at pos across the project.
func (c *Client) References(ctx context.Context, uri DocumentURI, pos Position, includeDecl bool) ([]Location, error) {
	var raw json.RawMessage
	params := ReferenceParams{
		TextDocumentPositionParams: TextDocumentPositionParams{
			TextDocument: TextDocumentIdentifier{URI: uri}, Position: pos,
		},
		Context: ReferenceContext{IncludeDeclaration: includeDecl},
	}
	if err := c.Call(ctx, "textDocument/references", params, &raw); err != nil {
		return nil, err
	}
	return parseLocations(raw), nil
}

// Hover returns the symbol's signature/documentation at pos as plain text
// (empty when the server has nothing to show).
func (c *Client) Hover(ctx context.Context, uri DocumentURI, pos Position) (string, error) {
	var raw json.RawMessage
	if err := c.Call(ctx, "textDocument/hover",
		TextDocumentPositionParams{TextDocument: TextDocumentIdentifier{URI: uri}, Position: pos}, &raw); err != nil {
		return "", err
	}
	if isNullRaw(raw) {
		return "", nil
	}
	var h struct {
		Contents json.RawMessage `json:"contents"`
	}
	if err := json.Unmarshal(raw, &h); err != nil {
		return "", err
	}
	return markupText(h.Contents), nil
}

// WorkspaceSymbols searches the project-wide symbol index by name.
func (c *Client) WorkspaceSymbols(ctx context.Context, query string) ([]SymbolInformation, error) {
	var syms []SymbolInformation
	err := c.Call(ctx, "workspace/symbol", WorkspaceSymbolParams{Query: query}, &syms)
	return syms, err
}

// Rename returns the edits to apply for a project-wide rename of the symbol at
// pos to newName, flattened to a per-file map of TextEdits.
func (c *Client) Rename(ctx context.Context, uri DocumentURI, pos Position, newName string) (map[DocumentURI][]TextEdit, error) {
	var raw json.RawMessage
	if err := c.Call(ctx, "textDocument/rename",
		RenameParams{TextDocument: TextDocumentIdentifier{URI: uri}, Position: pos, NewName: newName}, &raw); err != nil {
		return nil, err
	}
	return parseWorkspaceEdit(raw), nil
}

// parseWorkspaceEdit flattens a WorkspaceEdit (either the changes map or the
// documentChanges array) into a per-file edit map. File-operation entries in
// documentChanges (create/rename/delete) are skipped — symbol renames don't
// emit them.
func parseWorkspaceEdit(raw json.RawMessage) map[DocumentURI][]TextEdit {
	if isNullRaw(raw) {
		return nil
	}
	var we WorkspaceEdit
	if err := json.Unmarshal(raw, &we); err != nil {
		return nil
	}
	out := map[DocumentURI][]TextEdit{}
	for uri, edits := range we.Changes {
		out[uri] = append(out[uri], edits...)
	}
	if len(we.DocumentChanges) > 0 {
		var tdes []TextDocumentEdit
		if err := json.Unmarshal(we.DocumentChanges, &tdes); err == nil {
			for _, tde := range tdes {
				if tde.TextDocument.URI != "" && len(tde.Edits) > 0 {
					out[tde.TextDocument.URI] = append(out[tde.TextDocument.URI], tde.Edits...)
				}
			}
		}
	}
	return out
}

// parseDocumentSymbols decodes a documentSymbol result that may be either the
// hierarchical DocumentSymbol[] (has "selectionRange") or the flat
// SymbolInformation[] (has "location"), normalising the latter into the former.
func parseDocumentSymbols(raw json.RawMessage) []DocumentSymbol {
	raw = bytes.TrimSpace(raw)
	if isNullRaw(raw) || string(raw) == "[]" {
		return nil
	}
	// DocumentSymbol always carries selectionRange; SymbolInformation never does.
	if bytes.Contains(raw, []byte(`"selectionRange"`)) {
		var syms []DocumentSymbol
		if err := json.Unmarshal(raw, &syms); err == nil {
			return syms
		}
	}
	var infos []SymbolInformation
	if err := json.Unmarshal(raw, &infos); err == nil && len(infos) > 0 {
		out := make([]DocumentSymbol, 0, len(infos))
		for _, si := range infos {
			out = append(out, DocumentSymbol{
				Name:           si.Name,
				Kind:           si.Kind,
				Range:          si.Location.Range,
				SelectionRange: si.Location.Range,
			})
		}
		return out
	}
	// Last resort: a hierarchical payload that somehow lacked the marker.
	var syms []DocumentSymbol
	_ = json.Unmarshal(raw, &syms)
	return syms
}

// parseLocations decodes the definition/references union (Location, Location[],
// or LocationLink[]) into a flat []Location.
func parseLocations(raw json.RawMessage) []Location {
	raw = bytes.TrimSpace(raw)
	if isNullRaw(raw) {
		return nil
	}
	if raw[0] == '[' {
		// Prefer LocationLink[] when present (it carries targetUri).
		var links []LocationLink
		if err := json.Unmarshal(raw, &links); err == nil && len(links) > 0 && links[0].TargetURI != "" {
			out := make([]Location, 0, len(links))
			for _, l := range links {
				out = append(out, Location{URI: l.TargetURI, Range: l.TargetSelectionRange})
			}
			return out
		}
		var locs []Location
		if err := json.Unmarshal(raw, &locs); err == nil {
			return locs
		}
		return nil
	}
	var loc Location
	if err := json.Unmarshal(raw, &loc); err == nil && loc.URI != "" {
		return []Location{loc}
	}
	return nil
}

// markupText flattens a hover "contents" payload (string | {value} | array)
// into plain text.
func markupText(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if isNullRaw(raw) {
		return ""
	}
	switch raw[0] {
	case '"':
		var s string
		_ = json.Unmarshal(raw, &s)
		return s
	case '{':
		// Covers MarkupContent {kind,value} and MarkedString {language,value}.
		var o struct {
			Value string `json:"value"`
		}
		_ = json.Unmarshal(raw, &o)
		return o.Value
	case '[':
		var arr []json.RawMessage
		_ = json.Unmarshal(raw, &arr)
		parts := make([]string, 0, len(arr))
		for _, e := range arr {
			if t := markupText(e); t != "" {
				parts = append(parts, t)
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

func isNullRaw(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	return len(raw) == 0 || string(raw) == "null"
}

// URIToPath converts a file:// URI back to a filesystem path. On Windows the
// leading slash of file:///C:/x is stripped and slashes are converted back.
func URIToPath(uri DocumentURI) string {
	s := string(uri)
	p := strings.TrimPrefix(s, "file://")
	if u, err := url.Parse(s); err == nil && u.Scheme == "file" {
		p = u.Path
	}
	if runtime.GOOS == "windows" {
		p = strings.TrimPrefix(p, "/")
		p = filepath.FromSlash(p)
	}
	return p
}

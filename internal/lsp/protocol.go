// Package lsp implements a minimal Language Server Protocol client used by the
// coding squad's code-intelligence tools. It speaks JSON-RPC 2.0 over the LSP
// base protocol (Content-Length framed messages) and is kept deliberately
// dependency-light: only the handful of methods Omnis needs are modeled here.
//
// This file holds the wire types. Only the subset required by the implemented
// tools is defined; unknown fields are ignored on decode.
package lsp

import "encoding/json"

// DocumentURI is an LSP document URI, e.g. "file:///home/user/main.go".
type DocumentURI string

// Position is a zero-based (line, character) location. Per the LSP spec the
// character offset is measured in UTF-16 code units unless the server
// negotiates a different positionEncoding (LSP 3.17) — see position.go (M3+).
type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

// Range is a [start, end) span within a document.
type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

// Location is a range inside a specific document.
type Location struct {
	URI   DocumentURI `json:"uri"`
	Range Range       `json:"range"`
}

// TextDocumentIdentifier references a document by URI.
type TextDocumentIdentifier struct {
	URI DocumentURI `json:"uri"`
}

// TextDocumentItem carries a document's full content for textDocument/didOpen.
type TextDocumentItem struct {
	URI        DocumentURI `json:"uri"`
	LanguageID string      `json:"languageId"`
	Version    int         `json:"version"`
	Text       string      `json:"text"`
}

// --- initialize ---

// InitializeParams is the payload of the initial handshake request.
type InitializeParams struct {
	ProcessID    int                `json:"processId"`
	RootURI      DocumentURI        `json:"rootUri"`
	Capabilities ClientCapabilities `json:"capabilities"`
	ClientInfo   *ClientInfo        `json:"clientInfo,omitempty"`
}

// ClientInfo identifies this client to the server (shown in some servers' logs).
type ClientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// ClientCapabilities advertises what this client supports. Kept minimal; we
// request hierarchical document symbols so servers return DocumentSymbol[] (a
// tree) rather than the flat SymbolInformation[].
type ClientCapabilities struct {
	TextDocument TextDocumentClientCapabilities `json:"textDocument"`
}

// TextDocumentClientCapabilities groups per-document feature support flags.
type TextDocumentClientCapabilities struct {
	DocumentSymbol DocumentSymbolClientCapabilities `json:"documentSymbol"`
}

// DocumentSymbolClientCapabilities opts into the hierarchical symbol response.
type DocumentSymbolClientCapabilities struct {
	HierarchicalDocumentSymbolSupport bool `json:"hierarchicalDocumentSymbolSupport"`
}

// InitializeResult is the server's reply to initialize. Capabilities is kept as
// a raw map for now; specific feature negotiation is parsed in later milestones.
type InitializeResult struct {
	Capabilities map[string]any `json:"capabilities"`
	ServerInfo   *ServerInfo    `json:"serverInfo,omitempty"`
}

// ServerInfo identifies the language server (name + version).
type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// --- textDocument/didOpen ---

// DidOpenTextDocumentParams notifies the server that a document is now open and
// supplies its content (the server tracks edits via didChange thereafter).
type DidOpenTextDocumentParams struct {
	TextDocument TextDocumentItem `json:"textDocument"`
}

// --- textDocument/documentSymbol ---

// DocumentSymbolParams requests the symbol outline of one document.
type DocumentSymbolParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

// DocumentSymbol is the hierarchical symbol shape (when the server honours
// hierarchicalDocumentSymbolSupport). Children nest the file's structure.
type DocumentSymbol struct {
	Name           string           `json:"name"`
	Detail         string           `json:"detail,omitempty"`
	Kind           SymbolKind       `json:"kind"`
	Range          Range            `json:"range"`
	SelectionRange Range            `json:"selectionRange"`
	Children       []DocumentSymbol `json:"children,omitempty"`
}

// --- textDocument/didChange + publishDiagnostics ---

// VersionedTextDocumentIdentifier references a document at a specific version,
// for didChange.
type VersionedTextDocumentIdentifier struct {
	URI     DocumentURI `json:"uri"`
	Version int         `json:"version"`
}

// TextDocumentContentChangeEvent with only Text (no Range) is a full-document
// replace — valid under both Full and Incremental textDocumentSync, so it works
// with every server without inspecting the negotiated sync kind.
type TextDocumentContentChangeEvent struct {
	Text string `json:"text"`
}

// DidChangeTextDocumentParams notifies the server of new document content.
type DidChangeTextDocumentParams struct {
	TextDocument   VersionedTextDocumentIdentifier  `json:"textDocument"`
	ContentChanges []TextDocumentContentChangeEvent `json:"contentChanges"`
}

// DiagnosticSeverity is the LSP severity scale (1=Error … 4=Hint).
type DiagnosticSeverity int

// LSP DiagnosticSeverity constants.
const (
	SeverityError       DiagnosticSeverity = 1
	SeverityWarning     DiagnosticSeverity = 2
	SeverityInformation DiagnosticSeverity = 3
	SeverityHint        DiagnosticSeverity = 4
)

// String returns the lowercase severity name.
func (s DiagnosticSeverity) String() string {
	switch s {
	case SeverityError:
		return "error"
	case SeverityWarning:
		return "warning"
	case SeverityInformation:
		return "info"
	case SeverityHint:
		return "hint"
	default:
		return "diagnostic"
	}
}

// Diagnostic is one problem reported by the server. Code is left as any because
// the spec allows an integer or a string.
type Diagnostic struct {
	Range    Range              `json:"range"`
	Severity DiagnosticSeverity `json:"severity,omitempty"`
	Code     any                `json:"code,omitempty"`
	Source   string             `json:"source,omitempty"`
	Message  string             `json:"message"`
}

// PublishDiagnosticsParams is the server→client notification carrying a file's
// current diagnostics (an empty slice clears them).
type PublishDiagnosticsParams struct {
	URI         DocumentURI  `json:"uri"`
	Version     *int         `json:"version,omitempty"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

// --- textDocument/{definition,references,hover} ; workspace/symbol ---

// TextDocumentPositionParams locates a position within a document; it is the
// payload of definition/hover and the base of references.
type TextDocumentPositionParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
}

// ReferenceContext toggles whether the declaration is included in references.
type ReferenceContext struct {
	IncludeDeclaration bool `json:"includeDeclaration"`
}

// ReferenceParams is the textDocument/references payload (position + context).
// The embedded base struct's fields are inlined by encoding/json.
type ReferenceParams struct {
	TextDocumentPositionParams
	Context ReferenceContext `json:"context"`
}

// LocationLink is the richer definition-result shape (LSP 3.14+); some servers
// return these instead of plain Locations. We collapse them to Locations.
type LocationLink struct {
	TargetURI            DocumentURI `json:"targetUri"`
	TargetRange          Range       `json:"targetRange"`
	TargetSelectionRange Range       `json:"targetSelectionRange"`
}

// WorkspaceSymbolParams queries the project-wide symbol index by name.
type WorkspaceSymbolParams struct {
	Query string `json:"query"`
}

// SymbolInformation is the flat symbol shape returned by workspace/symbol and
// by servers that ignore hierarchicalDocumentSymbolSupport on documentSymbol.
type SymbolInformation struct {
	Name          string     `json:"name"`
	Kind          SymbolKind `json:"kind"`
	Location      Location   `json:"location"`
	ContainerName string     `json:"containerName,omitempty"`
}

// --- textDocument/rename ---

// RenameParams requests a project-wide rename of the symbol at Position.
type RenameParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
	NewName      string                 `json:"newName"`
}

// TextEdit is a single replacement within a document.
type TextEdit struct {
	Range   Range  `json:"range"`
	NewText string `json:"newText"`
}

// TextDocumentEdit groups edits for one document (the documentChanges form).
type TextDocumentEdit struct {
	TextDocument VersionedTextDocumentIdentifier `json:"textDocument"`
	Edits        []TextEdit                      `json:"edits"`
}

// WorkspaceEdit is a rename result. Servers return either the simple changes
// map or the richer documentChanges array (which may also carry create/rename/
// delete file operations — kept raw and decoded as TextDocumentEdit[], skipping
// file ops, which symbol renames don't produce).
type WorkspaceEdit struct {
	Changes         map[DocumentURI][]TextEdit `json:"changes,omitempty"`
	DocumentChanges json.RawMessage            `json:"documentChanges,omitempty"`
}

// SymbolKind enumerates the LSP symbol kinds (spec values 1..26).
type SymbolKind int

// LSP SymbolKind constants.
const (
	SymbolKindFile          SymbolKind = 1
	SymbolKindModule        SymbolKind = 2
	SymbolKindNamespace     SymbolKind = 3
	SymbolKindPackage       SymbolKind = 4
	SymbolKindClass         SymbolKind = 5
	SymbolKindMethod        SymbolKind = 6
	SymbolKindProperty      SymbolKind = 7
	SymbolKindField         SymbolKind = 8
	SymbolKindConstructor   SymbolKind = 9
	SymbolKindEnum          SymbolKind = 10
	SymbolKindInterface     SymbolKind = 11
	SymbolKindFunction      SymbolKind = 12
	SymbolKindVariable      SymbolKind = 13
	SymbolKindConstant      SymbolKind = 14
	SymbolKindString        SymbolKind = 15
	SymbolKindNumber        SymbolKind = 16
	SymbolKindBoolean       SymbolKind = 17
	SymbolKindArray         SymbolKind = 18
	SymbolKindObject        SymbolKind = 19
	SymbolKindKey           SymbolKind = 20
	SymbolKindNull          SymbolKind = 21
	SymbolKindEnumMember    SymbolKind = 22
	SymbolKindStruct        SymbolKind = 23
	SymbolKindEvent         SymbolKind = 24
	SymbolKindOperator      SymbolKind = 25
	SymbolKindTypeParameter SymbolKind = 26
)

// String returns the lowercase spec name of a SymbolKind (or "unknown").
func (k SymbolKind) String() string {
	switch k {
	case SymbolKindFile:
		return "file"
	case SymbolKindModule:
		return "module"
	case SymbolKindNamespace:
		return "namespace"
	case SymbolKindPackage:
		return "package"
	case SymbolKindClass:
		return "class"
	case SymbolKindMethod:
		return "method"
	case SymbolKindProperty:
		return "property"
	case SymbolKindField:
		return "field"
	case SymbolKindConstructor:
		return "constructor"
	case SymbolKindEnum:
		return "enum"
	case SymbolKindInterface:
		return "interface"
	case SymbolKindFunction:
		return "function"
	case SymbolKindVariable:
		return "variable"
	case SymbolKindConstant:
		return "constant"
	case SymbolKindString:
		return "string"
	case SymbolKindNumber:
		return "number"
	case SymbolKindBoolean:
		return "boolean"
	case SymbolKindArray:
		return "array"
	case SymbolKindObject:
		return "object"
	case SymbolKindKey:
		return "key"
	case SymbolKindNull:
		return "null"
	case SymbolKindEnumMember:
		return "enum-member"
	case SymbolKindStruct:
		return "struct"
	case SymbolKindEvent:
		return "event"
	case SymbolKindOperator:
		return "operator"
	case SymbolKindTypeParameter:
		return "type-parameter"
	default:
		return "unknown"
	}
}

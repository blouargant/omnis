// client.go — the JSON-RPC 2.0 transport for the LSP base protocol.
//
// A Client speaks to a single language server over a reader/writer pair (a
// server process's stdout/stdin). It frames messages with Content-Length
// headers, correlates responses to outbound requests by id, and dispatches
// inbound server→client requests and notifications. The Client is transport-
// only: process spawning and the (root, language) pool live in the manager
// (M2). It is safe for concurrent use — Call/Notify may be invoked from many
// goroutines.
package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

const jsonrpcVersion = "2.0"

// errClientClosed is returned to in-flight callers when the transport closes
// (server exited, read error, or an explicit Close).
var errClientClosed = errors.New("lsp: client closed")

// rpcMessage is the union envelope for every JSON-RPC 2.0 message on the wire.
// A request carries Method+ID; a response carries ID+(Result|Error); a
// notification carries Method only. ID is kept raw so both numeric (ours) and
// string (some servers') ids round-trip unchanged.
type rpcMessage struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"`
	Method  string           `json:"method,omitempty"`
	Params  json.RawMessage  `json:"params,omitempty"`
	Result  json.RawMessage  `json:"result,omitempty"`
	Error   *rpcError        `json:"error,omitempty"`
}

// rpcError is a JSON-RPC 2.0 error object.
type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *rpcError) Error() string {
	return fmt.Sprintf("lsp rpc error %d: %s", e.Code, e.Message)
}

// Client is a JSON-RPC connection to one language server.
type Client struct {
	w io.Writer
	r *bufio.Reader

	writeMu sync.Mutex // serialises frame writes (Call, Notify, responses)

	mu      sync.Mutex
	nextID  int
	pending map[int]chan rpcMessage
	closed  bool
	readErr error

	closeOnce sync.Once
	done      chan struct{}

	// onRequest handles inbound server→client requests (e.g. gopls's
	// workspace/configuration). It returns the result to send back; a nil
	// result becomes a JSON null. Defaults to defaultRequestHandler.
	onRequest func(method string, params json.RawMessage) (any, error)
	// onNotify handles inbound notifications ($/progress,
	// textDocument/publishDiagnostics, …). nil ignores them. Wired in M4.
	onNotify func(method string, params json.RawMessage)
}

// NewClient builds a Client over r (server stdout) and w (server stdin). Call
// Start to begin reading. The caller owns the underlying process.
func NewClient(r io.Reader, w io.Writer) *Client {
	c := &Client{
		w:       w,
		r:       bufio.NewReader(r),
		pending: map[int]chan rpcMessage{},
		done:    make(chan struct{}),
	}
	c.onRequest = c.defaultRequestHandler
	return c
}

// SetNotifyHandler installs the inbound-notification callback (used in M4 for
// diagnostics and progress). Set it before Start.
func (c *Client) SetNotifyHandler(fn func(method string, params json.RawMessage)) {
	c.onNotify = fn
}

// Start launches the background read loop. Call exactly once.
func (c *Client) Start() { go c.readLoop() }

// Close stops the transport. In-flight Calls return errClientClosed. It does
// not terminate the server process — the owner does that.
func (c *Client) Close() error {
	c.markClosed(errClientClosed)
	return nil
}

// Call sends a request and blocks until the matching response arrives, the
// context is cancelled, or the transport closes. params/result may be nil.
func (c *Client) Call(ctx context.Context, method string, params, result any) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return errClientClosed
	}
	c.nextID++
	id := c.nextID
	ch := make(chan rpcMessage, 1)
	c.pending[id] = ch
	c.mu.Unlock()

	rawParams, err := marshalParams(params)
	if err != nil {
		c.clearPending(id)
		return err
	}
	idRaw := json.RawMessage(strconv.Itoa(id))
	if err := c.writeMessage(rpcMessage{
		JSONRPC: jsonrpcVersion,
		ID:      &idRaw,
		Method:  method,
		Params:  rawParams,
	}); err != nil {
		c.clearPending(id)
		return err
	}

	select {
	case <-ctx.Done():
		c.clearPending(id)
		return ctx.Err()
	case <-c.done:
		return c.readErr
	case resp := <-ch:
		if resp.Error != nil {
			return resp.Error
		}
		if result != nil && len(resp.Result) > 0 {
			return json.Unmarshal(resp.Result, result)
		}
		return nil
	}
}

// Notify sends a notification (a request without an id; no reply expected).
func (c *Client) Notify(method string, params any) error {
	rawParams, err := marshalParams(params)
	if err != nil {
		return err
	}
	return c.writeMessage(rpcMessage{
		JSONRPC: jsonrpcVersion,
		Method:  method,
		Params:  rawParams,
	})
}

func marshalParams(params any) (json.RawMessage, error) {
	if params == nil {
		return nil, nil
	}
	b, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("marshal params: %w", err)
	}
	return b, nil
}

// writeMessage frames v with a Content-Length header and writes it atomically.
func (c *Client) writeMessage(v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal message: %w", err)
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if _, err := fmt.Fprintf(c.w, "Content-Length: %d\r\n\r\n", len(body)); err != nil {
		return err
	}
	_, err = c.w.Write(body)
	return err
}

// readLoop reads framed messages until the stream errors or closes.
func (c *Client) readLoop() {
	for {
		msg, err := c.readMessage()
		if err != nil {
			c.markClosed(err)
			return
		}
		c.dispatch(msg)
	}
}

// readMessage parses one Content-Length framed JSON-RPC message.
func (c *Client) readMessage() (rpcMessage, error) {
	var contentLength int
	for {
		line, err := c.r.ReadString('\n')
		if err != nil {
			return rpcMessage{}, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break // blank line terminates the header block
		}
		if v, ok := strings.CutPrefix(line, "Content-Length:"); ok {
			n, err := strconv.Atoi(strings.TrimSpace(v))
			if err != nil {
				return rpcMessage{}, fmt.Errorf("bad Content-Length: %w", err)
			}
			contentLength = n
		}
	}
	if contentLength <= 0 {
		return rpcMessage{}, fmt.Errorf("message missing Content-Length header")
	}
	body := make([]byte, contentLength)
	if _, err := io.ReadFull(c.r, body); err != nil {
		return rpcMessage{}, err
	}
	var msg rpcMessage
	if err := json.Unmarshal(body, &msg); err != nil {
		return rpcMessage{}, fmt.Errorf("decode message: %w", err)
	}
	return msg, nil
}

// dispatch routes a parsed message to the right handler.
func (c *Client) dispatch(msg rpcMessage) {
	switch {
	case msg.ID != nil && msg.Method != "":
		c.handleServerRequest(msg) // server→client request
	case msg.ID != nil:
		c.handleResponse(msg) // response to one of our Calls
	case msg.Method != "":
		if c.onNotify != nil {
			c.onNotify(msg.Method, msg.Params) // notification
		}
	}
}

// handleResponse delivers a response to the waiting Call.
func (c *Client) handleResponse(msg rpcMessage) {
	var id int
	if err := json.Unmarshal(*msg.ID, &id); err != nil {
		return
	}
	c.mu.Lock()
	ch, ok := c.pending[id]
	if ok {
		delete(c.pending, id)
	}
	c.mu.Unlock()
	if ok {
		ch <- msg
	}
}

// handleServerRequest answers an inbound request, echoing its id.
func (c *Client) handleServerRequest(msg rpcMessage) {
	var result any
	var rerr error
	if c.onRequest != nil {
		result, rerr = c.onRequest(msg.Method, msg.Params)
	}
	resp := map[string]any{"jsonrpc": jsonrpcVersion, "id": msg.ID}
	if rerr != nil {
		resp["error"] = map[string]any{"code": -32603, "message": rerr.Error()}
	} else {
		resp["result"] = result
	}
	_ = c.writeMessage(resp)
}

// defaultRequestHandler answers the common server→client requests so the
// handshake doesn't deadlock. workspace/configuration gets one null per
// requested item (server uses its defaults); everything else gets null.
func (c *Client) defaultRequestHandler(method string, params json.RawMessage) (any, error) {
	switch method {
	case "workspace/configuration":
		var p struct {
			Items []json.RawMessage `json:"items"`
		}
		_ = json.Unmarshal(params, &p)
		return make([]any, len(p.Items)), nil
	default:
		// window/workDoneProgress/create, client/registerCapability, etc.
		return nil, nil
	}
}

func (c *Client) clearPending(id int) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

// markClosed transitions the client to closed exactly once, unblocking every
// in-flight Call via the done channel.
func (c *Client) markClosed(err error) {
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.closed = true
		if err != nil {
			c.readErr = err
		} else {
			c.readErr = errClientClosed
		}
		c.pending = map[int]chan rpcMessage{}
		c.mu.Unlock()
		close(c.done)
	})
}

// --- LSP convenience helpers ---

// Initialize performs the LSP handshake: the initialize request against
// rootPath followed by the initialized notification. The server is ready for
// document requests once this returns.
func (c *Client) Initialize(ctx context.Context, rootPath string) (*InitializeResult, error) {
	params := InitializeParams{
		ProcessID:  os.Getpid(),
		RootURI:    PathToURI(rootPath),
		ClientInfo: &ClientInfo{Name: "omnis", Version: "dev"},
		Capabilities: ClientCapabilities{
			TextDocument: TextDocumentClientCapabilities{
				DocumentSymbol: DocumentSymbolClientCapabilities{
					HierarchicalDocumentSymbolSupport: true,
				},
			},
		},
	}
	var res InitializeResult
	if err := c.Call(ctx, "initialize", params, &res); err != nil {
		return nil, err
	}
	if err := c.Notify("initialized", struct{}{}); err != nil {
		return nil, err
	}
	return &res, nil
}

// DidOpen tells the server a document is open and supplies its content.
func (c *Client) DidOpen(uri DocumentURI, languageID, text string) error {
	return c.Notify("textDocument/didOpen", DidOpenTextDocumentParams{
		TextDocument: TextDocumentItem{
			URI:        uri,
			LanguageID: languageID,
			Version:    1,
			Text:       text,
		},
	})
}

// DidChange sends the document's new full content at version. A change event
// with only Text (no Range) is a full replace, accepted by Full and Incremental
// servers alike.
func (c *Client) DidChange(uri DocumentURI, version int, text string) error {
	return c.Notify("textDocument/didChange", DidChangeTextDocumentParams{
		TextDocument:   VersionedTextDocumentIdentifier{URI: uri, Version: version},
		ContentChanges: []TextDocumentContentChangeEvent{{Text: text}},
	})
}

// DocumentSymbols returns the symbol outline of an opened document. The server
// must have seen the document via DidOpen first. parseDocumentSymbols handles
// both the hierarchical DocumentSymbol[] tree (requested via capabilities) and
// the flat SymbolInformation[] some servers return regardless.
func (c *Client) DocumentSymbols(ctx context.Context, uri DocumentURI) ([]DocumentSymbol, error) {
	var raw json.RawMessage
	if err := c.Call(ctx, "textDocument/documentSymbol",
		DocumentSymbolParams{TextDocument: TextDocumentIdentifier{URI: uri}}, &raw); err != nil {
		return nil, err
	}
	return parseDocumentSymbols(raw), nil
}

// Shutdown asks the server to shut down (shutdown request + exit notification).
// Best-effort; the owner should still terminate the process.
func (c *Client) Shutdown(ctx context.Context) error {
	if err := c.Call(ctx, "shutdown", nil, nil); err != nil {
		return err
	}
	return c.Notify("exit", nil)
}

// PathToURI converts a filesystem path to a file:// URI. A Windows drive path
// (C:\x) becomes file:///C:/x — the leading slash is added so the URL renders
// as an absolute file URI on every OS.
func PathToURI(path string) DocumentURI {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	p := filepath.ToSlash(abs)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	u := url.URL{Scheme: "file", Path: p}
	return DocumentURI(u.String())
}

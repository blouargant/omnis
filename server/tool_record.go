package main

import (
	"encoding/json"
	"unicode/utf16"

	"google.golang.org/genai"

	"github.com/blouargant/omnis/internal/sessions"
)

// Caps on what one turn's tool record keeps on disk: a long agentic turn can
// make hundreds of calls with megabytes of output, and the record only has to
// let the user see what happened, not replay it.
const (
	maxRecordedCalls    = 300
	maxRecordedChildren = 100
	maxRecordedBytes    = 8 << 10
)

// recordSkippedTools are never recorded: the routing tools are the router's
// private negotiation (hidden live by isRoutingTool in the web UI).
var recordSkippedTools = map[string]bool{
	"route_to_squad":    true,
	"handoff_to_router": true,
	"ask_squad":         true,
}

// toolRecorder observes a turn's SSE frames and builds the tool-call record
// persisted with the turn (sessions.ToolCallRecord), so the web UI can
// re-render the tool runs after a reload. It tracks the visible reply length
// (token/message frames, in UTF-16 units) to anchor each call in the text.
// Used from the single goroutine that emits the turn's frames.
type toolRecorder struct {
	calls  []sessions.ToolCallRecord
	byID   map[string]*sessions.ToolCallRecord // top-level call id → record
	child  map[string]*sessions.ToolCallRecord // sub-agent call id → record
	offset int
}

// wrap returns a sink that records the frame, then forwards it unchanged.
func (r *toolRecorder) wrap(next func(string, []byte)) func(string, []byte) {
	return func(event string, data []byte) {
		r.observe(event, data)
		next(event, data)
	}
}

type recFrame struct {
	Text       string          `json:"text"`
	Name       string          `json:"name"`
	Agent      string          `json:"agent"`
	CallID     string          `json:"call_id"`
	Args       json.RawMessage `json:"args"`
	Response   json.RawMessage `json:"response"`
	Error      string          `json:"error"`
	DurationMs int64           `json:"duration_ms"`
}

func (r *toolRecorder) observe(event string, data []byte) {
	switch event {
	case "token", "message", "tool_call", "tool_result", "agent_tool_call",
		"agent_tool_result", "agent_tool_error":
	default:
		return
	}
	var f recFrame
	if json.Unmarshal(data, &f) != nil {
		return
	}
	if r.byID == nil {
		r.byID = map[string]*sessions.ToolCallRecord{}
		r.child = map[string]*sessions.ToolCallRecord{}
	}
	switch event {
	case "token", "message":
		r.offset += len(utf16.Encode([]rune(f.Text)))
	case "tool_call":
		if recordSkippedTools[f.Name] || len(r.calls) >= maxRecordedCalls {
			return
		}
		r.calls = append(r.calls, sessions.ToolCallRecord{
			CallID: f.CallID, Name: f.Name, Args: capJSON(f.Args), Offset: r.offset,
		})
		// Re-index: append may have moved the backing array.
		r.byID = map[string]*sessions.ToolCallRecord{}
		for i := range r.calls {
			if id := r.calls[i].CallID; id != "" {
				r.byID[id] = &r.calls[i]
			}
		}
	case "tool_result":
		if c := r.byID[f.CallID]; c != nil {
			r.resolve(c, f)
		}
	case "agent_tool_call":
		// Attach to the most recent top-level call, as the live UI does.
		if len(r.calls) == 0 {
			return
		}
		p := &r.calls[len(r.calls)-1]
		if len(p.Children) >= maxRecordedChildren {
			return
		}
		p.Children = append(p.Children, sessions.ToolCallRecord{
			CallID: f.CallID, Name: f.Name, Agent: f.Agent, Args: capJSON(f.Args), Offset: p.Offset,
		})
		r.child = map[string]*sessions.ToolCallRecord{}
		for i := range p.Children {
			if id := p.Children[i].CallID; id != "" {
				r.child[id] = &p.Children[i]
			}
		}
	case "agent_tool_result", "agent_tool_error":
		if c := r.child[f.CallID]; c != nil {
			r.resolve(c, f)
		}
	}
}

func (r *toolRecorder) resolve(c *sessions.ToolCallRecord, f recFrame) {
	c.DurationMs = f.DurationMs
	if f.Error != "" {
		c.Error = f.Error
	}
	if len(f.Response) > 0 {
		var resp map[string]any
		if json.Unmarshal(f.Response, &resp) == nil {
			if e, ok := resp["error"].(string); ok && e != "" {
				c.Error = e
			}
		}
		c.Result = capJSON(f.Response)
	}
}

// take returns the recorded calls and resets the recorder for the next turn.
func (r *toolRecorder) take() []sessions.ToolCallRecord {
	out := r.calls
	*r = toolRecorder{}
	return out
}

// capJSON keeps a JSON value under maxRecordedBytes; a larger one is replaced
// by a JSON string holding its truncated text and an ellipsis.
func capJSON(v json.RawMessage) json.RawMessage {
	if len(v) == 0 || string(v) == "null" {
		return nil
	}
	if len(v) <= maxRecordedBytes {
		return v
	}
	cut := []rune(string(v[:maxRecordedBytes]))
	if len(cut) > 0 {
		cut = cut[:len(cut)-1] // drop a possibly split rune
	}
	b, _ := json.Marshal(string(cut) + "…")
	return b
}

// injectedMemoryRecorder records the memory_save / memory_delete calls of an
// injected turn (mailbox, background, scheduled, loop, spawned task, durable
// resume). Those turns emit no SSE frames, so toolRecorder never sees them; this
// reads the turn's ADK events instead, so the web UI replays the memory chip
// after a reload. Only the writes are kept: a read shows nothing anyway.
type injectedMemoryRecorder struct {
	calls []sessions.ToolCallRecord
}

func recordedMemoryWrite(name string) bool {
	return name == "memory_save" || name == "memory_delete"
}

// call records a memory write; offset is the UTF-16 length of the hop's reply
// text produced before it.
func (r *injectedMemoryRecorder) call(fc *genai.FunctionCall, offset int) {
	if fc == nil || !recordedMemoryWrite(fc.Name) || len(r.calls) >= maxRecordedCalls {
		return
	}
	args, _ := json.Marshal(fc.Args)
	r.calls = append(r.calls, sessions.ToolCallRecord{
		CallID: fc.ID, Name: fc.Name, Args: capJSON(args), Offset: offset,
	})
}

// result attaches a response to its call (by call id, else the latest
// unresolved call of that name).
func (r *injectedMemoryRecorder) result(fr *genai.FunctionResponse) {
	if fr == nil || !recordedMemoryWrite(fr.Name) {
		return
	}
	var c *sessions.ToolCallRecord
	for i := len(r.calls) - 1; i >= 0; i-- {
		if fr.ID != "" && r.calls[i].CallID == fr.ID {
			c = &r.calls[i]
			break
		}
		if c == nil && r.calls[i].Name == fr.Name && r.calls[i].Result == nil {
			c = &r.calls[i]
		}
	}
	if c == nil {
		return
	}
	resp, _ := json.Marshal(fr.Response)
	c.Result = capJSON(resp)
	if e, ok := fr.Response["error"].(string); ok && e != "" {
		c.Error = e
	}
}

// utf16Len is the length of s in UTF-16 code units (what a JS String slices by).
func utf16Len(s string) int { return len(utf16.Encode([]rune(s))) }

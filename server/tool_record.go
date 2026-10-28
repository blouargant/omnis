package main

import (
	"encoding/json"
	"unicode/utf16"

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

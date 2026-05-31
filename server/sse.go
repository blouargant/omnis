package main

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/session"
	"google.golang.org/genai"

	"github.com/blouargant/yoke/core/events"
	"github.com/blouargant/yoke/internal/fileref"
	"github.com/blouargant/yoke/internal/sessions"
)

// messageRequest is the JSON body expected by POST /api/sessions/:id/messages.
type messageRequest struct {
	Prompt string   `json:"prompt"`
	Files  []string `json:"files,omitempty"` // absolute paths of uploaded files
}

// handleMessages drives one user turn against the lead agent and streams the
// resulting events back as Server-Sent Events. Browser disconnects propagate
// via the request context and abort the underlying r.Run.
func handleMessages(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		meta, ok := d.Registry.Get(id)
		if !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
			return
		}
		// Archived sessions are read-only: reject new turns until unarchived.
		if meta.Archived {
			c.JSON(http.StatusConflict, gin.H{"error": "session is archived; unarchive it to continue the conversation"})
			return
		}

		var req messageRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON body"})
			return
		}
		if strings.TrimSpace(req.Prompt) == "" && len(req.Files) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "prompt or files are required"})
			return
		}

		// SSE response headers.
		h := c.Writer.Header()
		h.Set("Content-Type", "text/event-stream")
		h.Set("Cache-Control", "no-cache")
		h.Set("Connection", "keep-alive")
		h.Set("X-Accel-Buffering", "no")
		c.Writer.WriteHeader(http.StatusOK)
		c.Writer.Flush()

		// Subscribe to sub-agent bus events before starting the run so no
		// early events are missed.
		subCh := d.AgentEvents.subscribe()
		defer d.AgentEvents.unsubscribe(subCh)

		ctx := c.Request.Context()

		// Serialise with any background mailbox-push turn for this session.
		if d.RunGuard != nil {
			release := d.RunGuard.acquire(meta.ID)
			defer release()
		}

		// We hold the run-guard, so no other turn is in flight for this
		// session. Migrate the pin to the current agent generation so a
		// reload that happened between turns takes effect immediately
		// (otherwise the session would stay on its old generation until the
		// idle-rebind scanner releases it, which can be many seconds).
		d.Manager.MigrateToCurrent(meta.ID)

		// Resolve the session's squad inside the (now-current) generation.
		sq := d.Manager.LookupSquad(meta.ID, meta.Squad)
		if sq == nil || sq.Runner == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "agent generation not available"})
			return
		}
		allowFileAttachments := sq.LeaderAllowFileAttachments

		parts := []*genai.Part{{Text: req.Prompt}}
		var toolPaths []string
		for _, fp := range req.Files {
			mime := imageMIME(fp)
			if allowFileAttachments && mime != "" {
				data, err := os.ReadFile(fp)
				if err != nil {
					log.Printf("server: skipping unreadable file %q: %v", fp, err)
					continue
				}
				data, mime = shrinkIfNeeded(data, mime)
				parts = append(parts, &genai.Part{
					InlineData: &genai.Blob{MIMEType: mime, Data: data},
				})
			} else {
				toolPaths = append(toolPaths, fp)
			}
		}
		if len(toolPaths) > 0 {
			note := "\n\n[Attached files — use the `mime` and `read` tools to inspect them before processing]"
			for _, p := range toolPaths {
				note += "\n- " + p
			}
			parts[0] = &genai.Part{Text: req.Prompt + note}
		}

		// Inline the content of any "@path" file references typed in the prompt,
		// resolved against the session's interactive working directory.
		if note := fileref.Context(req.Prompt, bashCwd.get(meta.ID)); note != "" {
			parts = append(parts, &genai.Part{Text: note})
		}

		seq := sq.Runner.Run(ctx, meta.UserID, meta.ID,
			&genai.Content{Role: "user", Parts: parts},
			agent.RunConfig{StreamingMode: agent.StreamingModeSSE})

		d.Registry.Touch(meta.ID)
		assistantText := streamEvents(ctx, c.Writer, seq, subCh)
		if ctx.Err() == nil && strings.TrimSpace(assistantText) != "" {
			if err := sessions.AppendConversationTurn(meta.ID, req.Prompt, assistantText); err != nil {
				log.Printf("server: failed to persist turn: %v", err)
			}
		}
	}
}

// streamEvents adapts an ADK event iterator into SSE frames written to w,
// interleaved with sub-agent tool events from the shared event bus.
// It returns the full assistant text produced during the turn.
func streamEvents(
	ctx context.Context,
	w io.Writer,
	seq func(yield func(*session.Event, error) bool),
	subCh <-chan agentBusEvent,
) string {
	debug := strings.EqualFold(os.Getenv("YOKE_DEBUG"), "true") || os.Getenv("YOKE_DEBUG") == "1"
	streamStart := time.Now()
	var firstTokenAt time.Time
	var tokenCount int
	var tokenBytes int

	var assistantBuf strings.Builder
	flusher, _ := w.(interface{ Flush() })
	flush := func() {
		if flusher != nil {
			flusher.Flush()
		}
	}
	emit := func(event string, payload any) {
		data, err := json.Marshal(payload)
		if err != nil {
			return
		}
		_, _ = io.WriteString(w, "event: "+event+"\ndata: "+string(data)+"\n\n")
		flush()
	}
	emitDone := func() {
		total := time.Since(streamStart)
		var ttfbMs int64
		var tokPerSec float64
		if !firstTokenAt.IsZero() {
			ttfbMs = firstTokenAt.Sub(streamStart).Milliseconds()
			streamDur := total - firstTokenAt.Sub(streamStart)
			if streamDur > 0 {
				tokPerSec = float64(tokenCount) / streamDur.Seconds()
			}
		}
		emit("debug_timing", map[string]any{
			"ttfb_ms":     ttfbMs,
			"total_ms":    total.Milliseconds(),
			"tokens":      tokenCount,
			"token_bytes": tokenBytes,
			"tok_per_sec": tokPerSec,
		})
		if debug {
			log.Printf("server: stream timing ttfb=%dms total=%dms tokens=%d bytes=%d tok/s=%.1f",
				ttfbMs, total.Milliseconds(), tokenCount, tokenBytes, tokPerSec)
		}
		emit("done", map[string]any{})
	}

	// Convert the rangefunc ADK iterator to a channel so we can select on it
	// alongside the sub-agent bus event channel.
	type adkEvt struct {
		ev  *session.Event
		err error
	}
	adkCh := make(chan adkEvt, 4)
	go func() {
		defer close(adkCh)
		seq(func(ev *session.Event, err error) bool {
			select {
			case adkCh <- adkEvt{ev, err}:
				return err == nil
			case <-ctx.Done():
				return false
			}
		})
	}()

	emitBusEvent := func(be agentBusEvent) {
		p := be.Payload
		agentName, _ := p["agent"].(string)
		toolName, _ := p["tool"].(string)
		switch be.Event {
		case events.EventBeforeTool:
			args, _ := p["input"].(map[string]any)
			callID, _ := p["call_id"].(string)
			emit("agent_tool_call", map[string]any{
				"agent":   agentName,
				"name":    toolName,
				"args":    args,
				"call_id": callID,
			})
		case events.EventAfterTool:
			resp, _ := p["output"].(map[string]any)
			dur, _ := p["duration"].(time.Duration)
			callID, _ := p["call_id"].(string)
			emit("agent_tool_result", map[string]any{
				"agent":       agentName,
				"name":        toolName,
				"response":    resp,
				"duration_ms": dur.Milliseconds(),
				"call_id":     callID,
			})
		case events.EventToolError:
			errMsg, _ := p["error"].(string)
			callID, _ := p["call_id"].(string)
			emit("agent_tool_error", map[string]any{
				"agent":   agentName,
				"name":    toolName,
				"error":   errMsg,
				"call_id": callID,
			})
		case events.EventCompressionSkipped:
			tokens, _ := p["tokens"].(int)
			soft, _ := p["soft"].(int)
			hard, _ := p["hard"].(int)
			window, _ := p["window"].(int)
			emit("context_usage", map[string]any{
				"tokens_used":   tokens,
				"soft_limit":    soft,
				"hard_limit":    hard,
				"window_tokens": window,
			})
		case events.EventCompressionEnd:
			after, _ := p["tokens_after"].(int)
			soft, _ := p["soft"].(int)
			hard, _ := p["hard"].(int)
			window, _ := p["window"].(int)
			emit("context_usage", map[string]any{
				"tokens_used":   after,
				"soft_limit":    soft,
				"hard_limit":    hard,
				"window_tokens": window,
			})
		case events.EventAfterModel:
			// Sub-agent model call (leader is handled via the ADK event stream below).
			usage, _ := p["usage"].(map[string]any)
			if usage == nil {
				return
			}
			emit("turn_usage", map[string]any{
				"agent":         agentName,
				"prompt_tokens": usage["prompt_tokens"],
				"output_tokens": usage["candidates_tokens"],
			})
		case events.EventAskUser:
			// Forward the full question payload so the browser can render
			// the question widget.
			emit("ask_user", p)
		case events.EventAskUserCancel:
			emit("ask_user_cancel", map[string]any{
				"question_id": p["question_id"],
				"session_id":  p["session_id"],
			})
		}
	}

	sawPartialText := false
	for {
		select {
		case <-ctx.Done():
			return assistantBuf.String()

		case be := <-subCh:
			emitBusEvent(be)

		case aev, ok := <-adkCh:
			if !ok {
				// ADK stream finished — drain any buffered sub-agent events.
				for {
					select {
					case be := <-subCh:
						emitBusEvent(be)
					default:
						emitDone()
						log.Printf("server: stream complete")
						return assistantBuf.String()
					}
				}
			}
			if aev.err != nil {
				emit("error", map[string]string{"message": aev.err.Error()})
				emitDone()
				return assistantBuf.String()
			}
			ev := aev.ev
			if ev == nil || ev.Content == nil {
				continue
			}
			isPartial := ev.LLMResponse.Partial
			for _, p := range ev.Content.Parts {
				if p == nil {
					continue
				}
				if p.Text != "" {
					if !isPartial && sawPartialText && p.FunctionCall == nil {
						// ADK emits the full streamed text again in a final non-partial
						// event; skip it to avoid sending duplicate content to the client.
					} else if isPartial {
						if firstTokenAt.IsZero() {
							firstTokenAt = time.Now()
						}
						tokenCount++
						tokenBytes += len(p.Text)
						emit("token", map[string]string{"text": p.Text})
						assistantBuf.WriteString(p.Text)
						sawPartialText = true
					} else {
						emit("message", map[string]string{"text": p.Text})
						assistantBuf.WriteString(p.Text)
					}
				}
				if p.FunctionCall != nil {
					emit("tool_call", map[string]any{
						"name":    p.FunctionCall.Name,
						"args":    p.FunctionCall.Args,
						"call_id": p.FunctionCall.ID,
					})
					sawPartialText = false
				}
				if p.FunctionResponse != nil {
					emit("tool_result", map[string]any{
						"name":     p.FunctionResponse.Name,
						"response": p.FunctionResponse.Response,
						"call_id":  p.FunctionResponse.ID,
					})
					sawPartialText = false
				}
			}
			if !isPartial {
				sawPartialText = false
				// Emit leader token counts so the browser can accumulate session cost.
				if u := ev.LLMResponse.UsageMetadata; u != nil {
					emit("turn_usage", map[string]any{
						"agent":         "leader",
						"prompt_tokens": int64(u.PromptTokenCount),
						"output_tokens": int64(u.CandidatesTokenCount),
					})
				}
			}
		}
	}
}

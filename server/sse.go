package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/session"
	"google.golang.org/genai"

	toolkitagent "github.com/blouargant/yoke/agent"
	"github.com/blouargant/yoke/core/events"
	fstools "github.com/blouargant/yoke/core/tools"
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

		// Serialise with any background mailbox-push turn for this session. We
		// acquire before responding so a second user message blocks until the
		// in-flight turn finishes; ownership of the release then passes to the
		// producer goroutine below, which holds the guard for the whole run.
		release := func() {}
		if d.RunGuard != nil {
			release = d.RunGuard.acquire(meta.ID)
		}

		// We hold the run-guard, so no other turn is in flight for this
		// session. Migrate the pin to the current agent generation so a
		// reload that happened between turns takes effect immediately
		// (otherwise the session would stay on its old generation until the
		// idle-rebind scanner releases it, which can be many seconds).
		d.Manager.MigrateToCurrent(meta.ID)

		// Resolve the starting squad inside the (now-current) generation — both
		// to decide attachment handling and as the entry point for the Omnis
		// routing dispatch loop below.
		startSquad := meta.Squad
		sq := d.Manager.LookupSquad(meta.ID, startSquad)
		if sq == nil || sq.Runner == nil {
			release()
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "agent generation not available"})
			return
		}
		allowFileAttachments := sq.LeaderAllowFileAttachments

		// The session's interactive working directory (the cwd the "!cd"
		// shell-escape and the Folders panel mutate). Carried on the run context
		// so it reaches the agent's file tools and any sub-agents.
		cwd := bashCwd.get(meta.ID)

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

		// The router's clean view: just the user's words plus, when files are
		// attached, a neutral one-line note that an attachment exists. The router
		// has no file tools, so it must NOT be shown the "use the mime/read tools"
		// attachment note baked into `parts` (that made it try to "read" the PDF
		// and then hallucinate an "update your plan?" step) nor the inlined
		// @file/image payloads. The full `parts` still forward to the answering
		// squad via RunWithRouting.
		routerParts := []*genai.Part{{Text: req.Prompt}}
		if len(req.Files) > 0 {
			routerParts = append(routerParts, &genai.Part{
				Text: fmt.Sprintf("\n\n[The user attached %d file(s). You cannot open attachments yourself — treat this as a signal to route to a squad that can read documents/images.]", len(req.Files)),
			})
		}

		// Run the turn on a background context so a client disconnect (a proxy
		// idle-timeout, a closed tab, a Wi-Fi blip) never aborts it. Only the Stop
		// button (the cancel endpoint, via lt.cancel) or server shutdown cancels
		// runCtx. The turn's SSE frames are buffered in the liveTurn so a
		// reconnecting client can replay whatever it missed.
		runCtx, cancel := context.WithCancel(d.rootCtx)
		runCtx = fstools.WithCwd(runCtx, cwd)
		lt := d.LiveTurns.start(meta.ID, cancel)

		// Producer: drives the (possibly multi-hop) turn to completion regardless
		// of whether any client is still attached. It owns the run-guard release.
		go func() {
			defer release()
			defer cancel()
			defer d.LiveTurns.release(meta.ID, lt)

			// Subscribe to sub-agent bus events for the lifetime of the run (not
			// the request) so no events are missed and the subscription is dropped
			// when the run — not the client connection — ends.
			subCh := d.AgentEvents.subscribe()
			defer d.AgentEvents.unsubscribe(subCh)

			d.Registry.Touch(meta.ID)

			sink := func(event string, data []byte) { lt.emit(event, data) }
			emitFrame := func(event string, payload any) {
				if data, err := json.Marshal(payload); err == nil {
					lt.emit(event, data)
				}
			}

			routerSquad := d.Manager.RouterSquad()
			// Per-agent token usage accumulated across every hop of this turn, so
			// the web UI's per-agent cost breakdown can be restored after a server
			// restart / page reload (it is otherwise built only from the live
			// `turn_usage` events and lost on reload). Persisted on the turn below.
			usageAccum := map[string]sessions.TokenUsage{}
			// runHop streams one squad turn (one Runner.Run) and returns its
			// assistant text. The Omnis dispatch loop calls it once per hop.
			//
			// The router hop is special: the model often emits chatter ("Routed to
			// the default squad; it will take over…") alongside its route_to_squad
			// call, despite the instruction telling it not to. Instructions can't
			// guarantee silence, so we suppress the router hop's text at the stream
			// level and decide afterwards: if it recorded a route
			// (Manager.PendingRoute), the text was chatter — discard it from BOTH
			// the chat and the persisted turn (return ""); if it did NOT route, the
			// text is a genuine reply to the user (a clarifying question), so flush
			// it now. Routing is signalled to the user only by the `routing` chip
			// (notify) and the answering squad's reply.
			runHop := func(rctx context.Context, hopSq *toolkitagent.SquadInstance, squadName string, hopParts []*genai.Part) (string, error) {
				seq := hopSq.Runner.Run(rctx, meta.UserID, meta.ID,
					&genai.Content{Role: "user", Parts: hopParts},
					agent.RunConfig{StreamingMode: agent.StreamingModeSSE})
				// The hop's runner-root agent name (squad leader, or the single
				// member of a leaderless squad). Used to attribute the root's usage
				// correctly and suppress its duplicate bus events.
				rootAgent := ""
				if hopSq.Leader != nil {
					rootAgent = hopSq.Leader.Name()
				}
				isRouter := routerSquad != "" && squadName == routerSquad
				if !isRouter {
					return streamEvents(rctx, sink, seq, subCh, cwd, rootAgent, false, usageAccum)
				}
				text, err := streamEvents(rctx, sink, seq, subCh, cwd, rootAgent, true /*suppressText*/, usageAccum)
				if err != nil {
					return text, err
				}
				if d.Manager.PendingRoute(meta.ID) {
					return "", nil // routed → drop the router's chatter entirely
				}
				// Router chose to talk to the user (no route): show its reply now.
				if strings.TrimSpace(text) != "" {
					emitFrame("message", map[string]string{"text": text})
				}
				return text, nil
			}
			// notify fires when control moves to another squad: persist the new
			// squad on the session (so the next turn resumes there and it survives
			// a restart) and tell the browser so it can update the squad label live.
			notify := func(from, to, reason string) {
				d.Registry.SetSquad(meta.ID, to)
				emitFrame("routing", map[string]any{"from": from, "to": to, "reason": reason})
			}

			_, assistantText, runErr := d.Manager.RunWithRouting(runCtx, meta.UserID, meta.ID, startSquad, parts, routerParts, runHop, notify)
			if runErr != nil {
				log.Printf("server: routing run error: %v", runErr)
			}
			// Terminal event for the (possibly multi-hop) turn — streamEvents no
			// longer emits it per hop.
			emitFrame("done", map[string]any{})

			// Persist whatever assistant text was streamed, even when the run was
			// cancelled (Stop button) or no client is attached. Disk I/O does not
			// depend on any request context, so the save lands regardless; we keep
			// only the non-empty check so an aborted turn with no output is not
			// persisted as a blank exchange. Persist BEFORE finish() so a reconnect
			// that races completion always finds the durable turn.
			if strings.TrimSpace(assistantText) != "" {
				if err := sessions.AppendConversationTurnWithUsage(meta.ID, req.Prompt, assistantText, usageAccum); err != nil {
					log.Printf("server: failed to persist turn: %v", err)
				}
			}
			lt.finish()
		}()

		// Consumer: stream the buffered + live frames to THIS client until the
		// turn finishes or the client disconnects. Returning here ends only this
		// HTTP response — the producer goroutine keeps running, and the client can
		// reconnect via GET /messages/stream?from=<lastSeq> to replay the rest.
		lt.stream(c.Request.Context(), c.Writer, c.Writer.Flush, 0)
	}
}

// handleMessageStream re-attaches a client to an in-flight turn's SSE stream
// after its original POST connection dropped. It replays every buffered frame
// with seq greater than the `from` query param, then streams live frames until
// the turn finishes. When no turn is in flight (it already completed and its
// buffer was released, or none ever started) it returns 204 so the client falls
// back to reloading the session history.
func handleMessageStream(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		if _, ok := d.Registry.Get(id); !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
			return
		}
		lt := d.LiveTurns.get(id)
		if lt == nil {
			c.Status(http.StatusNoContent)
			return
		}
		from := 0
		if v := c.Query("from"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				from = n
			}
		}
		h := c.Writer.Header()
		h.Set("Content-Type", "text/event-stream")
		h.Set("Cache-Control", "no-cache")
		h.Set("Connection", "keep-alive")
		h.Set("X-Accel-Buffering", "no")
		c.Writer.WriteHeader(http.StatusOK)
		c.Writer.Flush()
		lt.stream(c.Request.Context(), c.Writer, c.Writer.Flush, from)
	}
}

// handleCancel aborts the in-flight run for a session (the Stop button). It is
// idempotent — cancelling when no turn is live is a no-op.
func handleCancel(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		cancelled := d.LiveTurns.cancel(id)
		c.JSON(http.StatusOK, gin.H{"cancelled": cancelled})
	}
}

// streamEvents adapts an ADK event iterator into SSE frames pushed to sink
// (event name + marshalled JSON), interleaved with sub-agent tool events from
// the shared event bus. The sink buffers and fans the frames out to any attached
// HTTP consumers (see liveTurn). It streams
// ONE squad turn (one Runner.Run); the caller emits the terminal "done" event
// once, after the routing dispatch loop finishes, so a multi-hop turn is not
// prematurely closed. It returns the assistant text produced this hop and a
// non-nil error when the ADK stream itself errored (so the dispatch loop stops).
// suppressText, when true, withholds the assistant text frames ("token" /
// "message") from the client while still accumulating the text into the returned
// string (and still streaming tool calls, ask_user, bus events, and timing). The
// router hop uses it so its routing chatter never reaches the chat — the caller
// decides afterwards (via Manager.PendingRoute) whether the buffered text was a
// route (discard) or a genuine reply (flush).
// usageAccum, when non-nil, accumulates the per-agent token usage emitted as
// `turn_usage` events during this hop (agent name → counts), so the caller can
// persist it on the turn and the web UI's per-agent cost breakdown survives a
// restart / reload. It is fed from the exact same data as the live events, so
// persisted totals match what the browser accumulated live.
// rootAgent is the name of this hop's runner-root agent (the squad leader, or
// the single member of a leaderless squad). The ADK event stream surfaces the
// root's text, tool calls, and usage; the runner-level events plugin ALSO fires
// tool/model callbacks for that same root, so we drop the root's duplicate bus
// events here (keyed on rootAgent) and attribute the ADK-stream usage to it —
// otherwise a squad root named e.g. "omnis"/"knowledge_leader" (which slips past
// the broadcaster's legacy "leader"-only filter) gets counted twice.
func streamEvents(
	ctx context.Context,
	sink func(event string, data []byte),
	seq func(yield func(*session.Event, error) bool),
	subCh <-chan agentBusEvent,
	cwd string,
	rootAgent string,
	suppressText bool,
	usageAccum map[string]sessions.TokenUsage,
) (string, error) {
	if rootAgent == "" {
		rootAgent = "leader"
	}
	debug := strings.EqualFold(os.Getenv("YOKE_DEBUG"), "true") || os.Getenv("YOKE_DEBUG") == "1"
	addUsage := func(agent string, prompt, output int64) {
		if usageAccum == nil || agent == "" {
			return
		}
		u := usageAccum[agent]
		u.Prompt += prompt
		u.Output += output
		usageAccum[agent] = u
	}
	streamStart := time.Now()
	var firstTokenAt time.Time
	var tokenCount int
	var tokenBytes int

	var assistantBuf strings.Builder
	// lastContentAt is bumped by every content-bearing emit (anything but the
	// liveness heartbeat); the heartbeat ticker reads it to decide whether the
	// turn has gone quiet. Touched only from the single select loop below, so no
	// synchronisation is needed.
	lastContentAt := time.Now()
	emit := func(event string, payload any) {
		data, err := json.Marshal(payload)
		if err != nil {
			return
		}
		sink(event, data)
		if event != "heartbeat" {
			lastContentAt = time.Now()
		}
	}
	// emitTiming emits the per-hop debug_timing frame. The terminal "done" event
	// is emitted by the caller once the whole (possibly multi-hop) turn finishes.
	emitTiming := func() {
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
	}

	// Track file-mutating tool calls (Write/Edit/revert) by call id so a
	// successful completion can tell the web UI to live-refresh any open Monaco
	// editor showing that file. The path is resolved against the session's
	// working directory here (where the tools actually run) so the browser can
	// match it to an editor tab keyed by absolute path.
	pendingFileEdits := map[string]string{} // call_id → absolute path
	noteFileTool := func(name, callID string, args map[string]any) {
		switch strings.ToLower(name) {
		case "write", "edit", "revert":
		default:
			return
		}
		if callID == "" || args == nil {
			return
		}
		fp, _ := args["file_path"].(string)
		if fp == "" {
			return
		}
		if cwd != "" && !filepath.IsAbs(fp) {
			fp = filepath.Join(cwd, fp)
		}
		pendingFileEdits[callID] = fp
	}
	emitFileChanged := func(callID string, resp map[string]any) {
		path, ok := pendingFileEdits[callID]
		if !ok {
			return
		}
		delete(pendingFileEdits, callID)
		// The file tools report failures as a result string starting with
		// "Error " and leave the file untouched — don't refresh on those.
		if res, _ := resp["result"].(string); strings.HasPrefix(res, "Error") {
			return
		}
		emit("file_changed", map[string]any{"path": path})
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
		// The runner-level events plugin fires tool/model callbacks for the
		// runner's ROOT agent too. The ADK event stream already surfaces those
		// (tool_call/tool_result frames + the turn_usage emitted below), so drop
		// the root's duplicates here. Scoped to tool/model events so ask_user /
		// compression events (no/other agent) are never affected.
		if agentName != "" && agentName == rootAgent {
			switch be.Event {
			case events.EventBeforeTool, events.EventAfterTool,
				events.EventToolError, events.EventAfterModel:
				return
			}
		}
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
			noteFileTool(toolName, callID, args)
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
			emitFileChanged(callID, resp)
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
			// Sub-agent model usage. The runner-root's own EventAfterModel is
			// dropped above (agentName == rootAgent); the root's usage is emitted
			// from the ADK event stream instead, so this only reaches here for
			// genuine sub-agents.
			usage, _ := p["usage"].(map[string]any)
			if usage == nil {
				return
			}
			prompt, _ := usage["prompt_tokens"].(int64)
			output, _ := usage["candidates_tokens"].(int64)
			addUsage(agentName, prompt, output)
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

	// Liveness heartbeat. While a turn is running but the browser receives no
	// visible content for a stretch — most notably while the model streams a
	// large tool-call argument such as the AGENT.md body written by /init, which
	// the LLM adapter accumulates silently and only surfaces as a completed
	// FunctionCall — the client's status label would otherwise sit on a frozen
	// "streaming…", reading as a stuck turn. A periodic heartbeat carrying the
	// elapsed time lets the web UI show a ticking "working… (Ns)" instead. It
	// carries no content and stops with the stream.
	heartbeat := time.NewTicker(2 * time.Second)
	defer heartbeat.Stop()

	sawPartialText := false
	for {
		select {
		case <-ctx.Done():
			// Client disconnect / turn abort: stop this hop. Returning nil ends
			// the dispatch loop cleanly (no directive to follow).
			return assistantBuf.String(), nil

		case <-heartbeat.C:
			if time.Since(lastContentAt) >= 2*time.Second {
				emit("heartbeat", map[string]any{
					"elapsed_ms": time.Since(streamStart).Milliseconds(),
				})
			}

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
						emitTiming()
						log.Printf("server: stream complete")
						return assistantBuf.String(), nil
					}
				}
			}
			if aev.err != nil {
				emit("error", map[string]string{"message": aev.err.Error()})
				emitTiming()
				return assistantBuf.String(), aev.err
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
						if !suppressText {
							emit("token", map[string]string{"text": p.Text})
						}
						assistantBuf.WriteString(p.Text)
						sawPartialText = true
					} else {
						if !suppressText {
							emit("message", map[string]string{"text": p.Text})
						}
						assistantBuf.WriteString(p.Text)
					}
				}
				if p.FunctionCall != nil {
					emit("tool_call", map[string]any{
						"name":    p.FunctionCall.Name,
						"args":    p.FunctionCall.Args,
						"call_id": p.FunctionCall.ID,
					})
					noteFileTool(p.FunctionCall.Name, p.FunctionCall.ID, p.FunctionCall.Args)
					sawPartialText = false
				}
				if p.FunctionResponse != nil {
					emit("tool_result", map[string]any{
						"name":     p.FunctionResponse.Name,
						"response": p.FunctionResponse.Response,
						"call_id":  p.FunctionResponse.ID,
					})
					emitFileChanged(p.FunctionResponse.ID, p.FunctionResponse.Response)
					sawPartialText = false
				}
			}
			if !isPartial {
				sawPartialText = false
				// Emit leader token counts so the browser can accumulate session cost.
				if u := ev.LLMResponse.UsageMetadata; u != nil {
					addUsage(rootAgent, int64(u.PromptTokenCount), int64(u.CandidatesTokenCount))
					emit("turn_usage", map[string]any{
						"agent":         rootAgent,
						"prompt_tokens": int64(u.PromptTokenCount),
						"output_tokens": int64(u.CandidatesTokenCount),
					})
				}
			}
		}
	}
}

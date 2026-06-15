// routing.go — the "Omnis" router: a leaderless squad whose only job is to
// hand the conversation to the squad best suited to the user's request, then
// step out of the way. Unlike a coordinating leader (which orchestrates its
// own members and keeps control), the router transfers *control of the
// conversation*: after it routes, the chosen squad's leader answers directly.
//
// The mechanism is host-side and deliberately tiny. Two tools record a
// per-session directive — `route_to_squad` (mounted only on the router) and
// `handoff_to_router` (mounted on every other squad root) — and a shared
// dispatch loop (Manager.RunWithRouting) reads the directive after each turn,
// re-pins the session to the target squad, and re-dispatches within the same
// turn. The agent loop itself is untouched: tools only set state.
//
// Per-squad context retention within a session is a property of the existing
// architecture, not of this file: each SquadInstance owns a private
// session.InMemoryService and is stable across turns within a pinned
// generation, so going A -> B -> A returns the *same* A runner whose history
// still holds A's earlier turns. RunWithRouting only changes which runner is
// invoked — it never rebuilds a squad or resets a session — so that property
// is preserved. The forwarded handoff prompt is appended as a new user turn.
package agent

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
	"google.golang.org/genai"
)

// Directive kinds recorded by the routing tools.
const (
	routeKindRoute   = "route"   // route_to_squad: hand to a named squad
	routeKindHandoff = "handoff" // handoff_to_router: hand back to the router
)

// routerMaxHops bounds how many leader<->router handoffs a single user turn
// may chain through, so a mis-route can't loop forever.
const routerMaxHops = 4

// RouteDirective is a one-shot routing decision recorded by a routing tool and
// consumed by the dispatch loop. It lives in RouteRegistry keyed by session.
type RouteDirective struct {
	Kind   string // routeKindRoute | routeKindHandoff
	Target string // destination squad name (route only; lower-cased)
	Reason string // one-line rationale (surfaced to the UI routing chip / logs)
}

// RouteRegistry holds the pending routing directive per session. It is
// process-wide (lives on Infrastructure, survives hot-reload); directives are
// transient — Set by a tool during a turn and Taken (read+cleared) by the
// dispatch loop immediately after that turn's runner finishes.
type RouteRegistry struct {
	mu sync.Mutex
	m  map[string]*RouteDirective
}

// NewRouteRegistry returns an empty registry.
func NewRouteRegistry() *RouteRegistry {
	return &RouteRegistry{m: map[string]*RouteDirective{}}
}

// Set records the pending directive for sessionID, replacing any previous one.
func (r *RouteRegistry) Set(sessionID string, d *RouteDirective) {
	if r == nil || sessionID == "" || d == nil {
		return
	}
	r.mu.Lock()
	r.m[sessionID] = d
	r.mu.Unlock()
}

// Take returns and clears the pending directive for sessionID (nil when none).
func (r *RouteRegistry) Take(sessionID string) *RouteDirective {
	if r == nil || sessionID == "" {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	d := r.m[sessionID]
	delete(r.m, sessionID)
	return d
}

// Peek returns the pending directive for sessionID WITHOUT clearing it (nil when
// none). It lets a surface ask "did the just-finished router hop record a route?"
// before the dispatch loop Takes the directive — so it can decide whether to show
// or discard the router's streamed text without consuming the directive.
func (r *RouteRegistry) Peek(sessionID string) *RouteDirective {
	if r == nil || sessionID == "" {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.m[sessionID]
}

// ── Routing tools ──────────────────────────────────────────────────────────
//
// NOTE: the tool names below ("route_to_squad", "handoff_to_router") are
// mirrored in core/permissions/permissions.go CheckArgs, which exempts them
// from the permission layer (they record an internal directive with no system
// side effects, so they must never prompt). Keep the names in sync.

type routeIn struct {
	Squad  string `json:"squad" jsonschema:"the squad to hand the conversation to; must be one of the available squads listed in your instruction"`
	Reason string `json:"reason" jsonschema:"one short line on why this squad fits; shown to the user as the routing reason"`
}
type routeOut struct {
	Result string `json:"result"`
}

// routeToSquadTool builds the router-only `route_to_squad` tool. validTargets
// is the set of squad names the router may route to (every squad except the
// router itself); the tool rejects anything outside it so the model self-corrects.
func routeToSquadTool(reg *RouteRegistry, validTargets []string) tool.Tool {
	valid := make(map[string]bool, len(validTargets))
	for _, t := range validTargets {
		valid[lowerTrim(t)] = true
	}
	list := strings.Join(validTargets, ", ")
	t, err := functiontool.New(functiontool.Config{
		Name: "route_to_squad",
		Description: "Hand the conversation to the squad best suited to the user's request. " +
			"The user's ORIGINAL message and any attached files are forwarded to that squad " +
			"automatically and verbatim — you do NOT restate, summarise, translate, or in any way " +
			"rephrase the request (never invent or change details such as product names or numbers). " +
			"After you call this, that squad's leader takes over and answers the user directly — you " +
			"do not reply yourself. Available squads: " + list + ".",
	}, func(ctx tool.Context, in routeIn) (routeOut, error) {
		target := lowerTrim(in.Squad)
		if target == "" || !valid[target] {
			return routeOut{}, fmt.Errorf("unknown squad %q; choose exactly one of: %s", in.Squad, list)
		}
		reg.Set(ctx.SessionID(), &RouteDirective{
			Kind:   routeKindRoute,
			Target: target,
			Reason: strings.TrimSpace(in.Reason),
		})
		return routeOut{Result: "Routed to the " + target + " squad; it will take over and answer the user's original message directly."}, nil
	})
	if err != nil {
		panic(fmt.Errorf("route_to_squad tool: %w", err))
	}
	return t
}

type handoffIn struct {
	Reason string `json:"reason" jsonschema:"one short line explaining why this request is outside your squad's scope"`
}
type handoffOut struct {
	Result string `json:"result"`
}

// handoffToRouterTool builds the `handoff_to_router` tool mounted on every
// non-router squad root when routing is enabled. The squad calls it when the
// user's request is outside its scope; the router then re-routes.
func handoffToRouterTool(reg *RouteRegistry) tool.Tool {
	t, err := functiontool.New(functiontool.Config{
		Name: "handoff_to_router",
		Description: "Hand control of the conversation back to the Omnis router when the user's " +
			"request falls outside your squad's scope and you cannot help with it. The router will " +
			"choose a better-suited squad. The user's original message and any attached files are " +
			"forwarded automatically — you do not restate the request. Do not use this for requests " +
			"you can handle — answer those yourself.",
	}, func(ctx tool.Context, in handoffIn) (handoffOut, error) {
		reg.Set(ctx.SessionID(), &RouteDirective{
			Kind:   routeKindHandoff,
			Reason: strings.TrimSpace(in.Reason),
		})
		return handoffOut{Result: "Control handed back to the router; it will re-route the user's original message."}, nil
	})
	if err != nil {
		panic(fmt.Errorf("handoff_to_router tool: %w", err))
	}
	return t
}

// ── Capability probe (ask_squad) ─────────────────────────────────────────────

type askSquadIn struct {
	Squad   string `json:"squad" jsonschema:"the squad to privately ask; must be one of the available squads listed in your instruction"`
	Request string `json:"request" jsonschema:"the user's request, restated cleanly, that you want this squad to judge against its own scope"`
}
type askSquadOut struct {
	Verdict string `json:"verdict"` // CAN_HANDLE | CANNOT_HANDLE
	Reason  string `json:"reason"`
}

// askSquadTool builds the router-only `ask_squad` tool: a private, hidden
// capability check the router uses when it is unsure which squad fits. It runs
// the target squad's lead as a single isolated LLM call (the lead's own model +
// instruction, NO tools/sub-agents/event-bus) and returns that lead's yes/no
// verdict. validTargets is the same non-router squad list route_to_squad uses.
func askSquadTool(runtime RuntimeSettings, validTargets []string) tool.Tool {
	valid := make(map[string]bool, len(validTargets))
	for _, t := range validTargets {
		valid[lowerTrim(t)] = true
	}
	list := strings.Join(validTargets, ", ")
	t, err := functiontool.New(functiontool.Config{
		Name: "ask_squad",
		Description: "Privately ask a squad's lead whether a request is within its scope, BEFORE you " +
			"commit to routing. Use this ONLY when you are unsure which squad fits — if you are " +
			"confident, call route_to_squad directly. Returns the squad's own verdict (CAN_HANDLE or " +
			"CANNOT_HANDLE) with a one-line reason. This check is private: the user does not see it, " +
			"and it does not hand over the conversation. Available squads: " + list + ".",
	}, func(ctx tool.Context, in askSquadIn) (askSquadOut, error) {
		target := lowerTrim(in.Squad)
		if target == "" || !valid[target] {
			return askSquadOut{}, fmt.Errorf("unknown squad %q; choose exactly one of: %s", in.Squad, list)
		}
		can, reason, err := probeSquadCapability(ctx, runtime, target, strings.TrimSpace(in.Request))
		if err != nil {
			// A hard probe failure (no lead / model build / LLM error) is not a
			// capability decline. Surface it so the router falls back to its own
			// judgment instead of wrongly excluding the squad.
			return askSquadOut{}, fmt.Errorf("could not reach the %s squad to check; use your own judgment: %w", target, err)
		}
		verdict := "CANNOT_HANDLE"
		if can {
			verdict = "CAN_HANDLE"
		}
		return askSquadOut{Verdict: verdict, Reason: reason}, nil
	})
	if err != nil {
		panic(fmt.Errorf("ask_squad tool: %w", err))
	}
	return t
}

// probeSquadCapability runs one squad-lead scope judgment. It resolves the
// squad's lead from the runtime snapshot, builds the lead's model, and issues a
// single non-streamed completion (system = lead instruction, user = the
// capability-check prompt) — no runner, no tools, no event bus, so nothing
// reaches the SSE stream. Returns the parsed verdict, or an error on a hard
// failure (caller decides how to treat that).
func probeSquadCapability(ctx context.Context, runtime RuntimeSettings, squadName, request string) (bool, string, error) {
	sq, ok := runtime.Squad(squadName)
	if !ok {
		return false, "", fmt.Errorf("unknown squad %q", squadName)
	}
	leaderName := sq.Leader
	if leaderName == "" { // leaderless squad → its single member is the lead
		if len(sq.Members) == 0 {
			return false, "", fmt.Errorf("squad %q has no resolvable lead", squadName)
		}
		leaderName = sq.Members[0]
	}
	leadCfg, ok := runtime.AgentConfig(leaderName)
	if !ok {
		return false, "", fmt.Errorf("squad %q lead %q not in agent catalogue", squadName, leaderName)
	}
	m, err := newModelForAgent(ctx, leadCfg)
	if err != nil {
		return false, "", err
	}
	sysInstr := strings.TrimSpace(leadCfg.Instruction)
	if sysInstr == "" {
		sysInstr = defaultAgentInstruction(leadCfg.Name)
	}
	req := &model.LLMRequest{
		Config: &genai.GenerateContentConfig{
			SystemInstruction: &genai.Content{Parts: []*genai.Part{{Text: sysInstr}}},
		},
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: buildCapabilityCheckPrompt(sq.Description, request)}}},
		},
	}
	var out strings.Builder
	for resp, gerr := range m.GenerateContent(ctx, req, false) {
		if gerr != nil {
			return false, "", gerr
		}
		if resp == nil || resp.Content == nil {
			continue
		}
		for _, p := range resp.Content.Parts {
			out.WriteString(p.Text)
		}
	}
	can, reason := parseCapabilityVerdict(out.String())
	return can, reason, nil
}

// buildCapabilityCheckPrompt is the user turn sent to a squad lead during a
// probe. It pins the lead to a one-line verdict and forbids doing the work.
func buildCapabilityCheckPrompt(squadDesc, request string) string {
	var b strings.Builder
	b.WriteString("ROUTING CAPABILITY CHECK — a router is deciding whether to hand you this conversation.\n")
	b.WriteString("Do NOT perform the task and do NOT call any tools. Judge ONLY whether the request is " +
		"within your squad's scope, skills, and tools.\n\n")
	if d := strings.TrimSpace(squadDesc); d != "" {
		b.WriteString("Your squad's stated purpose: " + d + "\n\n")
	}
	b.WriteString("Request to judge:\n")
	b.WriteString(request)
	b.WriteString("\n\nReply with EXACTLY one line, beginning with `CAN_HANDLE:` or `CANNOT_HANDLE:` " +
		"followed by a short reason. Output nothing else.")
	return b.String()
}

// parseCapabilityVerdict reads a squad lead's probe reply. It checks for the
// CANNOT_HANDLE token first (so it isn't shadowed by CAN_HANDLE), then
// CAN_HANDLE; an unparseable reply is treated as CANNOT_HANDLE so the router
// probes another squad or asks the user rather than force-routing.
func parseCapabilityVerdict(text string) (bool, string) {
	t := strings.TrimSpace(text)
	up := strings.ToUpper(t)
	if i := strings.Index(up, "CANNOT_HANDLE"); i >= 0 {
		return false, verdictReason(t, i+len("CANNOT_HANDLE"))
	}
	if i := strings.Index(up, "CAN_HANDLE"); i >= 0 {
		return true, verdictReason(t, i+len("CAN_HANDLE"))
	}
	reason := firstNonEmptyLine(t)
	if reason == "" {
		reason = "no clear verdict from the squad"
	}
	return false, reason
}

// verdictReason extracts the trailing reason after a verdict token, trimming a
// leading separator and capping it to one short line.
func verdictReason(text string, from int) string {
	if from > len(text) {
		from = len(text)
	}
	r := strings.TrimSpace(text[from:])
	r = strings.TrimLeft(r, ":-—. \t")
	r = firstNonEmptyLine(r)
	if len(r) > 200 {
		r = strings.TrimSpace(r[:200])
	}
	return r
}

func firstNonEmptyLine(s string) string {
	for _, ln := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(ln); t != "" {
			return t
		}
	}
	return ""
}

// ── Router squad config injection ────────────────────────────────────────────

// resolveRouterSquadName resolves the configured router squad name from the
// config-file value (pointer: nil == absent) and the YOKE_ROUTER_SQUAD env
// override. Returns "" when routing is explicitly disabled. The default
// (absent + no env) is the built-in router squad name "omnis".
func resolveRouterSquadName(fileValue *string) string {
	name := defaultRouterSquadName // "omnis"
	if fileValue != nil {
		name = *fileValue
	}
	// A non-empty YOKE_ROUTER_SQUAD overrides the config value; an empty/unset
	// env var is ignored (consistent with yoke's other env handling). Set it to
	// "none" to disable routing from the environment.
	if env := strings.TrimSpace(os.Getenv("YOKE_ROUTER_SQUAD")); env != "" {
		name = env
	}
	switch lowerTrim(name) {
	case "", "none", "off", "disabled", "false":
		return ""
	default:
		return lowerTrim(name)
	}
}

// defaultRouterSquadName is the built-in router squad/agent name used when the
// config does not name one and routing is not disabled.
const defaultRouterSquadName = "omnis"

// ensureRouterSquad injects the router agent and a leaderless router squad into
// the resolved settings when routing is enabled and they are not already
// present. This makes Omnis the default for every config (opt-out via
// router_squad: "none") without requiring users to edit agents.json.
//
// It runs in the build path (BuildInstance), not in ResolveRuntimeSettings, so
// config-only unit tests see an unmodified squad list unless they build an
// instance. When no leader agent exists to base the router's model on, routing
// is disabled rather than failing the build.
func ensureRouterSquad(rs *RuntimeSettings) {
	name := lowerTrim(rs.RouterSquad)
	if name == "" {
		return
	}
	if _, ok := rs.AgentConfig(name); !ok {
		cfg, ok := synthesizeRouterAgentConfig(*rs, name)
		if !ok {
			rs.RouterSquad = "" // no leader to model on → disable routing
			return
		}
		rs.Agents = append(rs.Agents, cfg)
	}
	if _, ok := rs.Squad(name); !ok {
		rs.Squads = append(rs.Squads, RuntimeSquadConfig{
			Name:        name,
			Description: "Omnis router — hands your request to the most suitable squad.",
			Leader:      "", // leaderless: the router agent runs directly as the root
			Members:     []string{name},
		})
	}
}

// synthesizeRouterAgentConfig builds an in-memory router agent by copying the
// (fully model-resolved) leader config and stripping it down to a pure router:
// no coordinator role, no skills/MCP, no declared tool groups. Its instruction
// is resolved at build time (ReadAgentInstruction(name) || defaultRouterInstruction).
func synthesizeRouterAgentConfig(rs RuntimeSettings, name string) (RuntimeAgentConfig, bool) {
	leader, ok := rs.LeaderConfig()
	if !ok {
		return RuntimeAgentConfig{}, false
	}
	cfg := leader // value copy carries the resolved provider/model/base-url/api-key
	cfg.Name = name
	cfg.Leader = false
	cfg.BuiltIn = true
	cfg.Enabled = true
	cfg.Description = "Router that hands the conversation to the best-suited squad."
	cfg.Instruction = "" // resolved in buildSquadInstance
	cfg.Tools = nil      // route_to_squad + ask_user/mailbox mounted as always-on
	cfg.Skills = nil
	cfg.SoftSkillsDir = ""
	cfg.MCPConfigPath = ""
	cfg.MCPServers = nil
	cfg.A2AAgents = nil
	cfg.MaxInstances = 1
	return cfg, true
}

// routerSquadCatalogue returns the squad names (excluding the router) that the
// router may route to, in declaration order, lower-cased.
func routerSquadCatalogue(runtime RuntimeSettings) []string {
	out := make([]string, 0, len(runtime.Squads))
	for _, sq := range runtime.Squads {
		if sq.Name == runtime.RouterSquad {
			continue
		}
		out = append(out, sq.Name)
	}
	return out
}

// routerCatalogueBlock renders the available-squads catalogue (name +
// description) prepended to the router's instruction so it can route on grounded
// descriptions rather than guesswork.
func routerCatalogueBlock(runtime RuntimeSettings) string {
	var b strings.Builder
	b.WriteString("## Available squads\n\n")
	b.WriteString("Route the user's request to exactly one of these squads using `route_to_squad`:\n\n")
	for _, sq := range runtime.Squads {
		if sq.Name == runtime.RouterSquad {
			continue
		}
		desc := strings.TrimSpace(sq.Description)
		if desc == "" {
			desc = "(no description)"
		}
		fmt.Fprintf(&b, "- **%s** — %s\n", sq.Name, desc)
	}
	b.WriteString("\n")
	return b.String()
}

// ── Dispatch loop ────────────────────────────────────────────────────────────

// RouteRunFunc executes ONE squad turn for the session: it builds the user
// content from parts, runs sq.Runner, streams/echoes to the surface, and
// returns the assistant text produced. The surface owns all streaming so the
// dispatcher stays UI-agnostic.
type RouteRunFunc func(ctx context.Context, sq *SquadInstance, squadName string, parts []*genai.Part) (assistantText string, err error)

// RouteNotifyFunc is invoked when control moves between squads so the surface
// can show a "→ routed to <to>" indicator. Optional (may be nil).
type RouteNotifyFunc func(from, to, reason string)

// RouterSquad returns the router squad name for the current generation, or ""
// when routing is disabled.
func (m *Manager) RouterSquad() string {
	if m == nil {
		return ""
	}
	if inst := m.Current(); inst != nil {
		return inst.RouterName
	}
	return ""
}

// PendingRoute reports whether a routing directive is currently recorded for
// sessionID (without consuming it). A surface uses it right after a router hop to
// decide whether the router's streamed text was routing chatter (a route is
// pending → discard it) or a genuine reply to the user (no route → show it).
func (m *Manager) PendingRoute(sessionID string) bool {
	if m == nil || m.infra == nil {
		return false
	}
	return m.infra.RouteDirectives.Peek(sessionID) != nil
}

// RunWithRouting drives one user turn through the routing dispatch loop on the
// session's pinned generation. It runs the starting squad; if a routing tool
// recorded a directive during that run it re-pins to the target squad and
// re-dispatches the SAME original user input to it, repeating up to
// routerMaxHops. Returns the squad that produced the final answer and the
// concatenated assistant text across all hops.
//
// Parts handling: every answering (non-router) hop receives the user's original
// `initialParts` (verbatim text + any attached files) unchanged — directives
// decide WHERE control goes, never WHAT the answering squad receives, so the
// real request and its attachments always reach it and the router cannot
// paraphrase, twist, or drop the message. The router hop instead receives
// `routerParts`, a clean text-only view the surface builds: the router has no
// file tools, so it must not be shown attachment payloads or the "use the
// read/mime tools" note baked into `initialParts` (that made it try to "read"
// an attached PDF and then hallucinate an "update your plan?" step). Pass nil
// for `routerParts` to feed the router the original parts too (byte-identical
// to having no separate view).
//
// Invariants that preserve per-squad in-session memory: the loop only resolves
// runners via inst.Squad(name) (stable instances) and appends the same user
// turn — it never rebuilds a squad or resets a session.
func (m *Manager) RunWithRouting(
	ctx context.Context,
	userID, sessionID, startSquad string,
	initialParts []*genai.Part,
	routerParts []*genai.Part,
	run RouteRunFunc,
	notify RouteNotifyFunc,
) (finalSquad string, fullText string, err error) {
	inst := m.Lookup(sessionID)
	if inst == nil {
		return startSquad, "", fmt.Errorf("routing: no agent generation for session %q", sessionID)
	}
	routerSquad := inst.RouterName

	squadName := lowerTrim(startSquad)
	if squadName == "" {
		squadName = inst.DefaultName
	}
	// Surfaces that don't build a separate clean view feed the router the
	// original parts too.
	if routerParts == nil {
		routerParts = initialParts
	}

	reg := m.infra.RouteDirectives
	// Discard any stale directive from a previous (e.g. aborted) turn.
	reg.Take(sessionID)

	var full strings.Builder
	// Squads that handed this turn's request back to the router as out of scope,
	// in first-declined order, with the reason each gave. Re-fed to the router as
	// a synthetic note on its parts (below) so it does not bounce the request
	// back to a squad that just declined it — the bug where a handoff made the
	// router re-route to the same squad until routerMaxHops was exhausted.
	declinedReason := map[string]string{}
	var declinedOrder []string

	for hop := 0; hop < routerMaxHops; hop++ {
		sq := inst.Squad(squadName)
		if sq == nil {
			sq = inst.Default()
			squadName = inst.DefaultName
		}
		if sq == nil || sq.Runner == nil {
			return squadName, full.String(), fmt.Errorf("routing: squad %q unavailable", squadName)
		}

		// The router sees the clean text-only view; every answering squad sees
		// the user's original parts (verbatim text + attachments).
		hopParts := initialParts
		if routerSquad != "" && squadName == routerSquad {
			hopParts = routerParts
			// Tell the router which squads already declined this turn so it
			// re-routes elsewhere (or asks the user) instead of looping back to
			// one that just handed the request back.
			if len(declinedOrder) > 0 {
				hopParts = append(append([]*genai.Part{}, routerParts...),
					routerDeclineNote(declinedOrder, declinedReason))
			}
		}

		text, runErr := run(ctx, sq, squadName, hopParts)
		full.WriteString(text)
		if runErr != nil {
			return squadName, full.String(), runErr
		}

		dir := reg.Take(sessionID)
		if dir == nil || routerSquad == "" {
			break // no routing requested (or routing disabled)
		}

		var next string
		switch dir.Kind {
		case routeKindRoute:
			next = lowerTrim(dir.Target)
		case routeKindHandoff:
			// The squad that just ran handed control back as out of scope.
			// Remember it (and why) so the router hop below is told not to
			// re-route to it.
			if squadName != routerSquad {
				if _, seen := declinedReason[squadName]; !seen {
					declinedOrder = append(declinedOrder, squadName)
				}
				declinedReason[squadName] = dir.Reason
			}
			next = routerSquad
		}
		// Stop on a no-op or unknown target (route_to_squad already validates
		// targets; this is the safety net).
		if next == "" || next == squadName || inst.Squad(next) == nil {
			break
		}

		if notify != nil {
			notify(squadName, next, dir.Reason)
		}
		squadName = next
		// `initialParts` is never reassigned: every answering squad receives the
		// user's ORIGINAL turn input (verbatim text + any attached files), never
		// a router/squad paraphrase. This guarantees the routed squad sees the
		// real request and its attachments, and that the router cannot twist or
		// drop anything (e.g. "Xpeng G6" can't become "VW Golf 6", and the PDF
		// the user attached reaches the squad that answers). The next hop re-reads
		// `routerSquad == squadName` above to decide router-view vs. original.
	}

	return squadName, full.String(), nil
}

// routerDeclineNote builds the synthetic part appended to the router's input
// when one or more squads handed the current turn's request back as out of
// scope. It names those squads (and the reason each gave) and tells the router
// not to route to them again — so a handoff can't bounce the request straight
// back to the squad that just declined it.
func routerDeclineNote(order []string, reasons map[string]string) *genai.Part {
	var b strings.Builder
	b.WriteString("[routing context — not from the user] ")
	b.WriteString("These squads were already tried for this request this turn and handed it back as out of scope: ")
	for i, name := range order {
		if i > 0 {
			b.WriteString("; ")
		}
		b.WriteString(name)
		if r := strings.TrimSpace(reasons[name]); r != "" {
			b.WriteString(" (reason: " + r + ")")
		}
	}
	b.WriteString(". Do NOT route to those squads again. Route to a different squad only if one genuinely fits; " +
		"otherwise ask the user a clarifying question — do not force a route.")
	return &genai.Part{Text: b.String()}
}

// routerHandoffProtocolBlock is appended to every non-router squad leader's
// instruction when routing is enabled, so it knows to return control to the
// router for out-of-scope requests instead of attempting them.
func routerHandoffProtocolBlock() string {
	return "\n\n## Out-of-scope requests\n\n" +
		"You were given this conversation by the Omnis router because it matched your squad. " +
		"Handle everything within your scope directly. If the user later asks for something " +
		"clearly outside your squad's domain and skills, call `handoff_to_router` with their " +
		"request in `prompt` instead of attempting it — the router will pick a better squad. " +
		"Do not hand back requests you can reasonably handle.\n"
}

// defaultRouterInstruction is the built-in router prompt used when no
// registry/agents/<router>/instruction.md ships with the install.
func defaultRouterInstruction() string {
	return strings.TrimSpace(`
You are Omnis, the router. You do not answer questions or use domain tools
yourself — your single job is to send the conversation to the squad best able
to handle the user's request.

Hard limits: you have NO tools beyond route_to_squad, ask_squad, and ask_user.
You CANNOT read files, open PDFs, view images, browse, or run commands — the
squad you route to does that. Never say you will "read", "consult", or "open" an
attachment; never narrate or think out loud; you have no plan and no task list,
so never ask the user to "update a plan" or "update the task". If the user
attached a file you only see a note that one exists (not its contents) — treat
that as a routing signal and route to a squad that can read documents/images.

On each turn:

1. Read the user's message and decide which available squad fits best.
2. If one clearly fits, call ` + "`route_to_squad`" + ` with that squad's name and a
   one-line ` + "`reason`" + `. The user's ORIGINAL message and any attached files are
   forwarded to the squad automatically and verbatim — do NOT restate, summarise,
   translate, or rephrase the request, and NEVER invent or change details (e.g.
   do not turn "Xpeng G6" into "VW Golf 6"). When you route, emit NO text at all —
   just make the route_to_squad call and stop. Do not announce the route
   ("Connecting you to the … squad"), do not echo the request, do not explain what
   the squad will do. The squad answers the user directly.
3. If you are UNSURE which squad fits, verify before committing with
   ` + "`ask_squad(squad, request)`" + ` — a private check (the user does not see it,
   and it does not hand over control) that returns the squad lead's own
   CAN_HANDLE / CANNOT_HANDLE verdict. Route on CAN_HANDLE; on CANNOT_HANDLE try
   the next plausible squad; if every plausible squad declines, do NOT force a
   route — go to step 4. Skip this when you are already confident.
4. If the request is ambiguous, no squad fits, or all candidates declined, do
   NOT route. Reply with a short clarifying question (or use ` + "`ask_user`" + `) so
   the user can tell you what they need. Route only once a suitable squad is clear.

Routing heuristics:

- Match the KIND of help the user wants, not the technology they mention. A
  domain keyword alone (e.g. "fluxcd", "Kubernetes", "Postgres") is not a reason
  to pick a general-purpose squad — decide from what the user wants done.
- Questions about yoke ITSELF or its capabilities — where yoke (or "you") is the
  subject: "is there an agent / skill / tool for X?" (meaning a yoke one), "can
  yoke do X?", "find / install an agent or skill for X", "what can you do?" — go
  to the squad whose description covers answering questions about yoke and
  browsing / installing registry items, EVEN when X is a specialised domain. The
  user is asking whether a yoke capability exists or to get one, not (yet) to
  perform the task.
- World-knowledge / research questions are NOT yoke-capability questions, even
  when phrased "is there a …". "Is there a transparent HTTP proxy in Rust?",
  "what's a good library for X?", "does language Y have a package that does Z?"
  ask about software out in the world, not yoke's own agents/skills/tools —
  route these to the research / fact-finding squad, never the yoke-capabilities
  squad. The tell is the subject: yoke/you → capabilities squad; the world or a
  programming ecosystem → research squad.
- A general-purpose / coordinator squad is a last resort, not a catch-all: route
  there only for open-ended, hands-on work when no more specific squad fits.

Never invent a squad name; choose only from the list in your instruction. Never
restate or rephrase the user's request (it is forwarded verbatim). When you
route, output nothing — just the route_to_squad call; emit text ONLY when you
genuinely need to ask the user a clarifying question (step 4).`)
}

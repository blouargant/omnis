package agent

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/adk/runner"
	"google.golang.org/genai"
)

// fakeSquads builds an Instance with sentinel SquadInstances (non-nil Runner so
// RunWithRouting's availability check passes; the test's run callback is what
// actually "runs" each hop, so the runner is never invoked).
func routingTestManager(router string, squadNames ...string) (*Manager, *Infrastructure) {
	infra := &Infrastructure{RouteDirectives: NewRouteRegistry()}
	inst := &Instance{
		Generation:  1,
		DefaultName: DefaultSquadName,
		RouterName:  router,
		Squads:      map[string]*SquadInstance{},
	}
	for _, n := range squadNames {
		inst.Squads[n] = &SquadInstance{Name: n, Runner: &runner.Runner{}}
		// Mirror the squad into Settings too: the router catalogue (which gates a
		// salvaged route's destination) is resolved from RuntimeSettings, not from
		// the built Squads map.
		inst.Settings.Squads = append(inst.Settings.Squads, RuntimeSquadConfig{Name: n})
	}
	inst.Settings.RouterSquad = router
	return NewManager(infra, inst), infra
}

func TestRouteRegistrySetTake(t *testing.T) {
	r := NewRouteRegistry()
	if d := r.Take("s"); d != nil {
		t.Fatalf("Take on empty = %+v, want nil", d)
	}
	r.Set("s", &RouteDirective{Kind: routeKindRoute, Target: "x"})
	d := r.Take("s")
	if d == nil || d.Target != "x" {
		t.Fatalf("Take = %+v, want target x", d)
	}
	if again := r.Take("s"); again != nil {
		t.Fatalf("Take is not one-shot: %+v", again)
	}
}

func TestRouteRegistryProbeCounter(t *testing.T) {
	// IncProbe bounds ask_squad probes within a turn; ResetProbes clears the
	// budget at the start of each turn. This backstops the (uncapped) ADK flow
	// loop against a router that keeps probing without committing.
	r := NewRouteRegistry()
	for want := 1; want <= 3; want++ {
		if got := r.IncProbe("s"); got != want {
			t.Fatalf("IncProbe #%d = %d, want %d", want, got, want)
		}
	}
	// A different session keeps its own count.
	if got := r.IncProbe("other"); got != 1 {
		t.Fatalf("IncProbe other = %d, want 1 (per-session)", got)
	}
	r.ResetProbes("s")
	if got := r.IncProbe("s"); got != 1 {
		t.Fatalf("IncProbe after reset = %d, want 1", got)
	}
	if got := r.IncProbe("other"); got != 2 {
		t.Fatalf("ResetProbes leaked across sessions: other = %d, want 2", got)
	}
	// Nil-safe and empty-session-safe (matches the rest of the registry API).
	var nilReg *RouteRegistry
	if got := nilReg.IncProbe("s"); got != 0 {
		t.Fatalf("nil IncProbe = %d, want 0", got)
	}
	if got := r.IncProbe(""); got != 0 {
		t.Fatalf("empty-session IncProbe = %d, want 0", got)
	}
}

func TestRouteRegistryPeek(t *testing.T) {
	// Peek must report a pending directive WITHOUT clearing it — surfaces rely on
	// it to decide whether a router hop's text was a route (discard) before the
	// dispatch loop Takes the directive.
	r := NewRouteRegistry()
	if d := r.Peek("s"); d != nil {
		t.Fatalf("Peek on empty = %+v, want nil", d)
	}
	r.Set("s", &RouteDirective{Kind: routeKindRoute, Target: "x"})
	if d := r.Peek("s"); d == nil || d.Target != "x" {
		t.Fatalf("Peek = %+v, want target x", d)
	}
	// Peek did not consume it: a second Peek and a Take both still see it.
	if d := r.Peek("s"); d == nil {
		t.Fatalf("Peek cleared the directive (not idempotent)")
	}
	if d := r.Take("s"); d == nil || d.Target != "x" {
		t.Fatalf("Take after Peek = %+v, want target x still present", d)
	}
}

func TestRunWithRoutingRoutesToTarget(t *testing.T) {
	m, infra := routingTestManager("omnis", "omnis", DefaultSquadName, "research")
	sid := "sess-1"
	var hops []string
	run := func(_ context.Context, sq *SquadInstance, name string, _ []*genai.Part) (string, error) {
		hops = append(hops, name)
		if name == "omnis" {
			infra.RouteDirectives.Set(sid, &RouteDirective{Kind: routeKindRoute, Target: "research"})
			return "", nil
		}
		return "answer from " + name, nil
	}
	final, text, err := m.RunWithRouting(context.Background(), "u", sid, "omnis",
		[]*genai.Part{{Text: "hi"}}, nil, run, nil)
	if err != nil {
		t.Fatalf("RunWithRouting err = %v", err)
	}
	if final != "research" {
		t.Fatalf("final squad = %q, want research", final)
	}
	if want := []string{"omnis", "research"}; !equalStringSlice(hops, want) {
		t.Fatalf("hops = %v, want %v", hops, want)
	}
	if text != "answer from research" {
		t.Fatalf("text = %q, want concatenated answer", text)
	}
}

func TestRunWithRoutingForwardsOriginalParts(t *testing.T) {
	// The routed squad must receive the user's ORIGINAL parts (verbatim text +
	// attachment note) on every hop — never a router paraphrase, and attachments
	// must not be dropped.
	m, infra := routingTestManager("omnis", "omnis", "research")
	sid := "sess-parts"
	initial := []*genai.Part{
		{Text: "open the doors of the Xpeng G6 if the auxiliary battery is dead"},
		{Text: "[Attached files]\n- /tmp/xpeng-g6-manual.pdf"},
	}
	var seen [][]*genai.Part
	run := func(_ context.Context, sq *SquadInstance, name string, parts []*genai.Part) (string, error) {
		seen = append(seen, parts)
		if name == "omnis" {
			infra.RouteDirectives.Set(sid, &RouteDirective{Kind: routeKindRoute, Target: "research"})
		}
		return "", nil
	}
	if _, _, err := m.RunWithRouting(context.Background(), "u", sid, "omnis", initial, nil, run, nil); err != nil {
		t.Fatalf("RunWithRouting err = %v", err)
	}
	if len(seen) != 2 {
		t.Fatalf("expected 2 hops (router + squad), got %d", len(seen))
	}
	for i, got := range seen {
		if len(got) != len(initial) {
			t.Fatalf("hop %d received %d parts, want %d (original)", i, len(got), len(initial))
		}
		for j := range got {
			if got[j].Text != initial[j].Text {
				t.Fatalf("hop %d part %d = %q, want original %q", i, j, got[j].Text, initial[j].Text)
			}
		}
	}
}

func TestRunWithRoutingRouterSeesCleanView(t *testing.T) {
	// The router hop must receive the clean `routerParts` view (no attachment
	// note), while the answering squad still receives the user's original parts
	// (verbatim text + attachment note). This guards the fix for the router
	// trying to "read" an attached PDF and hallucinating an "update your plan?"
	// step because the "use the read/mime tools" note reached its model.
	m, infra := routingTestManager("omnis", "omnis", "research")
	sid := "sess-routerview"
	initial := []*genai.Part{
		{Text: "open the doors of the Xpeng G6 if the auxiliary battery is dead"},
		{Text: "[Attached files — use the mime and read tools to inspect them]\n- /tmp/xpeng-g6-manual.pdf"},
	}
	router := []*genai.Part{
		{Text: "open the doors of the Xpeng G6 if the auxiliary battery is dead"},
		{Text: "[The user attached 1 file(s). You cannot open attachments yourself.]"},
	}
	seen := map[string][]*genai.Part{}
	run := func(_ context.Context, sq *SquadInstance, name string, parts []*genai.Part) (string, error) {
		seen[name] = parts
		if name == "omnis" {
			infra.RouteDirectives.Set(sid, &RouteDirective{Kind: routeKindRoute, Target: "research"})
		}
		return "", nil
	}
	if _, _, err := m.RunWithRouting(context.Background(), "u", sid, "omnis", initial, router, run, nil); err != nil {
		t.Fatalf("RunWithRouting err = %v", err)
	}
	// Router got the clean view (no "read tools" note).
	if got := seen["omnis"]; len(got) != 2 || got[1].Text != router[1].Text {
		t.Fatalf("router hop parts = %v, want clean routerParts", got)
	}
	if strings.Contains(seen["omnis"][1].Text, "read tools") {
		t.Fatalf("router hop must not see the 'use the read tools' note")
	}
	// Answering squad got the original parts, attachment note intact.
	if got := seen["research"]; len(got) != 2 || got[1].Text != initial[1].Text {
		t.Fatalf("answering squad parts = %v, want original initialParts", got)
	}
}

func TestRunWithRoutingHandoffBackToRouter(t *testing.T) {
	m, infra := routingTestManager("omnis", "omnis", "research", "docs")
	sid := "sess-2"
	var hops []string
	run := func(_ context.Context, sq *SquadInstance, name string, _ []*genai.Part) (string, error) {
		hops = append(hops, name)
		switch name {
		case "research":
			// Out of scope → hand back to the router.
			infra.RouteDirectives.Set(sid, &RouteDirective{Kind: routeKindHandoff})
		case "omnis":
			// Router sends it to docs.
			infra.RouteDirectives.Set(sid, &RouteDirective{Kind: routeKindRoute, Target: "docs"})
		}
		return "", nil
	}
	// Start already on the research squad; it hands back, router re-routes to docs.
	final, _, err := m.RunWithRouting(context.Background(), "u", sid, "research",
		[]*genai.Part{{Text: "hi"}}, nil, run, nil)
	if err != nil {
		t.Fatalf("RunWithRouting err = %v", err)
	}
	if final != "docs" {
		t.Fatalf("final squad = %q, want docs", final)
	}
	if want := []string{"research", "omnis", "docs"}; !equalStringSlice(hops, want) {
		t.Fatalf("hops = %v, want %v", hops, want)
	}
}

func TestRunWithRoutingHandoffTellsRouterWhichSquadDeclined(t *testing.T) {
	// When a squad hands the request back as out of scope, the router's next hop
	// must be told which squad declined (and why) so it does NOT re-route to the
	// same squad. This is the reported bug: Omnis routed a general-knowledge
	// question to helper, helper handed it back, and Omnis routed it straight to
	// helper again, bouncing until maxHops. With the decline note the router can
	// see helper already declined and ask the user instead.
	m, infra := routingTestManager("omnis", "omnis", "helper", "research")
	sid := "sess-decline"
	omnisHops := 0
	var secondRouterParts []*genai.Part
	run := func(_ context.Context, sq *SquadInstance, name string, parts []*genai.Part) (string, error) {
		switch name {
		case "omnis":
			omnisHops++
			if omnisHops == 1 {
				// First decision: (mis-)route to helper.
				infra.RouteDirectives.Set(sid, &RouteDirective{Kind: routeKindRoute, Target: "helper"})
				return "", nil
			}
			// Second router hop: it was told helper declined → ask the user
			// instead of re-routing. Capture what it saw.
			secondRouterParts = parts
			return "Could you clarify what you need?", nil
		case "helper":
			infra.RouteDirectives.Set(sid, &RouteDirective{
				Kind:   routeKindHandoff,
				Reason: "general programming question, not a omnis capability",
			})
			return "", nil
		}
		return "answer from " + name, nil
	}
	final, text, err := m.RunWithRouting(context.Background(), "u", sid, "omnis",
		[]*genai.Part{{Text: "is there a transparent http proxy in rust?"}}, nil, run, nil)
	if err != nil {
		t.Fatalf("RunWithRouting err = %v", err)
	}
	if omnisHops != 2 {
		t.Fatalf("omnis hops = %d, want 2 (route to helper, then ask the user)", omnisHops)
	}
	var joined string
	for _, p := range secondRouterParts {
		joined += p.Text + "\n"
	}
	if !strings.Contains(joined, "helper") {
		t.Fatalf("router not told which squad declined; saw: %q", joined)
	}
	if !strings.Contains(joined, "general programming question, not a omnis capability") {
		t.Fatalf("router not given the decline reason; saw: %q", joined)
	}
	if !strings.Contains(strings.ToLower(joined), "do not route") {
		t.Fatalf("decline note missing the do-not-route instruction; saw: %q", joined)
	}
	if final != "omnis" || text != "Could you clarify what you need?" {
		t.Fatalf("final=%q text=%q; want the router asking the user", final, text)
	}
}

func TestRunWithRoutingBoundsHops(t *testing.T) {
	m, infra := routingTestManager("omnis", "omnis", "a", "b")
	sid := "sess-3"
	calls := 0
	// Each hop bounces to the other squad forever; the loop must stop at maxHops.
	run := func(_ context.Context, sq *SquadInstance, name string, _ []*genai.Part) (string, error) {
		calls++
		next := "a"
		if name == "a" {
			next = "b"
		}
		infra.RouteDirectives.Set(sid, &RouteDirective{Kind: routeKindRoute, Target: next})
		return "", nil
	}
	if _, _, err := m.RunWithRouting(context.Background(), "u", sid, "a",
		[]*genai.Part{{Text: "x"}}, nil, run, nil); err != nil {
		t.Fatalf("RunWithRouting err = %v", err)
	}
	if calls != routerMaxHops {
		t.Fatalf("hop count = %d, want %d (bounded)", calls, routerMaxHops)
	}
}

func TestRunWithRoutingUnknownTargetStops(t *testing.T) {
	m, infra := routingTestManager("omnis", "omnis", DefaultSquadName)
	sid := "sess-4"
	calls := 0
	run := func(_ context.Context, sq *SquadInstance, name string, _ []*genai.Part) (string, error) {
		calls++
		infra.RouteDirectives.Set(sid, &RouteDirective{Kind: routeKindRoute, Target: "ghost"})
		return "ok", nil
	}
	final, _, err := m.RunWithRouting(context.Background(), "u", sid, "omnis",
		[]*genai.Part{{Text: "x"}}, nil, run, nil)
	if err != nil {
		t.Fatalf("RunWithRouting err = %v", err)
	}
	if calls != 1 {
		t.Fatalf("hop count = %d, want 1 (unknown target stops)", calls)
	}
	if final != "omnis" {
		t.Fatalf("final squad = %q, want omnis (no hop)", final)
	}
}

func TestRunWithRoutingDisabledIgnoresDirectives(t *testing.T) {
	// RouterName "" → routing disabled: a stray directive must not cause a hop.
	m, infra := routingTestManager("", DefaultSquadName, "other")
	sid := "sess-5"
	calls := 0
	run := func(_ context.Context, sq *SquadInstance, name string, _ []*genai.Part) (string, error) {
		calls++
		infra.RouteDirectives.Set(sid, &RouteDirective{Kind: routeKindRoute, Target: "other"})
		return "", nil
	}
	if _, _, err := m.RunWithRouting(context.Background(), "u", sid, DefaultSquadName,
		[]*genai.Part{{Text: "x"}}, nil, run, nil); err != nil {
		t.Fatalf("RunWithRouting err = %v", err)
	}
	if calls != 1 {
		t.Fatalf("hop count = %d, want 1 (routing disabled)", calls)
	}
}

func TestParseCapabilityVerdict(t *testing.T) {
	cases := []struct {
		in       string
		wantCan  bool
		wantReas string
	}{
		{"CAN_HANDLE: finding and installing agents is my job", true, "finding and installing agents is my job"},
		{"CANNOT_HANDLE: this is hands-on Kubernetes work, not my scope", false, "this is hands-on Kubernetes work, not my scope"},
		{"  can_handle - yes, docs questions are mine ", true, "yes, docs questions are mine"},
		{"Sure, CANNOT_HANDLE because no relevant skills", false, "because no relevant skills"},
		{"I think maybe?", false, "I think maybe?"},    // unparseable → cannot
		{"", false, "no clear verdict from the squad"}, // empty → cannot
	}
	for _, c := range cases {
		can, reason := parseCapabilityVerdict(c.in)
		if can != c.wantCan {
			t.Errorf("parseCapabilityVerdict(%q) can = %v, want %v", c.in, can, c.wantCan)
		}
		if reason != c.wantReas {
			t.Errorf("parseCapabilityVerdict(%q) reason = %q, want %q", c.in, reason, c.wantReas)
		}
	}
	// CANNOT_HANDLE must win even when CAN_HANDLE also appears earlier.
	if can, _ := parseCapabilityVerdict("CAN_HANDLE maybe, but actually CANNOT_HANDLE"); can {
		t.Error("CANNOT_HANDLE should take precedence over a stray CAN_HANDLE")
	}
}

func TestProbeSquadCapabilityResolutionErrors(t *testing.T) {
	rs := RuntimeSettings{
		Agents: []RuntimeAgentConfig{{Name: "leader", Enabled: true, Leader: true}},
		Squads: []RuntimeSquadConfig{
			{Name: "default", Leader: "leader"},
			{Name: "ghostsquad", Members: []string{"ghost"}}, // leaderless, member not in catalogue
		},
	}
	// Unknown squad → error before any model build.
	if _, _, err := probeSquadCapability(context.Background(), rs, "nope", "x"); err == nil {
		t.Error("probe of unknown squad should error")
	}
	// Known squad whose lead isn't an enabled agent → error before model build.
	if _, _, err := probeSquadCapability(context.Background(), rs, "ghostsquad", "x"); err == nil {
		t.Error("probe with unresolvable lead should error")
	}
}

func TestResolveRouterSquadName(t *testing.T) {
	t.Setenv("OMNIS_ROUTER_SQUAD", "")
	// Absent (nil) → default "omnis".
	if got := resolveRouterSquadName(nil); got != "omnis" {
		t.Fatalf("absent → %q, want omnis", got)
	}
	// Explicit opt-out values disable.
	for _, v := range []string{"none", "off", "disabled", ""} {
		val := v
		if got := resolveRouterSquadName(&val); got != "" {
			t.Fatalf("router_squad %q → %q, want disabled", v, got)
		}
	}
	// Explicit custom name (case-normalised).
	custom := "MyRouter"
	if got := resolveRouterSquadName(&custom); got != "myrouter" {
		t.Fatalf("custom → %q, want myrouter", got)
	}
}

func TestResolveRouterSquadNameEnvOverride(t *testing.T) {
	t.Setenv("OMNIS_ROUTER_SQUAD", "none")
	on := "omnis"
	if got := resolveRouterSquadName(&on); got != "" {
		t.Fatalf("env override → %q, want disabled", got)
	}
	t.Setenv("OMNIS_ROUTER_SQUAD", "custom")
	if got := resolveRouterSquadName(nil); got != "custom" {
		t.Fatalf("env override → %q, want custom", got)
	}
}

func TestEnsureRouterSquadInjects(t *testing.T) {
	rs := RuntimeSettings{
		RouterSquad: "omnis",
		Agents: []RuntimeAgentConfig{
			{Name: "leader", Enabled: true, Leader: true, ModelRef: "premium"},
		},
		Squads: []RuntimeSquadConfig{{Name: DefaultSquadName, Leader: "leader"}},
	}
	ensureRouterSquad(&rs)

	cfg, ok := rs.AgentConfig("omnis")
	if !ok {
		t.Fatal("omnis agent not injected")
	}
	if cfg.Leader {
		t.Fatal("injected router must not be a coordinating leader")
	}
	if cfg.ModelRef != "premium" {
		t.Fatalf("router model = %q, want inherited from leader (premium)", cfg.ModelRef)
	}
	sq, ok := rs.Squad("omnis")
	if !ok {
		t.Fatal("omnis squad not injected")
	}
	if sq.Leader != "" || !equalStringSlice(sq.Members, []string{"omnis"}) {
		t.Fatalf("router squad = %+v, want leaderless with member omnis", sq)
	}
}

func TestEnsureRouterSquadDisabledNoop(t *testing.T) {
	rs := RuntimeSettings{
		RouterSquad: "",
		Agents:      []RuntimeAgentConfig{{Name: "leader", Enabled: true, Leader: true}},
		Squads:      []RuntimeSquadConfig{{Name: DefaultSquadName, Leader: "leader"}},
	}
	ensureRouterSquad(&rs)
	if _, ok := rs.AgentConfig("omnis"); ok {
		t.Fatal("router injected while disabled")
	}
	if len(rs.Squads) != 1 {
		t.Fatalf("squads changed while disabled: %d", len(rs.Squads))
	}
}

func TestEnsureRouterSquadNoLeaderDisables(t *testing.T) {
	rs := RuntimeSettings{
		RouterSquad: "omnis",
		Agents:      []RuntimeAgentConfig{{Name: "helper", Enabled: true}},
		Squads:      []RuntimeSquadConfig{{Name: "helper", Members: []string{"helper"}}},
	}
	ensureRouterSquad(&rs)
	if rs.RouterSquad != "" {
		t.Fatalf("RouterSquad = %q, want disabled (no leader to model on)", rs.RouterSquad)
	}
	if _, ok := rs.AgentConfig("omnis"); ok {
		t.Fatal("router injected without a leader")
	}
}

func TestEnsureRouterSquadRespectsExisting(t *testing.T) {
	rs := RuntimeSettings{
		RouterSquad: "omnis",
		Agents: []RuntimeAgentConfig{
			{Name: "leader", Enabled: true, Leader: true},
			{Name: "omnis", Enabled: true, Description: "preexisting"},
		},
		Squads: []RuntimeSquadConfig{
			{Name: DefaultSquadName, Leader: "leader"},
			{Name: "omnis", Members: []string{"omnis"}},
		},
	}
	before := len(rs.Agents)
	ensureRouterSquad(&rs)
	if len(rs.Agents) != before {
		t.Fatalf("agents grew (%d → %d); existing router should be respected", before, len(rs.Agents))
	}
	if cfg, _ := rs.AgentConfig("omnis"); cfg.Description != "preexisting" {
		t.Fatalf("existing router agent overwritten: %q", cfg.Description)
	}
}

// ── Written-as-text tool call (weak-model failure) ───────────────────────────

// TestWrittenToolCallNameDetectsHallucinatedCall pins the failure this guard
// exists for: the router's model WROTE a tool call into its message instead of
// emitting a function call, so ADK saw a tool-call-free response, ended the
// run, recorded no directive, and the surface flushed the call syntax to the
// user as if it were a genuine reply. The observed case is verbatim from
// conversation_subtle-bedbug.json.
func TestWrittenToolCallNameDetectsHallucinatedCall(t *testing.T) {
	observed := "\n\nask_squad(squad=\"helper\", request=\"L'utilisateur demande s'il existe un moyen de donner accès à des files Slack à un agent IA comme Claude Code pour rechercher dans l'historique.\")"
	if got := WrittenToolCallName(observed); got != "ask_squad" {
		t.Fatalf("observed failure: got %q, want %q", got, "ask_squad")
	}

	cases := []struct{ name, text, want string }{
		{"route call", `route_to_squad(squad="Knowledge", reason="web research")`, "route_to_squad"},
		{"handoff call", "handoff_to_router(reason=\"out of scope\")", "handoff_to_router"},
		{"ask_user call", `ask_user(question="which one?")`, "ask_user"},
		{"after narration", "Je transmets votre demande.\n\nroute_to_squad(squad=\"Coding\")", "route_to_squad"},
		{"space before paren", "ask_squad (squad=\"helper\")", "ask_squad"},
		{"markdown fenced", "```\nroute_to_squad(squad=\"Knowledge\")\n```", "route_to_squad"},
	}
	for _, c := range cases {
		if got := WrittenToolCallName(c.text); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// TestWrittenToolCallNameNoFalsePositives guards the expensive half: a genuine
// clarifying question must never be discarded as a hallucinated call. A false
// positive costs the user their reply, so the detector requires call *syntax*
// (an open paren), not a mere mention of a tool or squad name.
func TestWrittenToolCallNameNoFalsePositives(t *testing.T) {
	for _, text := range []string{
		"",
		"   \n ",
		"Souhaitez-vous que je transmette votre demande au squad Knowledge ou au squad Helper ?",
		"Je peux router votre demande, mais j'ai besoin de savoir si c'est pour du code ou de la recherche.",
		"I could ask a squad about this, but which environment do you mean?",
		"The route_to_squad mechanism is internal and I will not describe it further.",
		"Voulez-vous une réponse courte (rapide) ou détaillée ?",
	} {
		if got := WrittenToolCallName(text); got != "" {
			t.Errorf("false positive on %q: got %q, want \"\"", text, got)
		}
	}
}

// TestResolveRouterHopTextSalvagesWrittenRoute pins the SECOND observed shape of
// this failure, verbatim from conversation_select-moth.json: the model wrote only
// the argument object, with no function name — so the syntax detector cannot see
// it, and the raw JSON reached the user. The model's intent is unambiguous there,
// so it must be executed as a real route rather than retried or apologised for.
func TestResolveRouterHopTextSalvagesWrittenRoute(t *testing.T) {
	m, infra := routingTestManager("omnis", "omnis", "knowledge", "helper")
	observed := "\n\n{\"squad\":\"knowledge\",\"reason\":\"Recherche sur les méthodes pour connecter un agent IA (comme Claude Code) aux conversations Slack via l'API.\"}"

	show, nudge := m.ResolveRouterHopText("s1", observed, true)
	if show != "" || nudge != nil {
		t.Fatalf("salvage should show nothing and not retry; got show=%q nudge=%v", show, nudge)
	}
	d := infra.RouteDirectives.Take("s1")
	if d == nil {
		t.Fatal("no directive recorded: the written route was not salvaged")
	}
	if d.Kind != routeKindRoute || d.Target != "knowledge" {
		t.Fatalf("directive = %+v, want route→knowledge", d)
	}
	if !strings.Contains(d.Reason, "Slack") {
		t.Errorf("reason not carried over: %q", d.Reason)
	}

	// Call syntax naming a valid squad is salvageable the same way.
	if show, nudge := m.ResolveRouterHopText("s2", `route_to_squad(squad="Helper", reason="settings question")`, true); show != "" || nudge != nil {
		t.Errorf("call-syntax route not salvaged: show=%q nudge=%v", show, nudge)
	} else if d := infra.RouteDirectives.Take("s2"); d == nil || d.Target != "helper" {
		t.Errorf("directive = %+v, want route→helper", d)
	}
}

// TestResolveRouterHopTextRetriesThenFallsBack covers the non-salvageable paths:
// ask_squad (an optional private probe, so there is nothing to execute) gets one
// corrective retry, and a second failure — or silence — must yield the fallback
// rather than raw syntax or an empty turn.
func TestResolveRouterHopTextRetriesThenFallsBack(t *testing.T) {
	m, infra := routingTestManager("omnis", "omnis", "knowledge")

	show, nudge := m.ResolveRouterHopText("s1", `ask_squad(squad="knowledge", request="…")`, true)
	if show != "" || nudge == nil {
		t.Fatalf("ask_squad should retry, not show; got show=%q nudge=%v", show, nudge)
	}
	if !strings.Contains(nudge.Text, "ask_squad") {
		t.Errorf("nudge does not name the tool: %q", nudge.Text)
	}
	if d := infra.RouteDirectives.Take("s1"); d != nil {
		t.Errorf("ask_squad must not record a route: %+v", d)
	}

	// An unknown squad is not salvageable — route_to_squad itself would refuse it.
	if _, nudge := m.ResolveRouterHopText("s2", `{"squad":"nope","reason":"x"}`, true); nudge == nil {
		t.Error("unknown squad should fall through to a retry, not be routed")
	}
	if d := infra.RouteDirectives.Take("s2"); d != nil {
		t.Errorf("unknown squad was routed anyway: %+v", d)
	}

	// allowRetry=false is the retry hop: no second retry, fallback instead.
	for name, text := range map[string]string{
		"wrote it again":  `ask_squad(squad="knowledge", request="…")`,
		"bare args again": `{"squad":"nope","reason":"x"}`,
		"empty":           "",
		"whitespace":      "  \n\t ",
	} {
		show, nudge := m.ResolveRouterHopText("s3", text, false)
		if nudge != nil {
			t.Errorf("%s: retried twice", name)
		}
		if show != RouterConfusedFallback {
			t.Errorf("%s: got %q, want the fallback", name, show)
		}
	}
}

// TestResolveRouterHopTextPassesGenuineReplies is the expensive half: a real
// clarifying question must reach the user untouched, and must not be mistaken for
// a written call or replaced by the fallback.
func TestResolveRouterHopTextPassesGenuineReplies(t *testing.T) {
	m, _ := routingTestManager("omnis", "omnis", "knowledge")
	for _, text := range []string{
		"Souhaitez-vous que je transmette votre demande au squad Knowledge ?",
		"I could ask a squad about this, but which environment do you mean?",
		"Voici un exemple de configuration : {\"key\": \"value\"} — est-ce ce que vous cherchez ?",
	} {
		for _, allowRetry := range []bool{true, false} {
			show, nudge := m.ResolveRouterHopText("s", text, allowRetry)
			if nudge != nil {
				t.Errorf("genuine reply triggered a retry (allowRetry=%v): %q", allowRetry, text)
			}
			if show != text {
				t.Errorf("genuine reply altered (allowRetry=%v): got %q, want %q", allowRetry, show, text)
			}
		}
	}
}

// TestWrittenToolCallNudgeNamesTheTool checks the corrective retry part tells
// the model concretely what went wrong, since a vague nudge reproduces the bug.
func TestWrittenToolCallNudgeNamesTheTool(t *testing.T) {
	p := WrittenToolCallNudge("ask_squad")
	if p == nil || strings.TrimSpace(p.Text) == "" {
		t.Fatal("nudge part is empty")
	}
	if !strings.Contains(p.Text, "ask_squad") {
		t.Errorf("nudge does not name the tool: %q", p.Text)
	}
}

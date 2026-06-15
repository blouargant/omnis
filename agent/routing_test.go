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
	}
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
				Reason: "general programming question, not a yoke capability",
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
	if !strings.Contains(joined, "general programming question, not a yoke capability") {
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
	t.Setenv("YOKE_ROUTER_SQUAD", "")
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
	t.Setenv("YOKE_ROUTER_SQUAD", "none")
	on := "omnis"
	if got := resolveRouterSquadName(&on); got != "" {
		t.Fatalf("env override → %q, want disabled", got)
	}
	t.Setenv("YOKE_ROUTER_SQUAD", "custom")
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

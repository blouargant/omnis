package fleet

import "testing"

func TestProjectsForSessionScopesByFleet(t *testing.T) {
	all := []Project{
		{Name: "api", Engine: EngineOmnis, Fleet: "Payments"},
		{Name: "gateway", Engine: EngineClaude, Fleet: "Payments"},
		{Name: "ledger", Engine: EngineOmnis, Fleet: "Billing"},
		{Name: "legacy", Engine: EngineOmnis, Fleet: ""}, // Ungrouped
	}
	SetProjectsResolver(func() []Project { return all })
	defer SetProjectsResolver(nil)
	SetSessionFleetResolver(func(sid string) string {
		switch sid {
		case "cond-pay":
			return "Payments"
		case "cond-bill":
			return "Billing"
		}
		return "" // unknown session ⇒ no scope ⇒ Ungrouped
	})
	defer SetSessionFleetResolver(nil)

	names := func(ps []Project) []string {
		var out []string
		for _, p := range ps {
			out = append(out, p.Name)
		}
		return out
	}

	if got := names(ProjectsForSession("cond-pay")); len(got) != 2 || got[0] != "api" || got[1] != "gateway" {
		t.Fatalf("Payments scope = %v, want [api gateway]", got)
	}
	if got := names(ProjectsForSession("cond-bill")); len(got) != 1 || got[0] != "ledger" {
		t.Fatalf("Billing scope = %v, want [ledger]", got)
	}
	// No scope ⇒ Ungrouped (empty-tag projects only).
	if got := names(ProjectsForSession("someone-else")); len(got) != 1 || got[0] != "legacy" {
		t.Fatalf("unscoped session = %v, want [legacy]", got)
	}
	// With no session-fleet resolver installed at all, everything is unscoped ⇒ Ungrouped.
	SetSessionFleetResolver(nil)
	if got := names(ProjectsForSession("cond-pay")); len(got) != 1 || got[0] != "legacy" {
		t.Fatalf("nil resolver = %v, want [legacy]", got)
	}
}

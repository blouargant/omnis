package agent

import "testing"

// A deployment dedicated to one domain (the IA Parc test server) wants new chats
// to land on that domain's squad, not on the router: asked "quels sont mes
// projets", the router could not tell IA Parc projects from anything else and
// asked the user which ones were meant.
func TestResolveStartSquadName(t *testing.T) {
	squads := []RuntimeSquadConfig{
		{Name: "system", Leader: "leader"},
		{Name: "ia parc", Members: []string{"iaparc_operator"}},
		{Name: "session search", Members: []string{"session_search"}, Hidden: true},
	}
	cases := []struct {
		name, file, env, want string
	}{
		{"unset keeps the router default", "", "", ""},
		{"names a squad, case-insensitively", "IA Parc", "", "ia parc"},
		{"env overrides the file", "system", "ia parc", "ia parc"},
		{"unknown squad is ignored", "nope", "", ""},
		{"hidden squad is never a start squad", "session search", "", ""},
		{"the router itself means the default", "omnis", "", ""},
		{"none disables", "none", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("OMNIS_START_SQUAD", tc.env)
			if got := resolveStartSquadName(tc.file, squads, "omnis"); got != tc.want {
				t.Errorf("resolveStartSquadName(%q, env %q) = %q, want %q", tc.file, tc.env, got, tc.want)
			}
		})
	}
}

// The start squad wins over the router for new chats; without one the router
// stays the default, exactly as before.
func TestManagerStartSquad(t *testing.T) {
	var nilMgr *Manager
	if got := nilMgr.StartSquad(); got != "" {
		t.Errorf("nil manager StartSquad = %q", got)
	}
	if got := startSquadOf(&Instance{RouterName: "omnis"}); got != "omnis" {
		t.Errorf("no start squad: got %q, want the router", got)
	}
	if got := startSquadOf(&Instance{RouterName: "omnis", StartName: "ia parc"}); got != "ia parc" {
		t.Errorf("start squad set: got %q, want ia parc", got)
	}
	if got := startSquadOf(&Instance{}); got != "" {
		t.Errorf("no router, no start: got %q, want empty", got)
	}
}

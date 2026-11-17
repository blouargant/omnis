package permissions

import (
	"os"
	"path/filepath"
	"testing"
)

func writePerm(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// The highest layer that names a defaultMode wins, like any scalar of the
// layered config. Before, the shipped system layer's "acceptEdits" overrode a
// user's "bypassPermissions" (a bench still raised cards) or "default".
func TestHighestLayerDefaultModeWins(t *testing.T) {
	dir := t.TempDir()
	sys := writePerm(t, dir, "sys.json", `{"permissions":{"defaultMode":"acceptEdits","allow":[],"ask":["Bash(cd /*)"],"deny":[]}}`)
	cases := map[string]Mode{
		`{"permissions":{"defaultMode":"bypassPermissions"}}`: ModeBypass,
		`{"permissions":{"defaultMode":"default"}}`:           ModeDefault,
		`{"permissions":{"allow":["Read"]}}`:                  ModeAcceptEdits, // no mode: the lower layer's stays
	}
	for user, want := range cases {
		u := writePerm(t, dir, "user.json", user)
		r := NewReloader(sys, []string{u, filepath.Join(dir, "absent.json")}, nil)
		if err := r.reload(true); err != nil {
			t.Fatal(err)
		}
		if got := r.Snapshot().Permissions.DefaultMode; got != want {
			t.Errorf("user %s: mode %v, want %v", user, got, want)
		}
	}
}

// A skill overlay adds rules but never changes the mode.
func TestSkillOverlayDoesNotChangeMode(t *testing.T) {
	dir := t.TempDir()
	sys := writePerm(t, dir, "sys.json", `{"permissions":{"defaultMode":"acceptEdits"}}`)
	skill, err := Load(writePerm(t, dir, "skill.json", `{"permissions":{"defaultMode":"bypassPermissions","allow":["Bash(kubectl get *)"]}}`))
	if err != nil {
		t.Fatal(err)
	}
	r := NewReloader(sys, nil, []*Config{skill})
	if err := r.reload(true); err != nil {
		t.Fatal(err)
	}
	got := r.Snapshot()
	if got.Permissions.DefaultMode != ModeAcceptEdits || len(got.Permissions.Allow) != 1 {
		t.Fatalf("mode %v, allow %d", got.Permissions.DefaultMode, len(got.Permissions.Allow))
	}
	if skill.Permissions.DefaultMode != ModeBypass {
		t.Fatal("the skill's own config must not be modified")
	}
}

// Configs built in code (no ModeSet) keep the old rule.
func TestMergeWithoutModeSetKeepsFirstNonDefault(t *testing.T) {
	base := &Config{}
	o := &Config{Permissions: PermSet{DefaultMode: ModePlan}}
	if m := Merge(base, o).Permissions.DefaultMode; m != ModePlan {
		t.Fatalf("mode %v", m)
	}
}

// An "Allow always" rewrite of the user file keeps an explicit "default" mode.
func TestPersistKeepsExplicitDefaultMode(t *testing.T) {
	dir := t.TempDir()
	u := writePerm(t, dir, "user.json", `{"permissions":{"defaultMode":"default","allow":[]}}`)
	if err := persistApproval(u, "Read", `{"file_path":"a.go"}`, ""); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(u)
	if err != nil || !cfg.Permissions.ModeSet || cfg.Permissions.DefaultMode != ModeDefault || len(cfg.Permissions.Allow) != 1 {
		data, _ := os.ReadFile(u)
		t.Fatalf("after persist: %v\n%s", err, data)
	}
}

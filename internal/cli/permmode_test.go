package cli

import (
	"strings"
	"testing"

	"github.com/blouargant/omnis/core/permissions"
)

func TestCycleMode(t *testing.T) {
	P := permissions.ModeDefault
	seq := []permissions.Mode{P, permissions.ModeAcceptEdits, permissions.ModePlan, permissions.ModeAuto, permissions.ModeBypass, P}
	for i := 0; i < len(seq)-1; i++ {
		if got := cycleMode(seq[i], true); got != seq[i+1] {
			t.Errorf("bypass allowed: %v -> %v, want %v", seq[i], got, seq[i+1])
		}
	}
	if got := cycleMode(permissions.ModeAuto, false); got != P {
		t.Errorf("auto without bypass -> %v, want default", got)
	}
	if got := cycleMode(permissions.ModeDontAsk, true); got != P {
		t.Errorf("dontAsk -> %v, want default", got)
	}
}

func TestApplyModeCommand(t *testing.T) {
	st := permissions.NewModeStore()
	out, err := applyModeCommand(st, "s1", "")
	if err != nil || !strings.Contains(out, "default") {
		t.Fatalf("no arg: %q %v", out, err)
	}
	if _, err := applyModeCommand(st, "s1", "plan"); err != nil {
		t.Fatal(err)
	}
	if st.Effective("s1") != permissions.ModePlan {
		t.Fatalf("mode not set: %v", st.Effective("s1"))
	}
	if out, _ := applyModeCommand(st, "s1", ""); !strings.Contains(out, "plan") {
		t.Fatalf("no arg after set: %q", out)
	}
	if _, err := applyModeCommand(st, "s1", "default"); err != nil || st.Effective("s1") != permissions.ModeDefault {
		t.Fatalf("explicit default failed: %v", err)
	}
	_, err = applyModeCommand(st, "s1", "yolo")
	if err == nil {
		t.Fatal("unknown mode accepted")
	}
	for _, n := range []string{"default", "acceptEdits", "plan", "auto", "dontAsk", "bypassPermissions"} {
		if !strings.Contains(err.Error(), n) {
			t.Errorf("error %q lacks %s", err, n)
		}
	}
	st.SetBypassDisabled(true)
	if _, err := applyModeCommand(st, "s1", "bypassPermissions"); err == nil {
		t.Fatal("bypass accepted while disabled")
	}
}

func TestValidatePermissionModeFlag(t *testing.T) {
	if m, err := ValidatePermissionModeFlag("plan", false); err != nil || m != permissions.ModePlan {
		t.Fatalf("plan: %v %v", m, err)
	}
	if _, err := ValidatePermissionModeFlag("yolo", false); err == nil {
		t.Fatal("yolo accepted")
	}
	if _, err := ValidatePermissionModeFlag("bypassPermissions", true); err == nil {
		t.Fatal("bypass accepted while disabled")
	}
	if _, err := ValidatePermissionModeFlag("bypassPermissions", false); err != nil {
		t.Fatal(err)
	}
}

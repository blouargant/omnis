package cli

import (
	"fmt"

	"github.com/blouargant/omnis/core/permissions"
)

const modeNames = "default, acceptEdits, plan, auto, dontAsk, bypassPermissions"

// cycleMode returns the next mode in the Shift+Tab cycle:
// default → acceptEdits → plan → auto → bypassPermissions (only when allowed)
// → default. dontAsk is not in the cycle and returns to default.
func cycleMode(cur permissions.Mode, bypassAllowed bool) permissions.Mode {
	switch cur {
	case permissions.ModeDefault:
		return permissions.ModeAcceptEdits
	case permissions.ModeAcceptEdits:
		return permissions.ModePlan
	case permissions.ModePlan:
		return permissions.ModeAuto
	case permissions.ModeAuto:
		if bypassAllowed {
			return permissions.ModeBypass
		}
	}
	return permissions.ModeDefault
}

// CycleMode is the exported cycleMode, shared with the TUI.
func CycleMode(cur permissions.Mode, bypassAllowed bool) permissions.Mode {
	return cycleMode(cur, bypassAllowed)
}

// applyModeCommand implements "/mode [name]": no argument reports the
// effective mode; a name sets the session override.
func applyModeCommand(store *permissions.ModeStore, sid, arg string) (string, error) {
	if arg == "" {
		return fmt.Sprintf("permission mode: %s", store.Effective(sid)), nil
	}
	m, err := ValidatePermissionModeFlag(arg, store.BypassDisabled())
	if err != nil {
		return "", err
	}
	store.Set(sid, m)
	return fmt.Sprintf("permission mode: %s", m), nil
}

// ApplyModeCommand is the exported applyModeCommand, shared with the TUI.
func ApplyModeCommand(store *permissions.ModeStore, sid, arg string) (string, error) {
	return applyModeCommand(store, sid, arg)
}

// ValidatePermissionModeFlag parses a mode name (the --permission-mode flag or
// a /mode argument), refusing unknown names and bypassPermissions while bypass
// is disabled.
func ValidatePermissionModeFlag(s string, bypassDisabled bool) (permissions.Mode, error) {
	m, ok := permissions.ParseModeStrict(s)
	if !ok {
		return permissions.ModeDefault, fmt.Errorf("unknown permission mode %q (valid: %s)", s, modeNames)
	}
	if m == permissions.ModeBypass && bypassDisabled {
		return permissions.ModeDefault, fmt.Errorf("permission mode bypassPermissions is disabled")
	}
	return m, nil
}

package hooks

import "sync/atomic"

var permissionModeResolver atomic.Pointer[func(sessionID string) string]

// SetPermissionModeResolver installs the process-wide function reporting a
// session's permission mode (Claude Code spelling). Config.Run uses it to fill
// Input.PermissionMode when the caller left it empty. nil clears it.
func SetPermissionModeResolver(fn func(sessionID string) string) {
	if fn == nil {
		permissionModeResolver.Store(nil)
		return
	}
	permissionModeResolver.Store(&fn)
}

func resolvePermissionMode(sessionID string) string {
	if sessionID == "" {
		return ""
	}
	if p := permissionModeResolver.Load(); p != nil {
		return (*p)(sessionID)
	}
	return ""
}

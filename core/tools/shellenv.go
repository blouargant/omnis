package tools

import "context"

type shellEnvKey struct{}

// WithShellEnv returns a context carrying extra "NAME=value" entries for the
// agent's shell tools (Bash, the "!" escape, background tasks). Like WithCwd it
// propagates into sub-agent runners. The server uses it to hand a user's
// platform token to their own commands; core/tools knows nothing about what
// the entries mean. A nil/empty env is a no-op. Deliberately NOT read by
// RunShellCaptured (hooks, run_tests).
func WithShellEnv(ctx context.Context, env []string) context.Context {
	if len(env) == 0 {
		return ctx
	}
	return context.WithValue(ctx, shellEnvKey{}, append([]string(nil), env...))
}

// ShellEnvFrom returns the entries planted by WithShellEnv, or nil.
func ShellEnvFrom(ctx context.Context) []string {
	if ctx == nil {
		return nil
	}
	env, _ := ctx.Value(shellEnvKey{}).([]string)
	return env
}

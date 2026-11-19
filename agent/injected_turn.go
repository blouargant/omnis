package agent

import "context"

type injectedTurnKey struct{}

// WithInjectedTurn marks ctx as running a turn whose prompt text did not come
// from the user (mailbox, background task, scheduler, A2A). The auto-mode
// classifier shows that text separately, labelled as not from the user.
func WithInjectedTurn(ctx context.Context, text string) context.Context {
	return context.WithValue(ctx, injectedTurnKey{}, text)
}

func injectedTurnText(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	s, ok := ctx.Value(injectedTurnKey{}).(string)
	return s, ok && s != ""
}

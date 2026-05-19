// inputs.go — interactive resolution of "${input:id}" template
// references inside MCP server configs.
//
// At config-load time servers may embed references to user-supplied
// values, e.g. `"Authorization": "Bearer ${input:github_pat}"`. The
// matching input is declared in the top-level `inputs` array (id,
// type, description, password). The resolver prompts the user the
// first time it sees a given id and caches the answer process-wide so
// concurrent sessions sharing the same server reuse the same value.
//
// Password-typed inputs flow through the askuser registry with the
// Question.Password flag set; web/TUI surfaces render a masked input
// for these.
package mcp

import (
	"context"
	"fmt"
	"regexp"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/blouargant/yoke/internal/askuser"
)

// inputRefRe matches "${input:id}" anywhere in a string. The id is
// captured in group 1. The pattern follows VS Code's input variable
// syntax exactly.
var inputRefRe = regexp.MustCompile(`\$\{input:([a-zA-Z0-9_.-]+)\}`)

// hasInputReferences reports whether any string field of s contains a
// "${input:id}" reference. Servers without references skip the lazy
// resolver wrapper.
func hasInputReferences(s Server) bool {
	if inputRefRe.MatchString(s.Command) || inputRefRe.MatchString(s.URL) {
		return true
	}
	for _, a := range s.Args {
		if inputRefRe.MatchString(a) {
			return true
		}
	}
	for _, v := range s.Env {
		if inputRefRe.MatchString(v) {
			return true
		}
	}
	for _, v := range s.Headers {
		if inputRefRe.MatchString(v) {
			return true
		}
	}
	return false
}

// InputResolver bridges MCP "${input:id}" references to the askuser
// registry. One resolver is shared across the whole process so a
// credential entered once is reused across sessions and across agent
// generations.
//
// Cache key is the input id alone (not (server, id)) so the same input
// referenced by multiple servers prompts the user exactly once.
type InputResolver struct {
	reg *askuser.Registry

	mu       sync.Mutex
	cache    map[string]string
	inflight map[string]chan struct{}
}

// NewInputResolver returns a resolver backed by reg. A nil registry
// disables interactive resolution: a server with template references
// will fail to connect with a clear error.
func NewInputResolver(reg *askuser.Registry) *InputResolver {
	return &InputResolver{
		reg:      reg,
		cache:    map[string]string{},
		inflight: map[string]chan struct{}{},
	}
}

// Resolve returns the user-supplied value for the given input,
// prompting on first call (and coalescing concurrent racers onto a
// single prompt).
func (r *InputResolver) Resolve(ctx context.Context, in Input) (string, error) {
	if r == nil || r.reg == nil {
		return "", fmt.Errorf("mcp input %q: no askuser registry is wired", in.ID)
	}
	if in.ID == "" {
		return "", fmt.Errorf("mcp input: missing id")
	}

	r.mu.Lock()
	if v, ok := r.cache[in.ID]; ok {
		r.mu.Unlock()
		return v, nil
	}
	if wait, ok := r.inflight[in.ID]; ok {
		r.mu.Unlock()
		select {
		case <-wait:
		case <-ctx.Done():
			return "", ctx.Err()
		}
		r.mu.Lock()
		v, ok := r.cache[in.ID]
		r.mu.Unlock()
		if ok {
			return v, nil
		}
		return "", fmt.Errorf("mcp input %q: concurrent resolution failed", in.ID)
	}
	done := make(chan struct{})
	r.inflight[in.ID] = done
	r.mu.Unlock()

	v, err := r.askAndCache(ctx, in)
	close(done)
	r.mu.Lock()
	delete(r.inflight, in.ID)
	r.mu.Unlock()
	return v, err
}

func (r *InputResolver) askAndCache(ctx context.Context, in Input) (string, error) {
	q := r.buildQuestion(in)
	ans, err := r.reg.Ask(ctx, sessionIDFromContext(ctx), q)
	if err != nil {
		return "", err
	}
	if ans.Cancelled {
		return "", fmt.Errorf("mcp input %q: user cancelled", in.ID)
	}
	val := answerToValue(in, ans)
	if val == "" && in.Default != "" {
		val = in.Default
	}
	if val == "" {
		return "", fmt.Errorf("mcp input %q: empty answer", in.ID)
	}
	r.mu.Lock()
	r.cache[in.ID] = val
	r.mu.Unlock()
	return val, nil
}

// buildQuestion translates an Input declaration into the askuser
// Question shape the surfaces know how to render. pickString → single-
// choice with the input's Options; promptString → free-text, with
// Password forwarded so the UI masks the input field.
func (r *InputResolver) buildQuestion(in Input) askuser.Question {
	prompt := in.Description
	if prompt == "" {
		prompt = fmt.Sprintf("Provide a value for MCP input %q", in.ID)
	}
	switch in.Kind() {
	case InputPickString:
		return askuser.Question{
			Kind:    askuser.KindSingle,
			Prompt:  prompt,
			Choices: append([]string(nil), in.Options...),
			Default: in.Default,
		}
	default:
		return askuser.Question{
			Kind:     askuser.KindText,
			Prompt:   prompt,
			Default:  in.Default,
			Password: in.Password,
		}
	}
}

func answerToValue(in Input, ans askuser.Answer) string {
	if in.Kind() == InputPickString {
		if len(ans.Selected) > 0 {
			return ans.Selected[0]
		}
		// Fall back to free-text if a surface returned text instead of
		// a choice (e.g. the "other" path in the stdin asker).
		return ans.Text
	}
	return ans.Text
}

// sessionIDFromContext extracts the active session ID from ctx by
// type-asserting against the ADK ReadonlyContext interface. Plain
// context.Background() returns "".
func sessionIDFromContext(ctx context.Context) string {
	if sc, ok := ctx.(interface{ SessionID() string }); ok {
		return sc.SessionID()
	}
	return ""
}

// resolveServerTemplates returns a copy of s with every "${input:id}"
// reference in any string field replaced by the user's answer. The
// inputs slice is searched to look up the matching Input declaration;
// references to undeclared inputs are an error.
func resolveServerTemplates(ctx context.Context, r *InputResolver, s Server, inputs []Input) (Server, error) {
	out := s
	expand := func(text string) (string, error) {
		return r.expand(ctx, text, inputs, s.Name)
	}

	if s.Command != "" {
		v, err := expand(s.Command)
		if err != nil {
			return s, err
		}
		out.Command = v
	}
	if s.URL != "" {
		v, err := expand(s.URL)
		if err != nil {
			return s, err
		}
		out.URL = v
	}
	if len(s.Args) > 0 {
		out.Args = make([]string, len(s.Args))
		for i, a := range s.Args {
			v, err := expand(a)
			if err != nil {
				return s, err
			}
			out.Args[i] = v
		}
	}
	if len(s.Env) > 0 {
		out.Env = make(map[string]string, len(s.Env))
		for k, v := range s.Env {
			vv, err := expand(v)
			if err != nil {
				return s, err
			}
			out.Env[k] = vv
		}
	}
	if len(s.Headers) > 0 {
		out.Headers = make(map[string]string, len(s.Headers))
		for k, v := range s.Headers {
			vv, err := expand(v)
			if err != nil {
				return s, err
			}
			out.Headers[k] = vv
		}
	}
	return out, nil
}

// expand walks text for every "${input:id}" reference and substitutes
// the resolver-supplied value. Returns the original text unchanged
// when no references are present (no allocation on the hot path).
func (r *InputResolver) expand(ctx context.Context, text string, inputs []Input, serverName string) (string, error) {
	if !inputRefRe.MatchString(text) {
		return text, nil
	}
	var resolveErr error
	out := inputRefRe.ReplaceAllStringFunc(text, func(match string) string {
		if resolveErr != nil {
			return match
		}
		sub := inputRefRe.FindStringSubmatch(match)
		if len(sub) < 2 {
			return match
		}
		id := sub[1]
		in, ok := lookupInput(inputs, id)
		if !ok {
			resolveErr = fmt.Errorf("mcp server %q: unknown input %q", serverName, id)
			return match
		}
		v, err := r.Resolve(ctx, in)
		if err != nil {
			resolveErr = err
			return match
		}
		return v
	})
	return out, resolveErr
}

func lookupInput(inputs []Input, id string) (Input, bool) {
	for _, in := range inputs {
		if in.ID == id {
			return in, true
		}
	}
	return Input{}, false
}

// lazyTransport is an mcp.Transport that defers building the underlying
// transport until first Connect, so input prompts fire at first tool
// use (when a session context is available) rather than at agent
// build. All concurrent Connect calls share a single resolution
// attempt via `once`; the resolved server config is cached for the
// lifetime of this transport, so reconnects (after network blips on
// the http transport) don't re-prompt.
type lazyTransport struct {
	server   Server
	inputs   []Input
	resolver *InputResolver

	once sync.Once
	real mcp.Transport
	err  error
}

func newLazyTransport(s Server, inputs []Input, r *InputResolver) *lazyTransport {
	return &lazyTransport{server: s, inputs: inputs, resolver: r}
}

// Connect resolves any pending template references, then delegates to
// the real transport. Failures are sticky — once init fails, every
// future Connect returns the same error so we never repeatedly pester
// the user for a credential they already declined.
func (l *lazyTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	l.once.Do(func() {
		resolved, err := resolveServerTemplates(ctx, l.resolver, l.server, l.inputs)
		if err != nil {
			l.err = err
			return
		}
		l.real, l.err = buildTransport(resolved)
	})
	if l.err != nil {
		return nil, l.err
	}
	return l.real.Connect(ctx)
}

package identity

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

var (
	// ErrRejected: the validator ran and refused the token (or named no login).
	ErrRejected = errors.New("identity: token rejected")
	// ErrUnavailable: the validator could not give an answer (missing binary,
	// timeout, exec failure). Distinct from a rejection so the server answers
	// 503 rather than "not logged in".
	ErrUnavailable = errors.New("identity: validator unavailable")
)

// Validator turns a token into an Identity.
type Validator interface {
	Validate(ctx context.Context, token string) (Identity, error)
}

// CommandValidator runs Argv (no shell) with <TokenEnv>=<token> added to the
// process environment. Exit 0 = accepted; stdout is parsed as JSON, else YAML,
// and LoginField / RolesField are dotted paths into it.
type CommandValidator struct {
	Argv       []string
	TokenEnv   string
	LoginField string
	RolesField string
	Timeout    time.Duration
}

func (v CommandValidator) Validate(ctx context.Context, token string) (Identity, error) {
	if len(v.Argv) == 0 {
		return Identity{}, fmt.Errorf("%w: no command", ErrUnavailable)
	}
	timeout := v.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, v.Argv[0], v.Argv[1:]...)
	cmd.Env = append(os.Environ(), v.TokenEnv+"="+token)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = nil // stderr may echo the token; never capture it into an error
	cmd.WaitDelay = 2 * time.Second
	err := cmd.Run()
	if cctx.Err() != nil {
		return Identity{}, fmt.Errorf("%w: timed out after %s", ErrUnavailable, timeout)
	}
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return Identity{}, fmt.Errorf("%w: validator exited %d", ErrRejected, exitErr.ExitCode())
		}
		return Identity{}, fmt.Errorf("%w: cannot run validator", ErrUnavailable)
	}
	doc, ok := parseDoc(out.Bytes())
	if !ok {
		return Identity{}, fmt.Errorf("%w: unparseable validator output", ErrRejected)
	}
	login, _ := lookup(doc, v.LoginField).(string)
	login = strings.TrimSpace(login)
	if login == "" {
		return Identity{}, fmt.Errorf("%w: no login at %q", ErrRejected, v.LoginField)
	}
	return Identity{Login: login, Roles: toStrings(lookup(doc, v.RolesField)), Token: token}, nil
}

func parseDoc(b []byte) (any, bool) {
	var doc any
	if err := json.Unmarshal(b, &doc); err == nil {
		return doc, true
	}
	if err := yaml.Unmarshal(b, &doc); err == nil && doc != nil {
		return doc, true
	}
	return nil, false
}

// lookup walks a dotted path through nested maps. "" or a miss yields nil.
func lookup(doc any, path string) any {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	cur := doc
	for _, key := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = m[key]
	}
	return cur
}

func toStrings(v any) []string {
	switch t := v.(type) {
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			if s, ok := e.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
		return out
	case string:
		var out []string
		for _, s := range strings.Split(t, ",") {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// SplitArgs splits a command line into argv with single/double-quote support
// (no expansion, no escapes beyond quoting). It never invokes a shell.
func SplitArgs(s string) ([]string, error) {
	var args []string
	var cur strings.Builder
	inArg := false
	var quote rune
	for _, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '"' || r == '\'':
			quote = r
			inArg = true
		case r == ' ' || r == '\t' || r == '\n':
			if inArg {
				args = append(args, cur.String())
				cur.Reset()
				inArg = false
			}
		default:
			cur.WriteRune(r)
			inArg = true
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated %c quote", quote)
	}
	if inArg {
		args = append(args, cur.String())
	}
	return args, nil
}

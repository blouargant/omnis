package identity

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func fakeCmd(t *testing.T, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell fake")
	}
	p := filepath.Join(t.TempDir(), "fake")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSplitArgs(t *testing.T) {
	got, err := SplitArgs(`iapcli user get -C "/etc/my dir/x.yaml" 'a b'`)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"iapcli", "user", "get", "-C", "/etc/my dir/x.yaml", "a b"}
	if len(got) != len(want) {
		t.Fatalf("got %q", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %q", got)
		}
	}
	if _, err := SplitArgs(`unterminated "quote`); err == nil {
		t.Fatal("want error on unterminated quote")
	}
}

func TestCommandValidatorJSON(t *testing.T) {
	cmd := fakeCmd(t, `[ "$TOK" = "good" ] || exit 1; echo '{"user":{"login":"alice","roles":["admin","rd"]}}'`)
	v := CommandValidator{Argv: []string{cmd}, TokenEnv: "TOK", LoginField: "user.login", RolesField: "user.roles", Timeout: 5 * time.Second}
	id, err := v.Validate(context.Background(), "good")
	if err != nil || id.Login != "alice" || len(id.Roles) != 2 || id.Token != "good" {
		t.Fatalf("got %+v %v", id, err)
	}
	if _, err := v.Validate(context.Background(), "bad"); !errors.Is(err, ErrRejected) {
		t.Fatalf("bad token: want ErrRejected, got %v", err)
	}
}

func TestCommandValidatorYAML(t *testing.T) {
	cmd := fakeCmd(t, `printf 'login: bob\nroles: rd\n'`)
	v := CommandValidator{Argv: []string{cmd}, TokenEnv: "TOK", LoginField: "login", RolesField: "roles", Timeout: 5 * time.Second}
	id, err := v.Validate(context.Background(), "x")
	if err != nil || id.Login != "bob" || len(id.Roles) != 1 || id.Roles[0] != "rd" {
		t.Fatalf("got %+v %v", id, err)
	}
}

func TestCommandValidatorEmptyLoginRejected(t *testing.T) {
	cmd := fakeCmd(t, `echo '{"other":1}'`)
	v := CommandValidator{Argv: []string{cmd}, TokenEnv: "TOK", LoginField: "login", Timeout: 5 * time.Second}
	if _, err := v.Validate(context.Background(), "x"); !errors.Is(err, ErrRejected) {
		t.Fatalf("want ErrRejected, got %v", err)
	}
}

func TestCommandValidatorUnavailable(t *testing.T) {
	v := CommandValidator{Argv: []string{"/nonexistent/validator"}, TokenEnv: "TOK", LoginField: "login", Timeout: time.Second}
	if _, err := v.Validate(context.Background(), "x"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("missing binary: want ErrUnavailable, got %v", err)
	}
	slow := fakeCmd(t, `sleep 5`)
	v = CommandValidator{Argv: []string{slow}, TokenEnv: "TOK", LoginField: "login", Timeout: 200 * time.Millisecond}
	if _, err := v.Validate(context.Background(), "x"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("timeout: want ErrUnavailable, got %v", err)
	}
}

func TestCommandValidatorErrorNeverContainsToken(t *testing.T) {
	cmd := fakeCmd(t, `echo "bad token $TOK" >&2; exit 1`)
	v := CommandValidator{Argv: []string{cmd}, TokenEnv: "TOK", LoginField: "login", Timeout: 5 * time.Second}
	_, err := v.Validate(context.Background(), "s3cr3t-token")
	if err == nil || strings.Contains(err.Error(), "s3cr3t-token") {
		t.Fatalf("error must not leak the token: %v", err)
	}
}

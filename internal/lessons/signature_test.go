package lessons

import (
	"strings"
	"testing"
)

func TestSignatureIgnoresVolatileParts(t *testing.T) {
	a := Signature("Edit", "Error: old_string not found in /tmp/a1b2/x.go at line 42")
	b := Signature("Edit", "Error: old_string not found in /home/u/y.go at line 7")
	if a != b {
		t.Fatal("paths and numbers must not change the signature")
	}
	if a == Signature("Edit", "Error: old_string appears 3 times") {
		t.Fatal("different errors must differ")
	}
	same := [][2]string{
		{`file "a.txt" is locked`, `file "other name" is locked`},
		{"bad handle 0x7f3a9c21 here", "bad handle 0xdeadbeef00 here"},
		{"job 123e4567-e89b-12d3-a456-426614174000 failed", "job 9b2c1d3e-0000-4000-8000-123456789abc failed"},
		{"failed at 2026-11-20T10:11:12Z", "failed at 2025-01-02T03:04:05+01:00"},
		{"token abcdef0123456789 expired", "token 0123456789abcdef expired"},
	}
	for _, p := range same {
		if Signature("T", p[0]) != Signature("T", p[1]) {
			t.Errorf("expected equal: %q vs %q", p[0], p[1])
		}
	}
	if Signature("Edit", "x") == Signature("Write", "x") {
		t.Fatal("tool key is part of the signature")
	}
}

func TestSignatureUsesOnlyTheHead(t *testing.T) {
	head := strings.Repeat("z", SignatureTextChars)
	if Signature("T", head+" tail one") != Signature("T", head+" other tail") {
		t.Fatal("text past SignatureTextChars must be ignored")
	}
}

func TestToolKeyBash(t *testing.T) {
	cases := []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"command": "sudo timeout 5 kubectl get pods"}, "Bash:kubectl"},
		{map[string]any{"command": "FOO=1 git status"}, "Bash:git"},
		{map[string]any{"command": ""}, "Bash"},
		{nil, "Bash"},
	}
	for _, c := range cases {
		if got := ToolKey("Bash", c.args); got != c.want {
			t.Errorf("ToolKey(%v) = %q, want %q", c.args, got, c.want)
		}
	}
	if got := ToolKey("Edit", nil); got != "Edit" {
		t.Fatalf("got %q", got)
	}
}

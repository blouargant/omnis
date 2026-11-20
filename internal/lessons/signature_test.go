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

func TestSignatureNormalisesEveryPathShape(t *testing.T) {
	same := [][2]string{
		{"Error: old_string not found in prices.go — include more context", "Error: old_string not found in server/main.go — include more context"},
		{"Error: old_string not found in src/main.go", "Error: old_string not found in /abs/other.py"},
		{"cannot open:/tmp/x", "cannot open:/var/lib/y/z"},
		{"failed (/tmp/x) here", "failed (/home/u/a.txt) here"},
		{"bad value --file=/a/b", "bad value --file=/c/d/e"},
		{"no such file ./x", "no such file ../y/z"},
		{"x.go:42:7: undefined: foo", "pkg/y.go:3:1: undefined: foo"},
		{"failed at 2026-11-20 10:11:12 in a.go", "failed at 2025-01-02 03:04:05 in b.go"},
	}
	for _, p := range same {
		if Signature("T", p[0]) != Signature("T", p[1]) {
			t.Errorf("expected equal: %q vs %q (%q / %q)", p[0], p[1], normalise(p[0]), normalise(p[1]))
		}
	}
	differ := [][2]string{
		{"Error: old_string not found in a.go", "Error: old_string appears 2 times in a.go"},
		{"unknown flag --namespace-all", "unknown flag --all-namespaces"},
	}
	for _, p := range differ {
		if Signature("T", p[0]) == Signature("T", p[1]) {
			t.Errorf("expected different: %q vs %q", p[0], p[1])
		}
	}
}

// A bare dotted name is a file name only with a known extension: dotted
// identifiers keep their errors apart.
func TestSignatureKeepsDottedIdentifiers(t *testing.T) {
	differ := [][2]string{
		{"undefined: strings.Builder", "undefined: http.Client"},
		{`deployments.apps "x" not found`, `statefulsets.apps "x" not found`},
	}
	for _, p := range differ {
		if Signature("T", p[0]) == Signature("T", p[1]) {
			t.Errorf("expected different: %q vs %q (%q / %q)", p[0], p[1], normalise(p[0]), normalise(p[1]))
		}
	}
	base := Signature("Edit", "old_string not found in prices.go")
	for _, other := range []string{"old_string not found in server/main.go", "old_string not found in notes.md"} {
		if Signature("Edit", other) != base {
			t.Errorf("expected equal to prices.go: %q (%q)", other, normalise(other))
		}
	}
}

func TestSignatureExtraFileExtensionsAndHostnames(t *testing.T) {
	if Signature("T", "cannot read app.vue") != Signature("T", "cannot read deploy.tfvars") {
		t.Error("vue and tfvars files must normalise alike")
	}
	if Signature("T", "dial api.example.com failed") == Signature("T", "dial db.internal.net failed") {
		t.Error("hostnames are not paths")
	}
}

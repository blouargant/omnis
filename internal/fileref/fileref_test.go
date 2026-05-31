package fileref

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestTokens(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"@.agents/README.md", []string{".agents/README.md"}},
		{"please read @main.go now", []string{"main.go"}},
		{"mail me at user@example.com", []string{}}, // "@" preceded by non-space → not a ref
		{"see @main.go.", []string{"main.go"}},
		{"check @a/b.go, @c/d.go!", []string{"a/b.go", "c/d.go"}},
		{"@", []string{}},  // bare "@" → no token
		{"@.", []string{}}, // only trailing punctuation
		{"first\n@second.txt", []string{"second.txt"}},
		{"a@b and @c", []string{"c"}}, // a@b excluded, @c kept
	}
	for _, c := range cases {
		if got := Tokens(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("Tokens(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestClassifyAndContext(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hi there"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}

	if got := Classify("hello.txt", dir).Kind; got != KindFile {
		t.Errorf("Classify file kind = %v, want %v", got, KindFile)
	}
	if got := Classify("sub", dir).Kind; got != KindDir {
		t.Errorf("Classify dir kind = %v, want %v", got, KindDir)
	}
	if got := Classify("nope.txt", dir).Kind; got != KindMissing {
		t.Errorf("Classify missing kind = %v, want %v", got, KindMissing)
	}

	ctx := Context("look at @hello.txt and @sub and @nope.txt", dir)
	if ctx == "" {
		t.Fatal("Context returned empty, want inlined file content")
	}
	if !contains(ctx, "hi there") {
		t.Errorf("Context missing file content; got:\n%s", ctx)
	}
	if !contains(ctx, "FILE: hello.txt") {
		t.Errorf("Context missing file header; got:\n%s", ctx)
	}
	// Directories and missing paths are not inlined.
	if contains(ctx, "sub") || contains(ctx, "nope") {
		t.Errorf("Context inlined a dir/missing ref; got:\n%s", ctx)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

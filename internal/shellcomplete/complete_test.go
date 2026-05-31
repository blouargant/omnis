package shellcomplete

import (
	"os"
	"path/filepath"
	"testing"
)

func contains(s []string, want string) bool {
	for _, v := range s {
		if v == want {
			return true
		}
	}
	return false
}

func TestCompletePaths(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "alpha.txt"), "x")
	mustWrite(t, filepath.Join(dir, "alphabet.txt"), "x")
	mustWrite(t, filepath.Join(dir, "beta.txt"), "x")
	if err := os.Mkdir(filepath.Join(dir, "subdir"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Bare prefix in argument position completes files relative to cwd.
	start, cands := Complete("cat alph", dir)
	if start != 4 {
		t.Fatalf("start = %d, want 4", start)
	}
	if !contains(cands, "alpha.txt") || !contains(cands, "alphabet.txt") {
		t.Fatalf("missing alpha matches: %v", cands)
	}
	if contains(cands, "beta.txt") {
		t.Fatalf("beta.txt should not match prefix alph: %v", cands)
	}

	// Directories carry a trailing slash.
	_, cands = Complete("cat sub", dir)
	if !contains(cands, "subdir/") {
		t.Fatalf("expected subdir/ in %v", cands)
	}

	// A directory prefix is preserved verbatim on the candidate.
	start, cands = Complete("cat subdir/", dir)
	if start != 4 {
		t.Fatalf("start = %d, want 4", start)
	}
	// subdir is empty → no candidates, but must not error.
	if cands == nil {
		// empty dir yields nil slice; acceptable.
	}
	mustWrite(t, filepath.Join(dir, "subdir", "nested.go"), "x")
	_, cands = Complete("cat subdir/ne", dir)
	if !contains(cands, "subdir/nested.go") {
		t.Fatalf("expected subdir/nested.go in %v", cands)
	}
}

func TestCompleteCommandsFirstToken(t *testing.T) {
	// Build a fake $PATH with one executable.
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "yoketestbin"), "#!/bin/sh\n")
	if err := os.Chmod(filepath.Join(dir, "yoketestbin"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(dir, "yoketestlib"), "data") // not executable
	if err := os.Chmod(filepath.Join(dir, "yoketestlib"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)

	_, cands := Complete("yoketest", "")
	if !contains(cands, "yoketestbin") {
		t.Fatalf("expected yoketestbin in %v", cands)
	}
	if contains(cands, "yoketestlib") {
		t.Fatalf("non-executable yoketestlib should be excluded: %v", cands)
	}
}

func TestDotfilesHiddenUnlessTyped(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, ".hidden"), "x")
	mustWrite(t, filepath.Join(dir, "visible"), "x")

	_, cands := Complete("cat ", dir)
	if contains(cands, ".hidden") {
		t.Fatalf(".hidden should be hidden for empty prefix: %v", cands)
	}
	if !contains(cands, "visible") {
		t.Fatalf("expected visible in %v", cands)
	}
	_, cands = Complete("cat .", dir)
	if !contains(cands, ".hidden") {
		t.Fatalf(".hidden should show when prefix is '.': %v", cands)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

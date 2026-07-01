package binpath

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAugmentedPath covers append-only semantics: new dirs are appended in
// order, dedupe skips ones already present, existing entries keep their order,
// and an empty candidate is ignored.
func TestAugmentedPath(t *testing.T) {
	sep := string(os.PathListSeparator)
	cur := strings.Join([]string{"/usr/bin", "/bin"}, sep)

	got := augmentedPath(cur, []string{"/home/u/.local/bin", "/usr/bin", "", "/home/u/go/bin"})
	want := strings.Join([]string{"/usr/bin", "/bin", "/home/u/.local/bin", "/home/u/go/bin"}, sep)
	if got != want {
		t.Errorf("augmentedPath =\n  %q\nwant\n  %q", got, want)
	}

	// Nothing new to add → unchanged (identity, so callers can skip the Setenv).
	if got := augmentedPath(cur, []string{"/usr/bin", "/bin"}); got != cur {
		t.Errorf("no-op augment changed PATH: %q", got)
	}

	// Empty starting PATH → just the added dirs, no leading separator.
	if got := augmentedPath("", []string{"/a", "/b"}); got != "/a"+sep+"/b" {
		t.Errorf("empty-PATH augment = %q", got)
	}
}

// TestAugmentedPathAppendsUnconditionally confirms a dir is added even when it
// does not exist yet (so a first-time install that creates it is still found).
func TestAugmentedPathAppendsUnconditionally(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist", "bin")
	got := augmentedPath("/usr/bin", []string{missing})
	if !strings.Contains(got, missing) {
		t.Errorf("expected non-existent dir to be appended: %q", got)
	}
}

// TestCandidateDirs checks the override precedence (PIPX_BIN_DIR, GOBIN over
// GOPATH, CARGO_HOME) and the home-relative defaults.
func TestCandidateDirs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// On some platforms UserHomeDir reads other vars; set the common ones.
	t.Setenv("USERPROFILE", home)

	t.Setenv("PIPX_BIN_DIR", "/opt/pipx/bin")
	t.Setenv("GOBIN", "/opt/go/bin")
	t.Setenv("GOPATH", "/should/be/ignored")
	t.Setenv("CARGO_HOME", "/opt/cargo")

	dirs := CandidateDirs()
	has := func(p string) bool {
		for _, d := range dirs {
			if d == p {
				return true
			}
		}
		return false
	}
	for _, want := range []string{
		"/opt/pipx/bin",                      // PIPX_BIN_DIR honoured
		filepath.Join(home, ".local", "bin"), // and the default still included
		"/opt/go/bin",                        // GOBIN wins over GOPATH
		"/opt/cargo/bin",                     // CARGO_HOME/bin
		filepath.Join(home, ".deno", "bin"),
	} {
		if !has(want) {
			t.Errorf("CandidateDirs missing %q; got %v", want, dirs)
		}
	}
	if has(filepath.Join("/should/be/ignored", "bin")) {
		t.Errorf("GOPATH should be ignored when GOBIN is set; got %v", dirs)
	}

	// Without GOBIN, GOPATH's first entry wins.
	t.Setenv("GOBIN", "")
	t.Setenv("GOPATH", "/first"+string(os.PathListSeparator)+"/second")
	dirs = CandidateDirs()
	has = func(p string) bool {
		for _, d := range dirs {
			if d == p {
				return true
			}
		}
		return false
	}
	if !has(filepath.Join("/first", "bin")) || has(filepath.Join("/second", "bin")) {
		t.Errorf("GOPATH first-entry rule failed; got %v", dirs)
	}
}

// TestEnabled checks the OMNIS_PATH_AUGMENT opt-out.
func TestEnabled(t *testing.T) {
	for _, v := range []string{"false", "0", "no", "OFF"} {
		t.Setenv("OMNIS_PATH_AUGMENT", v)
		if enabled() {
			t.Errorf("OMNIS_PATH_AUGMENT=%q should disable", v)
		}
	}
	for _, v := range []string{"", "true", "1", "yes"} {
		t.Setenv("OMNIS_PATH_AUGMENT", v)
		if !enabled() {
			t.Errorf("OMNIS_PATH_AUGMENT=%q should stay enabled", v)
		}
	}
}

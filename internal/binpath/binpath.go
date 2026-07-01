// Package binpath augments the process $PATH with the standard user-local binary
// directories so tools installed there are found by exec.LookPath and by every
// subprocess omnis spawns (the Bash tool, hooks, MCP servers, language servers,
// run_tests, …).
//
// This matters because omnis auto-installs missing dependencies through the
// dependency gate (skill/MCP/LSP `requires`), and the common installers drop
// their binary into a per-user directory that is frequently *not* on the PATH the
// omnis process inherited — especially a server started from systemd or a minimal
// login environment:
//
//   - pipx / `pip install --user`      → ~/.local/bin        (PIPX_BIN_DIR)
//   - `go install …`                   → $GOBIN | $GOPATH/bin | ~/go/bin
//   - `cargo install …`                → $CARGO_HOME/bin | ~/.cargo/bin
//   - `deno install …`                 → ~/.deno/bin
//
// Without this, an install can *succeed* while the gate's post-install
// exec.LookPath still reports the binary missing (the classic "installed to
// ~/.local/bin but it's not on PATH" trap). So this fixes reliability for the
// existing gopls (`go install` → ~/go/bin) and rust-analyzer/cargo gates too, not
// just pipx-installed tools like ast-grep.
//
// It is **append-only and idempotent**: existing PATH entries keep their order
// and precedence (so a user's system binaries are never shadowed), and each
// directory is added at most once. The install-target directories are appended
// *unconditionally* (deduped) rather than only when they already exist, so a
// first-time install that creates the directory is still found on recheck — a
// non-existent PATH entry is simply skipped by exec.LookPath, so this is
// harmless. Disable with OMNIS_PATH_AUGMENT=false.
package binpath

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var once sync.Once

// Ensure appends the standard user-local bin directories to $PATH exactly once
// per process. Safe to call from every surface's startup; later calls are no-ops.
// Governed by OMNIS_PATH_AUGMENT (default on; "false"/"0"/"no" disables).
func Ensure() { once.Do(apply) }

func apply() {
	if !enabled() {
		return
	}
	dirs := CandidateDirs()
	if len(dirs) == 0 {
		return
	}
	if next := augmentedPath(os.Getenv("PATH"), dirs); next != "" {
		_ = os.Setenv("PATH", next)
	}
}

func enabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("OMNIS_PATH_AUGMENT"))) {
	case "false", "0", "no", "off":
		return false
	}
	return true
}

// CandidateDirs returns the user-local bin directories omnis's dependency
// installers target, honouring the standard overrides (PIPX_BIN_DIR, GOBIN,
// GOPATH, CARGO_HOME). Empty when the home directory can't be resolved.
func CandidateDirs() []string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil
	}
	var dirs []string
	add := func(p string) {
		if p != "" {
			dirs = append(dirs, p)
		}
	}

	// pipx / pip --user.
	if v := strings.TrimSpace(os.Getenv("PIPX_BIN_DIR")); v != "" {
		add(v)
	}
	add(filepath.Join(home, ".local", "bin"))

	// Go (go install).
	if v := strings.TrimSpace(os.Getenv("GOBIN")); v != "" {
		add(v)
	} else if v := strings.TrimSpace(os.Getenv("GOPATH")); v != "" {
		// GOPATH may be a list; the first entry's bin is where installs land.
		for _, g := range filepath.SplitList(v) {
			if g != "" {
				add(filepath.Join(g, "bin"))
				break
			}
		}
	} else {
		add(filepath.Join(home, "go", "bin"))
	}

	// Rust (cargo install).
	if v := strings.TrimSpace(os.Getenv("CARGO_HOME")); v != "" {
		add(filepath.Join(v, "bin"))
	} else {
		add(filepath.Join(home, ".cargo", "bin"))
	}

	// Deno (deno install).
	add(filepath.Join(home, ".deno", "bin"))

	return dirs
}

// augmentedPath returns cur with each directory in add appended (in order),
// skipping any that is empty or already present. Existing entries are left
// untouched, so precedence is preserved. Returns cur unchanged when nothing is
// added.
func augmentedPath(cur string, add []string) string {
	seen := map[string]bool{}
	for _, p := range filepath.SplitList(cur) {
		if p != "" {
			seen[filepath.Clean(p)] = true
		}
	}
	var extra []string
	for _, d := range add {
		if strings.TrimSpace(d) == "" {
			continue
		}
		cd := filepath.Clean(d)
		if seen[cd] {
			continue
		}
		seen[cd] = true
		extra = append(extra, cd)
	}
	if len(extra) == 0 {
		return cur
	}
	sep := string(os.PathListSeparator)
	if cur == "" {
		return strings.Join(extra, sep)
	}
	return cur + sep + strings.Join(extra, sep)
}

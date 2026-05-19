// Package paths centralises filesystem location resolution for yoke.
//
// Two roots are exposed:
//
//   - Home(): writable state root. Defaults to $HOME/.yoke; overridable via
//     $YOKE_HOME. Every mutable file produced by the running agent (logs,
//     uploads, mailboxes, soft-skills, user-edited config) is anchored
//     here so a yoke binary started from any working directory never
//     scatters state across the filesystem.
//
//   - ConfigSearchDirs(): read-only search chain for configuration files,
//     in high-to-low precedence:
//
//     1. Home()/config       — user overrides written by the web UI
//     2. ./config            — developer/local checkout (CWD-relative)
//     3. SystemConfigDir     — /etc/yoke by default; system-wide install
//
//     The whole chain can be replaced via $YOKE_CONFIG_DIRS (a list using
//     the OS path-list separator, ":" on Unix). FindConfig returns the
//     first existing file in the chain, falling back to the write target
//     under Home()/config when nothing exists yet — so callers can use the
//     returned path both for reading and creating.
//
// All resolution is done lazily from the current environment at call time
// so tests can set $YOKE_HOME / $YOKE_CONFIG_DIRS with t.Setenv and observe
// the effect immediately, without re-running init.
package paths

import (
	"os"
	"path/filepath"
	"strings"
)

const (
	envHome       = "YOKE_HOME"
	envConfigDirs = "YOKE_CONFIG_DIRS"
)

// SystemConfigDir is the lowest-precedence layer of the default config
// search chain. It's a package-level variable so distribution packagers
// can override it at build time via -ldflags for non-FHS targets.
var SystemConfigDir = "/etc/yoke"

// Home returns the directory under which all mutable yoke state lives.
// Lookup order: $YOKE_HOME, then $HOME/.yoke. Falls back to ".yoke"
// relative to CWD when no home directory is resolvable, so the binary
// still works in containers or CI environments that lack $HOME.
func Home() string {
	if v := strings.TrimSpace(os.Getenv(envHome)); v != "" {
		return v
	}
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		return filepath.Join(h, ".yoke")
	}
	return ".yoke"
}

// ConfigSearchDirs returns the config-file search chain in high-to-low
// precedence. $YOKE_CONFIG_DIRS, if set, replaces the chain wholesale
// (entries separated by the OS path-list separator).
func ConfigSearchDirs() []string {
	if v := strings.TrimSpace(os.Getenv(envConfigDirs)); v != "" {
		parts := strings.Split(v, string(os.PathListSeparator))
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p != "" {
				out = append(out, p)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return []string{
		filepath.Join(Home(), "config"),
		"config",
		SystemConfigDir,
	}
}

// ConfigWriteDir is the single directory to which yoke writes configuration
// files. The web UI editor, future per-user overrides, anything that
// persists "user-edited" config goes here. Always Home()/config.
func ConfigWriteDir() string { return filepath.Join(Home(), "config") }

// FindConfig resolves a filename against the config search chain and
// returns the first existing path. When the file exists in none of the
// layers, returns the would-be write path under ConfigWriteDir(): callers
// that just want to read should Stat the result; callers about to write
// already have a valid destination.
func FindConfig(name string) string {
	for _, dir := range ConfigSearchDirs() {
		p := filepath.Join(dir, name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return filepath.Join(ConfigWriteDir(), name)
}

// FindConfigDir resolves a subdirectory name against the config search chain
// and returns the first existing directory. Falls back to the
// write-target path under ConfigWriteDir() when no layer has it.
func FindConfigDir(name string) string {
	for _, dir := range ConfigSearchDirs() {
		p := filepath.Join(dir, name)
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			return p
		}
	}
	return filepath.Join(ConfigWriteDir(), name)
}

// LogsDir returns Home()/logs — per-session task graph, todo list, audit,
// statelog, and the global event log all land here.
func LogsDir() string { return filepath.Join(Home(), "logs") }

// UploadsDir returns Home()/logs/uploads — per-session web UI uploads.
func UploadsDir() string { return filepath.Join(LogsDir(), "uploads") }

// MailboxesDir returns Home()/mailboxes — JSONL inter-agent mailbox files.
// The directory is no longer dot-prefixed because Home() is itself hidden
// ($HOME/.yoke), so there's no reason to hide a child of an already
// hidden directory.
func MailboxesDir() string { return filepath.Join(Home(), "mailboxes") }

// SoftSkillsDir returns Home()/softskills — curator-distilled procedures.
// Always anchored under Home() (read AND write).
func SoftSkillsDir() string { return filepath.Join(Home(), "softskills") }

// SkillsDir returns the resolved skills directory. Skills follow the same
// 3-layer search as config files but rooted at the parent of each layer
// (i.e. ./skills, Home()/skills, and SystemConfigDir's sibling), falling
// back to Home()/skills when none exist.
func SkillsDir() string {
	for _, p := range skillsSearchDirs() {
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			return p
		}
	}
	return filepath.Join(Home(), "skills")
}

// SkillsRegistryDir returns the resolved registry/skills directory used
// by the web UI's installer. Same first-existing-wins fallback as
// SkillsDir so a developer checkout with `./registry/skills`
// keeps working, while fresh installs default to
// `$HOME/.yoke/registry/skills`.
func SkillsRegistryDir() string {
	for _, p := range skillsRegistrySearchDirs() {
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			return p
		}
	}
	return filepath.Join(Home(), "registry/skills")
}

// SkillsRegistryWriteDir is the write-target for skills installed or created by
// the web UI. Always anchored under Home() so saves never land in a local
// checkout's ./registry/skills directory — mirrors the config write contract.
func SkillsRegistryWriteDir() string { return filepath.Join(Home(), "registry/skills") }

// AgentsRegistryDir returns the resolved registry/agents directory used
// to load per-agent configuration files. Same first-existing-wins fallback
// as SkillsRegistryDir: a developer checkout with `./registry/agents`
// keeps working, while fresh installs default to `$HOME/.yoke/registry/agents`.
func AgentsRegistryDir() string {
	for _, p := range agentsRegistrySearchDirs() {
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			return p
		}
	}
	return filepath.Join(Home(), "registry/agents")
}

// AgentsRegistryWriteDir is the write-target for agents installed or modified by
// the web UI. Always anchored under Home() — mirrors the config write contract.
func AgentsRegistryWriteDir() string { return filepath.Join(Home(), "registry/agents") }

func skillsSearchDirs() []string {
	out := []string{filepath.Join(Home(), "skills"), "skills"}
	// Add SystemConfigDir's sibling (e.g. /etc/yoke -> /etc/yoke/skills).
	// /etc is FHS-typical for read-only system data; we keep skills under
	// the same prefix rather than splitting into /usr/share/yoke to keep
	// packaging simple.
	if SystemConfigDir != "" {
		out = append(out, filepath.Join(SystemConfigDir, "skills"))
	}
	return out
}

func skillsRegistrySearchDirs() []string {
	out := []string{
		filepath.Join(Home(), "registry/skills"),
		filepath.Join("registry/skills"),
	}
	if SystemConfigDir != "" {
		out = append(out, filepath.Join(SystemConfigDir, "registry/skills"))
	}
	return out
}

func agentsRegistrySearchDirs() []string {
	out := []string{
		filepath.Join(Home(), "registry/agents"),
		filepath.Join("registry/agents"),
	}
	if SystemConfigDir != "" {
		out = append(out, filepath.Join(SystemConfigDir, "registry/agents"))
	}
	return out
}

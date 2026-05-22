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
//     1. .agents            — project-local directory (CWD-relative)
//     2. Home()             — per-user state root ($HOME/.yoke)
//     3. SystemRegistryDir  — /etc/yoke/registry by default; system-wide install
//
//     The whole chain can be replaced via $YOKE_CONFIG_DIRS (a list using
//     the OS path-list separator, ":" on Unix). FindConfig returns the
//     first existing file in the chain, falling back to the write target
//     under Home() when nothing exists yet — so callers can use the
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

	// LocalDir is the project-local configuration directory (CWD-relative).
	// Place agents.json, permissions.json, registry/agents/, etc. here to
	// scope configuration to a single project checkout.
	LocalDir = ".agents"
)

// SystemConfigDir is the lowest-precedence base directory used for system-wide
// configuration. It's a package-level variable so distribution packagers can
// override it at build time via -ldflags for non-FHS targets.
var SystemConfigDir = "/etc/yoke"

// SystemRegistryDir is the lowest-precedence layer of the default config
// search chain. Derived from SystemConfigDir. It's where system-wide config
// files (agents.json, permissions.json, …) and registry subdirectories
// (registry/agents/, registry/skills/) live in a packaged installation.
func SystemRegistryDir() string {
	return filepath.Join(SystemConfigDir, "registry")
}

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
//
// Default chain:
//
//  1. .agents              — project-local (highest priority)
//  2. $YOKE_HOME           — per-user ($HOME/.yoke)
//  3. /etc/yoke/registry   — system-wide (lowest priority)
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
		LocalDir,
		Home(),
		SystemRegistryDir(),
	}
}

// ConfigWriteDir is the single directory to which yoke writes configuration
// files. The web UI editor, future per-user overrides, anything that
// persists "user-edited" config goes here. Always Home() ($YOKE_HOME).
func ConfigWriteDir() string { return Home() }

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
func MailboxesDir() string { return filepath.Join(Home(), "mailboxes") }

// SoftSkillsDir returns Home()/softskills — curator-distilled procedures.
// Always anchored under Home() (read AND write).
func SoftSkillsDir() string { return filepath.Join(Home(), "softskills") }

// SkillsAllSearchDirs returns every directory that should be scanned for skill
// definitions, across all layers and both the hand-crafted (skills/) and
// registry-installed (registry/skills/) sub-paths. Directories are ordered
// by descending precedence; callers that deduplicate by skill name should
// process them in order so the first occurrence wins.
func SkillsAllSearchDirs() []string {
	var out []string
	for _, base := range []string{LocalDir, Home()} {
		out = append(out,
			filepath.Join(base, "skills"),
			filepath.Join(base, "registry/skills"),
		)
	}
	if SystemConfigDir != "" {
		out = append(out, filepath.Join(SystemConfigDir, "registry/skills"))
	}
	return out
}

// SkillsRegistryDir returns the resolved registry/skills directory used
// by the web UI's installer. First-existing-wins across the three layers;
// defaults to Home()/registry/skills.
func SkillsRegistryDir() string {
	for _, p := range skillsRegistrySearchDirs() {
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			return p
		}
	}
	return filepath.Join(Home(), "registry/skills")
}

// SkillsRegistryWriteDir is the write-target for skills installed or created by
// the web UI. Always anchored under Home() so saves never land in .agents/ or
// /etc/yoke/registry — mirrors the config write contract.
func SkillsRegistryWriteDir() string { return filepath.Join(Home(), "registry/skills") }

// AgentsRegistryDir returns the resolved registry/agents directory used
// to load per-agent configuration files. First-existing-wins across the
// three layers; defaults to Home()/registry/agents.
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

func skillsRegistrySearchDirs() []string {
	out := []string{
		filepath.Join(LocalDir, "registry/skills"),
		filepath.Join(Home(), "registry/skills"),
	}
	if SystemConfigDir != "" {
		out = append(out, filepath.Join(SystemConfigDir, "registry/skills"))
	}
	return out
}

// AgentsRegistrySearchDirs returns all candidate agent registry directories in
// high-to-low precedence order. Unlike AgentsRegistryDir (which stops at the
// first existing directory), this returns the full list so callers can search
// each layer for a specific agent.
func AgentsRegistrySearchDirs() []string { return agentsRegistrySearchDirs() }

func agentsRegistrySearchDirs() []string {
	out := []string{
		filepath.Join(LocalDir, "registry/agents"),
		filepath.Join(Home(), "registry/agents"),
	}
	if SystemConfigDir != "" {
		out = append(out, filepath.Join(SystemConfigDir, "registry/agents"))
	}
	return out
}

// Package paths centralises filesystem location resolution for omnis.
//
// Two roots are exposed:
//
//   - Home(): writable state root. Defaults to $HOME/.omnis; overridable via
//     $OMNIS_HOME. Every mutable file produced by the running agent (logs,
//     uploads, mailboxes, soft-skills, user-edited config) is anchored
//     here so a omnis binary started from any working directory never
//     scatters state across the filesystem.
//
//   - ConfigSearchDirs(): read-only search chain for configuration files,
//     in high-to-low precedence:
//
//     1. .agents / agents   — project-local directories (CWD-relative).
//     `.agents/` is the canonical name; `agents/` is accepted as a
//     dotless alias. When both exist, `.agents/` wins; the alias is
//     included in the chain right after.
//     2. Home()             — per-user state root ($HOME/.omnis)
//     3. SystemConfigDir    — /etc/omnis by default; system-wide install.
//     Agent/skill registries live under `registry/agents` and
//     `registry/skills` in every layer.
//
//     One extra, registry-only layer sits BELOW all of the above in the
//     agent/skill search chains (but not in ConfigSearchDirs, which is for
//     config files): AgentSkillsDir (/etc/agentskills by default). When that
//     directory exists it is treated like a /etc/omnis/registry root — its
//     `agents/` and `skills/` subdirectories contribute definitions — letting
//     omnis share an Agent-Skills registry installed by other tools. omnis's own
//     /etc/omnis registry wins on name conflicts; see agentSkillsDir().
//
//     The whole chain can be replaced via $OMNIS_CONFIG_DIRS (a list using
//     the OS path-list separator, ":" on Unix). FindConfig returns the
//     first existing file in the chain, falling back to the write target
//     under Home() when nothing exists yet — so callers can use the
//     returned path both for reading and creating.
//
// All resolution is done lazily from the current environment at call time
// so tests can set $OMNIS_HOME / $OMNIS_CONFIG_DIRS with t.Setenv and observe
// the effect immediately, without re-running init.
package paths

import (
	"os"
	"path/filepath"
	"strings"
)

const (
	envHome       = "OMNIS_HOME"
	envConfigDirs = "OMNIS_CONFIG_DIRS"

	// envSystemConfigDir overrides just the system layer (SystemConfigDir)
	// at runtime, leaving the higher-precedence .agents and $HOME/.omnis
	// layers intact — unlike OMNIS_CONFIG_DIRS, which replaces the whole
	// chain. Distribution wrappers that install bundled config outside the
	// FHS /etc/omnis location use this: the Homebrew formula points it at
	// "$(brew --prefix)/etc/omnis" and the Windows MSI at "C:\ProgramData\Omnis".
	envSystemConfigDir = "OMNIS_SYSTEM_CONFIG_DIR"

	// envAgentSkillsDir overrides the location of the shared, lowest-precedence
	// Agent-Skills registry (AgentSkillsDir, default /etc/agentskills). Set it
	// to an empty value to disable the layer entirely.
	envAgentSkillsDir = "OMNIS_AGENTSKILLS_DIR"

	// LocalDir is the canonical project-local configuration directory
	// (CWD-relative). Place agents.json, permissions.json, registry/agents/,
	// etc. here to scope configuration to a single project checkout.
	LocalDir = ".agents"

	// LocalDirAlias is the dotless alternative name accepted alongside
	// LocalDir. When both exist, LocalDir wins; the alias is still part of
	// the search chain.
	LocalDirAlias = "agents"
)

// SystemConfigDir is the lowest-precedence base directory used for system-wide
// configuration AND the lowest-precedence layer of the default config search
// chain. Config files (agents.json, permissions.json, server.yaml, …) and the
// filters/ directory live directly under SystemConfigDir; the agent and skill
// registries live under SystemConfigDir/registry/agents and
// SystemConfigDir/registry/skills respectively. It's a package-level variable
// so distribution packagers can override it at build time via -ldflags for
// non-FHS targets. For runtime overrides (e.g. a package wrapper that can't
// know the install prefix at build time) use the OMNIS_SYSTEM_CONFIG_DIR env
// var instead — read via systemConfigDir().
var SystemConfigDir = "/etc/omnis"

// systemConfigDir returns the effective system-config base directory: the
// OMNIS_SYSTEM_CONFIG_DIR env override when set, otherwise the SystemConfigDir
// package variable. All internal resolution goes through this so a package
// wrapper can relocate the system layer without recompiling.
func systemConfigDir() string {
	if v := strings.TrimSpace(os.Getenv(envSystemConfigDir)); v != "" {
		return v
	}
	return SystemConfigDir
}

// SystemDir returns the effective system-config base directory — the
// OMNIS_SYSTEM_CONFIG_DIR override when set, otherwise the SystemConfigDir
// package variable. Exposed so callers outside this package (e.g. the
// AGENT.md resolver) can locate system-layer files without duplicating the
// override logic.
func SystemDir() string { return systemConfigDir() }

// AgentSkillsDir is an additional, lowest-precedence registry root shared with
// other Agent-Skills-aware tools. When this directory exists it is treated like
// /etc/omnis/registry — its agents/ and skills/ subdirectories contribute agent
// and skill definitions to the search chains — but it sits BELOW the /etc/omnis
// system layer, so omnis's own registry wins on name conflicts. It is never
// written to (omnis forks edited items into the user layer, like /etc/omnis).
// Package-level var so distribution packagers can relocate it via -ldflags;
// for runtime overrides use the OMNIS_AGENTSKILLS_DIR env var (set it empty to
// disable the layer) via agentSkillsDir().
var AgentSkillsDir = "/etc/agentskills"

// agentSkillsDir returns the effective shared Agent-Skills registry root: the
// OMNIS_AGENTSKILLS_DIR env override when set (including an explicit empty value,
// which disables the layer), otherwise the AgentSkillsDir package variable.
func agentSkillsDir() string {
	if v, ok := os.LookupEnv(envAgentSkillsDir); ok {
		return strings.TrimSpace(v)
	}
	return AgentSkillsDir
}

// LocalDirNames returns the candidate local-dir names in precedence order.
// `.agents/` (canonical) comes before `agents/` (alias).
func LocalDirNames() []string { return []string{LocalDir, LocalDirAlias} }

// LocalDirs returns the existing project-local directories at CWD in
// precedence order. If none exist, returns [LocalDir] as a baseline so
// callers always have a target to read from or write to.
func LocalDirs() []string {
	var out []string
	for _, name := range LocalDirNames() {
		if st, err := os.Stat(name); err == nil && st.IsDir() {
			out = append(out, name)
		}
	}
	if len(out) == 0 {
		return []string{LocalDir}
	}
	return out
}

// LocalWriteDir returns the highest-precedence existing local dir, falling
// back to LocalDir when none exist (so first writes land in the canonical
// `.agents/` rather than silently creating `agents/`).
func LocalWriteDir() string { return LocalDirs()[0] }

// Home returns the directory under which all mutable omnis state lives.
// Lookup order: $OMNIS_HOME, then $HOME/.omnis. Falls back to ".omnis"
// relative to CWD when no home directory is resolvable, so the binary
// still works in containers or CI environments that lack $HOME.
func Home() string {
	if v := strings.TrimSpace(os.Getenv(envHome)); v != "" {
		return v
	}
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		return filepath.Join(h, ".omnis")
	}
	return ".omnis"
}

// ConfigSearchDirs returns the config-file search chain in high-to-low
// precedence. $OMNIS_CONFIG_DIRS, if set, replaces the chain wholesale
// (entries separated by the OS path-list separator).
//
// Default chain:
//
//  1. .agents (and `agents/` when it exists) — project-local (highest priority)
//  2. $OMNIS_HOME                              — per-user ($HOME/.omnis)
//  3. /etc/omnis                               — system-wide (lowest priority)
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
	out := append([]string(nil), LocalDirs()...)
	out = append(out, Home(), systemConfigDir())
	return out
}

// Layer classifies a filesystem path as "local", "user", or "system" based
// on whether it lives under one of the local dirs, Home(), or anywhere else
// (treated as system). Comparison is done on absolute paths.
func Layer(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		abs = p
	}
	for _, name := range LocalDirNames() {
		localAbs, err := filepath.Abs(name)
		if err != nil {
			continue
		}
		if abs == localAbs || strings.HasPrefix(abs, localAbs+string(filepath.Separator)) {
			return "local"
		}
	}
	homeAbs := Home()
	if abs == homeAbs || strings.HasPrefix(abs, homeAbs+string(filepath.Separator)) {
		return "user"
	}
	return "system"
}

// ConfigWriteDir is the default per-user write directory ($OMNIS_HOME).
// Callers that need layer-aware writes should use WriteDirForLayer instead;
// this remains the default for any callsite that doesn't yet care about the
// distinction.
func ConfigWriteDir() string { return Home() }

// WriteDirForLayer returns the base directory omnis writes into for the
// given config-chain layer:
//
//   - "local"  → LocalWriteDir() (project-local `.agents/`)
//   - "user"   → Home() ($OMNIS_HOME)
//   - "system" → Home() (omnis never writes into /etc/omnis; fall back to user)
//
// Any unrecognised layer falls back to Home().
func WriteDirForLayer(layer string) string {
	if layer == "local" {
		return LocalWriteDir()
	}
	return Home()
}

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

// IndexDir returns Home()/index — semantic vector indexes (go-turbovec
// .tvim files, metadata sidecars, manifests) and the embedding cache.
// State (write root), not config, so always anchored under Home().
func IndexDir() string { return filepath.Join(Home(), "index") }

// SkillsAllSearchDirs returns every directory that should be scanned for skill
// definitions, across all layers and both the hand-crafted (skills/) and
// registry-installed (registry/skills/) sub-paths. Directories are ordered
// by descending precedence; callers that deduplicate by skill name should
// process them in order so the first occurrence wins.
func SkillsAllSearchDirs() []string {
	var out []string
	for _, base := range LocalDirs() {
		out = append(out,
			filepath.Join(base, "skills"),
			filepath.Join(base, "registry/skills"),
		)
	}
	out = append(out,
		filepath.Join(Home(), "skills"),
		filepath.Join(Home(), "registry/skills"),
	)
	if sys := systemConfigDir(); sys != "" {
		out = append(out, filepath.Join(sys, "registry/skills"))
	}
	if as := agentSkillsDir(); as != "" {
		out = append(out, filepath.Join(as, "skills"))
	}
	return out
}

// SkillsRegistryDir returns the resolved registry/skills directory used
// by the web UI's installer. First-existing-wins across the layers;
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
// the web UI when no specific layer is requested. Always anchored under
// Home() — mirrors the default config write contract. Use
// SkillsRegistryWriteDirForLayer to pick a layer explicitly.
func SkillsRegistryWriteDir() string { return filepath.Join(Home(), "registry/skills") }

// SkillsRegistryWriteDirForLayer returns the registry/skills write target
// under the requested config-chain layer ("local" → LocalWriteDir,
// "user"/"system" → Home).
func SkillsRegistryWriteDirForLayer(layer string) string {
	return filepath.Join(WriteDirForLayer(layer), "registry/skills")
}

// AgentsRegistryDir returns the resolved registry/agents directory used
// to load per-agent configuration files. First-existing-wins across the
// layers; defaults to Home()/registry/agents.
func AgentsRegistryDir() string {
	for _, p := range agentsRegistrySearchDirs() {
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			return p
		}
	}
	return filepath.Join(Home(), "registry/agents")
}

// AgentsRegistryWriteDir is the write-target for agents installed or
// modified by the web UI when no specific layer is requested. Always
// anchored under Home() — use AgentsRegistryWriteDirForLayer to pick a
// layer explicitly.
func AgentsRegistryWriteDir() string { return filepath.Join(Home(), "registry/agents") }

// AgentsRegistryWriteDirForLayer returns the registry/agents write target
// under the requested config-chain layer ("local" → LocalWriteDir,
// "user"/"system" → Home).
func AgentsRegistryWriteDirForLayer(layer string) string {
	return filepath.Join(WriteDirForLayer(layer), "registry/agents")
}

func skillsRegistrySearchDirs() []string {
	var out []string
	for _, base := range LocalDirs() {
		out = append(out, filepath.Join(base, "registry/skills"))
	}
	out = append(out, filepath.Join(Home(), "registry/skills"))
	if sys := systemConfigDir(); sys != "" {
		out = append(out, filepath.Join(sys, "registry/skills"))
	}
	if as := agentSkillsDir(); as != "" {
		out = append(out, filepath.Join(as, "skills"))
	}
	return out
}

// AgentsRegistrySearchDirs returns all candidate agent registry directories in
// high-to-low precedence order. Unlike AgentsRegistryDir (which stops at the
// first existing directory), this returns the full list so callers can search
// each layer for a specific agent.
func AgentsRegistrySearchDirs() []string { return agentsRegistrySearchDirs() }

// AgentSourceLayer returns the layer ("local"/"user"/"system") of the
// highest-precedence directory containing <name>/agent.json. Returns "" when
// the agent is not found in any layer (e.g. brand-new agents).
func AgentSourceLayer(name string) string {
	for _, dir := range agentsRegistrySearchDirs() {
		if _, err := os.Stat(filepath.Join(dir, name, "agent.json")); err == nil {
			return Layer(dir)
		}
	}
	return ""
}

// SkillSourceLayer returns the layer ("local"/"user"/"system") of the
// highest-precedence directory containing <name>/SKILL.md. Returns "" when
// the skill is not found in any layer.
func SkillSourceLayer(name string) string {
	for _, dir := range SkillsAllSearchDirs() {
		if _, err := os.Stat(filepath.Join(dir, name, "SKILL.md")); err == nil {
			return Layer(dir)
		}
	}
	return ""
}

func agentsRegistrySearchDirs() []string {
	var out []string
	for _, base := range LocalDirs() {
		out = append(out, filepath.Join(base, "registry/agents"))
	}
	out = append(out, filepath.Join(Home(), "registry/agents"))
	if sys := systemConfigDir(); sys != "" {
		out = append(out, filepath.Join(sys, "registry/agents"))
	}
	if as := agentSkillsDir(); as != "" {
		out = append(out, filepath.Join(as, "agents"))
	}
	return out
}

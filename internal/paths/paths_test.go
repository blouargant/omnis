package paths

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHomeDefault(t *testing.T) {
	t.Setenv("OMNIS_HOME", "")
	h, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no resolvable HOME")
	}
	if got := Home(); got != filepath.Join(h, ".omnis") {
		t.Fatalf("Home() = %q, want %q", got, filepath.Join(h, ".omnis"))
	}
}

func TestHomeEnvOverride(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("OMNIS_HOME", tmp)
	if got := Home(); got != tmp {
		t.Fatalf("Home() = %q, want %q", got, tmp)
	}
}

func TestConfigSearchDirsDefault(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("OMNIS_HOME", tmp)
	t.Setenv("OMNIS_CONFIG_DIRS", "")
	dirs := ConfigSearchDirs()
	if len(dirs) != 3 {
		t.Fatalf("ConfigSearchDirs() len = %d, want 3", len(dirs))
	}
	if dirs[0] != LocalDir {
		t.Errorf("first layer = %q, want %q", dirs[0], LocalDir)
	}
	if dirs[1] != tmp {
		t.Errorf("second layer = %q, want %q", dirs[1], tmp)
	}
	if dirs[2] != SystemConfigDir {
		t.Errorf("third layer = %q, want %q", dirs[2], SystemConfigDir)
	}
}

func TestConfigSearchDirsEnvOverride(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	t.Setenv("OMNIS_CONFIG_DIRS", a+string(os.PathListSeparator)+b)
	dirs := ConfigSearchDirs()
	if len(dirs) != 2 || dirs[0] != a || dirs[1] != b {
		t.Fatalf("ConfigSearchDirs() = %v, want [%s %s]", dirs, a, b)
	}
}

func TestConfigSearchDirsSystemOverride(t *testing.T) {
	tmp := t.TempDir()
	sys := t.TempDir()
	t.Setenv("OMNIS_HOME", tmp)
	t.Setenv("OMNIS_CONFIG_DIRS", "")
	t.Setenv("OMNIS_SYSTEM_CONFIG_DIR", sys)
	dirs := ConfigSearchDirs()
	// .agents and $HOME/.omnis layers must be preserved; only the system
	// (lowest-precedence) layer is relocated.
	if len(dirs) != 3 {
		t.Fatalf("ConfigSearchDirs() len = %d, want 3", len(dirs))
	}
	if dirs[0] != LocalDir {
		t.Errorf("first layer = %q, want %q", dirs[0], LocalDir)
	}
	if dirs[1] != tmp {
		t.Errorf("second layer = %q, want %q", dirs[1], tmp)
	}
	if dirs[2] != sys {
		t.Errorf("system layer = %q, want override %q", dirs[2], sys)
	}
}

func TestFindConfigPrecedence(t *testing.T) {
	home := t.TempDir()
	local := t.TempDir()
	system := t.TempDir()
	t.Setenv("OMNIS_HOME", home)
	t.Setenv("OMNIS_CONFIG_DIRS", local+string(os.PathListSeparator)+home+string(os.PathListSeparator)+system)

	mustWrite := func(dir, name string) string {
		t.Helper()
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}

	systemPath := mustWrite(system, "agent.json")
	if got := FindConfig("agent.json"); got != systemPath {
		t.Errorf("only-system: got %q, want %q", got, systemPath)
	}

	homePath := mustWrite(home, "agent.json")
	if got := FindConfig("agent.json"); got != homePath {
		t.Errorf("home-overrides-system: got %q, want %q", got, homePath)
	}

	localPath := mustWrite(local, "agent.json")
	if got := FindConfig("agent.json"); got != localPath {
		t.Errorf("local-overrides-home: got %q, want %q", got, localPath)
	}
}

func TestFindConfigMissingReturnsWriteTarget(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIS_HOME", home)
	t.Setenv("OMNIS_CONFIG_DIRS", "")
	want := filepath.Join(home, "nope.json")
	if got := FindConfig("nope.json"); got != want {
		t.Fatalf("FindConfig missing: got %q, want %q", got, want)
	}
}

func TestConfigWriteDirIsHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIS_HOME", home)
	if got := ConfigWriteDir(); got != home {
		t.Errorf("ConfigWriteDir() = %q, want %q", got, home)
	}
}

func TestStateDirsUnderHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIS_HOME", home)
	cases := []struct {
		name, want string
	}{
		{LogsDir(), filepath.Join(home, "logs")},
		{UploadsDir(), filepath.Join(home, "logs", "uploads")},
		{MailboxesDir(), filepath.Join(home, "mailboxes")},
		{SoftSkillsDir(), filepath.Join(home, "softskills")},
		{ConfigWriteDir(), home},
	}
	for _, c := range cases {
		if c.name != c.want {
			t.Errorf("got %q, want %q", c.name, c.want)
		}
	}
}

func TestAgentsRegistrySearchDirs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIS_HOME", home)
	t.Setenv("OMNIS_AGENTSKILLS_DIR", "")
	// Run from a clean CWD so the project's own .agents/ directory doesn't
	// participate in the chain.
	chdirTemp(t)
	dirs := AgentsRegistrySearchDirs()
	if len(dirs) != 3 {
		t.Fatalf("AgentsRegistrySearchDirs() len = %d, want 3", len(dirs))
	}
	if dirs[0] != filepath.Join(LocalDir, "registry/agents") {
		t.Errorf("first layer = %q, want %q", dirs[0], filepath.Join(LocalDir, "registry/agents"))
	}
	if dirs[1] != filepath.Join(home, "registry/agents") {
		t.Errorf("second layer = %q, want %q", dirs[1], filepath.Join(home, "registry/agents"))
	}
	wantSystem := filepath.Join(SystemConfigDir, "registry/agents")
	if dirs[2] != wantSystem {
		t.Errorf("third layer = %q, want %q", dirs[2], wantSystem)
	}
}

// TestAgentSkillsRegistryLayer verifies that the shared /etc/agentskills
// registry is appended as the lowest-precedence layer of both the agent and
// skill search chains, with its agents/ and skills/ subdirs (no registry/
// prefix), and that it can be relocated via OMNIS_AGENTSKILLS_DIR.
func TestAgentSkillsRegistryLayer(t *testing.T) {
	home := t.TempDir()
	extra := t.TempDir()
	t.Setenv("OMNIS_HOME", home)
	t.Setenv("OMNIS_AGENTSKILLS_DIR", extra)
	chdirTemp(t)

	agents := AgentsRegistrySearchDirs()
	if len(agents) != 4 {
		t.Fatalf("AgentsRegistrySearchDirs() len = %d, want 4: %v", len(agents), agents)
	}
	if last := agents[3]; last != filepath.Join(extra, "agents") {
		t.Errorf("agents last layer = %q, want %q", last, filepath.Join(extra, "agents"))
	}
	// It must sit below /etc/omnis.
	if agents[2] != filepath.Join(SystemConfigDir, "registry/agents") {
		t.Errorf("agents system layer = %q, want %q", agents[2], filepath.Join(SystemConfigDir, "registry/agents"))
	}

	skills := SkillsAllSearchDirs()
	if last := skills[len(skills)-1]; last != filepath.Join(extra, "skills") {
		t.Errorf("skills last layer = %q, want %q", last, filepath.Join(extra, "skills"))
	}
}

// TestAgentSkillsRegistryDisabled verifies that an explicit empty
// OMNIS_AGENTSKILLS_DIR removes the layer entirely, leaving /etc/omnis as the
// lowest-precedence registry.
func TestAgentSkillsRegistryDisabled(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIS_HOME", home)
	t.Setenv("OMNIS_AGENTSKILLS_DIR", "")
	chdirTemp(t)

	agents := AgentsRegistrySearchDirs()
	if len(agents) != 3 {
		t.Fatalf("AgentsRegistrySearchDirs() len = %d, want 3 when disabled: %v", len(agents), agents)
	}
	if last := agents[2]; last != filepath.Join(SystemConfigDir, "registry/agents") {
		t.Errorf("agents last layer = %q, want %q (no agentskills)", last, filepath.Join(SystemConfigDir, "registry/agents"))
	}
	skills := SkillsAllSearchDirs()
	if last := skills[len(skills)-1]; last != filepath.Join(SystemConfigDir, "registry/skills") {
		t.Errorf("skills last layer = %q, want %q (no agentskills)", last, filepath.Join(SystemConfigDir, "registry/skills"))
	}
}

// TestAgentSkillsRegistryDefault verifies the built-in default path is used
// when no override is set.
func TestAgentSkillsRegistryDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIS_HOME", home)
	os.Unsetenv("OMNIS_AGENTSKILLS_DIR")
	chdirTemp(t)

	agents := AgentsRegistrySearchDirs()
	if last := agents[len(agents)-1]; last != filepath.Join(AgentSkillsDir, "agents") {
		t.Errorf("default agents last layer = %q, want %q", last, filepath.Join(AgentSkillsDir, "agents"))
	}
	skills := SkillsAllSearchDirs()
	if last := skills[len(skills)-1]; last != filepath.Join(AgentSkillsDir, "skills") {
		t.Errorf("default skills last layer = %q, want %q", last, filepath.Join(AgentSkillsDir, "skills"))
	}
}

// chdirTemp moves the process into a fresh temp dir for the duration of t.
// Restores the original CWD on cleanup. Used by tests that probe local-dir
// behaviour and don't want the project's own .agents/ to leak into results.
func chdirTemp(t *testing.T) string {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	if err := os.Chdir(tmp); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })
	return tmp
}

func TestLocalDirsNoneExistsFallback(t *testing.T) {
	chdirTemp(t)
	got := LocalDirs()
	if len(got) != 1 || got[0] != LocalDir {
		t.Fatalf("LocalDirs() = %v, want [%q]", got, LocalDir)
	}
	if w := LocalWriteDir(); w != LocalDir {
		t.Fatalf("LocalWriteDir() = %q, want %q", w, LocalDir)
	}
}

func TestLocalDirsOnlyCanonical(t *testing.T) {
	tmp := chdirTemp(t)
	if err := os.Mkdir(filepath.Join(tmp, LocalDir), 0o755); err != nil {
		t.Fatal(err)
	}
	got := LocalDirs()
	if len(got) != 1 || got[0] != LocalDir {
		t.Fatalf("LocalDirs() = %v, want [%q]", got, LocalDir)
	}
}

func TestLocalDirsOnlyAlias(t *testing.T) {
	tmp := chdirTemp(t)
	if err := os.Mkdir(filepath.Join(tmp, LocalDirAlias), 0o755); err != nil {
		t.Fatal(err)
	}
	got := LocalDirs()
	if len(got) != 1 || got[0] != LocalDirAlias {
		t.Fatalf("LocalDirs() = %v, want [%q]", got, LocalDirAlias)
	}
	if w := LocalWriteDir(); w != LocalDirAlias {
		t.Fatalf("LocalWriteDir() = %q, want %q", w, LocalDirAlias)
	}
}

func TestLocalDirsBothPresent(t *testing.T) {
	tmp := chdirTemp(t)
	if err := os.Mkdir(filepath.Join(tmp, LocalDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(tmp, LocalDirAlias), 0o755); err != nil {
		t.Fatal(err)
	}
	got := LocalDirs()
	if len(got) != 2 || got[0] != LocalDir || got[1] != LocalDirAlias {
		t.Fatalf("LocalDirs() = %v, want [%q %q]", got, LocalDir, LocalDirAlias)
	}
	if w := LocalWriteDir(); w != LocalDir {
		t.Fatalf("LocalWriteDir() = %q, want %q", w, LocalDir)
	}
}

func TestConfigSearchDirsIncludesBothLocalDirs(t *testing.T) {
	tmp := chdirTemp(t)
	if err := os.Mkdir(filepath.Join(tmp, LocalDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(tmp, LocalDirAlias), 0o755); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	t.Setenv("OMNIS_HOME", home)
	t.Setenv("OMNIS_CONFIG_DIRS", "")
	dirs := ConfigSearchDirs()
	if len(dirs) != 4 {
		t.Fatalf("ConfigSearchDirs() = %v, want 4 entries", dirs)
	}
	if dirs[0] != LocalDir || dirs[1] != LocalDirAlias {
		t.Errorf("local layers = %v, want [%q %q]", dirs[:2], LocalDir, LocalDirAlias)
	}
	if dirs[2] != home {
		t.Errorf("user layer = %q, want %q", dirs[2], home)
	}
	if dirs[3] != SystemConfigDir {
		t.Errorf("system layer = %q, want %q", dirs[3], SystemConfigDir)
	}
}

func TestWriteDirForLayer(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIS_HOME", home)
	tmp := chdirTemp(t)
	if err := os.Mkdir(filepath.Join(tmp, LocalDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := WriteDirForLayer("local"); got != LocalDir {
		t.Errorf("local: got %q, want %q", got, LocalDir)
	}
	if got := WriteDirForLayer("user"); got != home {
		t.Errorf("user: got %q, want %q", got, home)
	}
	// "system" falls back to user — omnis never writes /etc/omnis.
	if got := WriteDirForLayer("system"); got != home {
		t.Errorf("system: got %q, want %q", got, home)
	}
	// AgentsRegistryWriteDirForLayer/SkillsRegistryWriteDirForLayer.
	if got := AgentsRegistryWriteDirForLayer("local"); got != filepath.Join(LocalDir, "registry/agents") {
		t.Errorf("agents/local: got %q", got)
	}
	if got := SkillsRegistryWriteDirForLayer("local"); got != filepath.Join(LocalDir, "registry/skills") {
		t.Errorf("skills/local: got %q", got)
	}
	if got := AgentsRegistryWriteDirForLayer("user"); got != filepath.Join(home, "registry/agents") {
		t.Errorf("agents/user: got %q", got)
	}
}

func TestLayerClassifiesPaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIS_HOME", home)
	tmp := chdirTemp(t)
	localAbs := filepath.Join(tmp, LocalDir)
	if err := os.MkdirAll(filepath.Join(localAbs, "registry/agents/foo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := Layer(filepath.Join(localAbs, "registry/agents/foo/agent.json")); got != "local" {
		t.Errorf(".agents/ path: got %q, want local", got)
	}
	aliasAbs := filepath.Join(tmp, LocalDirAlias)
	if err := os.MkdirAll(aliasAbs, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := Layer(filepath.Join(aliasAbs, "agents.json")); got != "local" {
		t.Errorf("agents/ path: got %q, want local", got)
	}
	if got := Layer(filepath.Join(home, "agents.json")); got != "user" {
		t.Errorf("home path: got %q, want user", got)
	}
	if got := Layer("/etc/omnis/agents.json"); got != "system" {
		t.Errorf("system path: got %q, want system", got)
	}
}

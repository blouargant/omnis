package paths

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHomeDefault(t *testing.T) {
	t.Setenv("YOKE_HOME", "")
	h, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no resolvable HOME")
	}
	if got := Home(); got != filepath.Join(h, ".yoke") {
		t.Fatalf("Home() = %q, want %q", got, filepath.Join(h, ".yoke"))
	}
}

func TestHomeEnvOverride(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("YOKE_HOME", tmp)
	if got := Home(); got != tmp {
		t.Fatalf("Home() = %q, want %q", got, tmp)
	}
}

func TestConfigSearchDirsDefault(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("YOKE_HOME", tmp)
	t.Setenv("YOKE_CONFIG_DIRS", "")
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
	wantSystem := filepath.Join(SystemConfigDir, "registry")
	if dirs[2] != wantSystem {
		t.Errorf("third layer = %q, want %q", dirs[2], wantSystem)
	}
}

func TestConfigSearchDirsEnvOverride(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	t.Setenv("YOKE_CONFIG_DIRS", a+string(os.PathListSeparator)+b)
	dirs := ConfigSearchDirs()
	if len(dirs) != 2 || dirs[0] != a || dirs[1] != b {
		t.Fatalf("ConfigSearchDirs() = %v, want [%s %s]", dirs, a, b)
	}
}

func TestFindConfigPrecedence(t *testing.T) {
	home := t.TempDir()
	local := t.TempDir()
	system := t.TempDir()
	t.Setenv("YOKE_HOME", home)
	t.Setenv("YOKE_CONFIG_DIRS", local+string(os.PathListSeparator)+home+string(os.PathListSeparator)+system)

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
	t.Setenv("YOKE_HOME", home)
	t.Setenv("YOKE_CONFIG_DIRS", "")
	want := filepath.Join(home, "nope.json")
	if got := FindConfig("nope.json"); got != want {
		t.Fatalf("FindConfig missing: got %q, want %q", got, want)
	}
}

func TestConfigWriteDirIsHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("YOKE_HOME", home)
	if got := ConfigWriteDir(); got != home {
		t.Errorf("ConfigWriteDir() = %q, want %q", got, home)
	}
}

func TestStateDirsUnderHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("YOKE_HOME", home)
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
	t.Setenv("YOKE_HOME", home)
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

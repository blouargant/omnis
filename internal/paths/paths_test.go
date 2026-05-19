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
	if dirs[0] != filepath.Join(tmp, "config") {
		t.Errorf("first layer = %q, want %q", dirs[0], filepath.Join(tmp, "config"))
	}
	if dirs[1] != "config" {
		t.Errorf("second layer = %q, want %q", dirs[1], "config")
	}
	if dirs[2] != SystemConfigDir {
		t.Errorf("third layer = %q, want %q", dirs[2], SystemConfigDir)
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
	t.Setenv("YOKE_CONFIG_DIRS", filepath.Join(home, "config")+string(os.PathListSeparator)+local+string(os.PathListSeparator)+system)

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

	localPath := mustWrite(local, "agent.json")
	if got := FindConfig("agent.json"); got != localPath {
		t.Errorf("local-overrides-system: got %q, want %q", got, localPath)
	}

	homePath := mustWrite(filepath.Join(home, "config"), "agent.json")
	if got := FindConfig("agent.json"); got != homePath {
		t.Errorf("home-overrides-local: got %q, want %q", got, homePath)
	}
}

func TestFindConfigMissingReturnsWriteTarget(t *testing.T) {
	home := t.TempDir()
	t.Setenv("YOKE_HOME", home)
	t.Setenv("YOKE_CONFIG_DIRS", "")
	want := filepath.Join(home, "config", "nope.json")
	if got := FindConfig("nope.json"); got != want {
		t.Fatalf("FindConfig missing: got %q, want %q", got, want)
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
		{ConfigWriteDir(), filepath.Join(home, "config")},
	}
	for _, c := range cases {
		if c.name != c.want {
			t.Errorf("got %q, want %q", c.name, c.want)
		}
	}
}

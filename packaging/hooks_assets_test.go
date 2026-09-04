package packaging

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The shipped hook must parse, must be fail_closed, and must point at a script
// that exists. A guard whose script is missing stops guarding silently, which
// internal/hooks' fail_closed turns into a block — but only if the declaration
// is right in the first place.
func TestShippedHooksConfigIsWellFormed(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "config", "hooks.json"))
	if err != nil {
		t.Fatalf("read config/hooks.json: %v", err)
	}
	var f struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Command    string `json:"command"`
				Timeout    int    `json:"timeout"`
				FailClosed bool   `json:"fail_closed"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("config/hooks.json does not parse: %v", err)
	}
	pre := f.Hooks["PreToolUse"]
	if len(pre) == 0 || len(pre[0].Hooks) == 0 {
		t.Fatal("no PreToolUse hook declared")
	}
	h := pre[0].Hooks[0]
	if !h.FailClosed {
		t.Fatal("the validation hook must be fail_closed, or a broken script silently stops validating")
	}
	if h.Timeout <= 0 {
		t.Fatal("the validation hook must declare a timeout (a cluster dry-run takes seconds)")
	}
	if !strings.Contains(h.Command, "OMNIS_SYSTEM_CONFIG_DIR") {
		t.Fatal("the command must resolve through OMNIS_SYSTEM_CONFIG_DIR so the non-FHS packages (brew/MSI/pip) find it")
	}
	if _, err := os.Stat(filepath.Join("..", "config", "hooks", "k8s-validate.py")); err != nil {
		t.Fatalf("the declared script does not exist: %v", err)
	}
}

// --- Per-channel shipping: structural, comment-stripped assertions.
//
// A substring match against a whole file (the original version of this test)
// is close to vacuous: ".goreleaser.yaml" contains "config/hooks/k8s-validate.py"
// at BOTH the nfpms entry and the brews install line, so deleting either one
// alone leaves the file green via the other; a needle can be satisfied by a
// documentation COMMENT (as omnis.wxs's top-of-file layout comment now is,
// deliberately, for humans) rather than by the code that actually ships the
// file; and a needle like the bare word "hooks" is satisfied by the asset
// whose presence WITHOUT the script is the catastrophic case (hooks.json)
// while the copytree that prevents exactly that could be deleted and the
// test would stay green. None of these assert the two properties the
// packaging ruling in CLAUDE.md actually turns on: mode 0755, and NOT
// "config|noreplace". Below, each channel gets a narrow, structural
// assertion instead.

// stripLineComments removes everything from the first unquoted "#" to the end
// of each line. "#" is the comment leader in every file these tests parse —
// YAML, the PowerShell embedded in release.yml's `run: |` block, and Python —
// so one helper covers all of them (omnis.wxs is asserted via its XML
// structure below instead, not by a stripped-text needle).
func stripLineComments(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if j := strings.IndexByte(line, '#'); j >= 0 {
			lines[i] = line[:j]
		}
	}
	return strings.Join(lines, "\n")
}

func readStripped(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return stripLineComments(string(data))
}

// goreleaserConfig is a deliberately minimal view of .goreleaser.yaml — only
// the fields these tests need, not the full goreleaser schema.
type goreleaserConfig struct {
	Nfpms []struct {
		Contents []struct {
			Src      string `yaml:"src"`
			Dst      string `yaml:"dst"`
			Type     string `yaml:"type"`
			FileInfo struct {
				Mode int `yaml:"mode"`
			} `yaml:"file_info"`
		} `yaml:"contents"`
	} `yaml:"nfpms"`
	Brews []struct {
		Install string `yaml:"install"`
	} `yaml:"brews"`
}

func loadGoreleaserConfig(t *testing.T) goreleaserConfig {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", ".goreleaser.yaml"))
	if err != nil {
		t.Fatalf("read .goreleaser.yaml: %v", err)
	}
	var cfg goreleaserConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf(".goreleaser.yaml does not parse as YAML: %v", err)
	}
	return cfg
}

// deb/rpm (nfpms): the hook script must be mode 0755 (it is invoked directly
// by python3, and must be readable/listable regardless) and must NOT be
// "config|noreplace" — see the ruling in CLAUDE.md: the script is executable
// code, not user configuration, so an upgrade must REPLACE it or a validation
// bug fix (this branch alone has fixed four verified bypasses) would never
// reach an already-installed machine.
func TestNfpmsShipsTheHookScriptExecutableAndReplaceable(t *testing.T) {
	cfg := loadGoreleaserConfig(t)
	if len(cfg.Nfpms) == 0 {
		t.Fatal("no nfpms block in .goreleaser.yaml")
	}
	var found bool
	for _, c := range cfg.Nfpms[0].Contents {
		if c.Src != "config/hooks/k8s-validate.py" {
			continue
		}
		found = true
		if c.FileInfo.Mode != 0o755 {
			t.Fatalf("nfpms hook script file_info.mode = %#o, want 0755", c.FileInfo.Mode)
		}
		if strings.Contains(c.Type, "noreplace") {
			t.Fatalf("nfpms hook script type = %q, must NOT be config|noreplace "+
				"(the script is executable code, not preserved config)", c.Type)
		}
	}
	if !found {
		t.Fatal("nfpms contents has no entry for config/hooks/k8s-validate.py")
	}
}

// nfpms' hooks.json entry is the complementary case — it must stay
// config|noreplace (an operator's edits survive an upgrade) so that a future
// change to either entry cannot silently make them identical.
func TestNfpmsShipsHooksConfigAsPreservedConfig(t *testing.T) {
	cfg := loadGoreleaserConfig(t)
	if len(cfg.Nfpms) == 0 {
		t.Fatal("no nfpms block in .goreleaser.yaml")
	}
	var found bool
	for _, c := range cfg.Nfpms[0].Contents {
		if c.Src != "config/hooks.json" {
			continue
		}
		found = true
		if !strings.Contains(c.Type, "noreplace") {
			t.Fatalf("nfpms hooks.json type = %q, want config|noreplace (an operator's edits must survive an upgrade)", c.Type)
		}
	}
	if !found {
		t.Fatal("nfpms contents has no entry for config/hooks.json")
	}
}

// Homebrew: the formula's install block (goreleaser's brews[0].install,
// literal Ruby source) must contain a REAL install statement for the script
// — not merely mention its name in a comment above it.
func TestHomebrewFormulaInstallsTheHookScript(t *testing.T) {
	cfg := loadGoreleaserConfig(t)
	if len(cfg.Brews) == 0 {
		t.Fatal("no brews block in .goreleaser.yaml")
	}
	install := stripLineComments(cfg.Brews[0].Install)
	if !strings.Contains(install, `.install "config/hooks/k8s-validate.py"`) {
		t.Fatalf("the Homebrew formula's install block does not install config/hooks/k8s-validate.py "+
			"as real Ruby code (comments stripped): %s", install)
	}
	if !strings.Contains(install, "chmod 0755") {
		t.Fatalf("the Homebrew formula does not chmod the hook script executable: %s", install)
	}
}

// Windows MSI: asserted at the level that actually determines what ships —
// the wildcard `<Files Include="$(var.StageDir)\data\**" />` in
// packaging/windows/omnis.wxs, which sweeps EVERYTHING the release.yml
// staging step puts under data\ into the MSI. Needling "k8s-validate.py" in
// this file (as the original version of this test did) can only ever match
// the file's own top-of-file documentation COMMENT describing the staged
// layout — the .wxs file contains no per-file XML element for it at all — so
// that assertion is dropped in favour of the wildcard that does the real
// work; asserting its presence is what would catch someone narrowing the
// glob to something that stops sweeping the hooks/ subdirectory.
func TestWindowsMSIWildcardSweepsStagedData(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("windows", "omnis.wxs"))
	if err != nil {
		t.Fatalf("read windows/omnis.wxs: %v", err)
	}
	if !strings.Contains(string(data), `Include="$(var.StageDir)\data\**"`) {
		t.Fatal(`omnis.wxs no longer sweeps "$(var.StageDir)\data\**" into the MSI — ` +
			"anything staged under data\\, including hooks\\k8s-validate.py, would silently stop shipping")
	}
}

// Windows MSI staging (.github/workflows/release.yml, the "msi" job): the
// hook SCRIPT must be staged (needled on the real Copy-Item statement, not a
// comment mentioning it) — and, per the Critical finding that the guard's
// POSIX command breaks EVERY Bash call under cmd.exe (see the long comment
// in release.yml and packaging/README.md), hooks.json's DECLARATION must be
// explicitly excluded from what gets staged. Both properties matter: shipping
// the declaration without a working guard is worse than shipping neither.
func TestMSIStagingShipsTheScriptButExcludesTheDeclaration(t *testing.T) {
	staging := readStripped(t, filepath.Join("..", ".github", "workflows", "release.yml"))
	if !strings.Contains(staging, `config\hooks"`) {
		t.Fatal(`release.yml's MSI staging step no longer copies "config\hooks" ` +
			"(the real Copy-Item statement, not a comment) — the hook script would not ship on Windows")
	}
	if !strings.Contains(staging, `-Exclude "hooks.json"`) {
		t.Fatal(`release.yml's MSI staging step no longer excludes "hooks.json" — ` +
			"the guard's POSIX-only command would be declared (and block every Bash call) on Windows; see Critical 1")
	}
}

// pip (scripts/build_wheels.py): needle the copytree SOURCE path, which can
// only appear in the real call that stages the script into sysconf/ — not the
// bare word "hooks", which the original version of this test used and which
// is satisfied by "hooks.json" alone (in CONFIG_FILES) even with the
// copytree call that ships the SCRIPT deleted entirely.
func TestPipStagesTheHookScriptDirectory(t *testing.T) {
	data := readStripped(t, filepath.Join("..", "scripts", "build_wheels.py"))
	if !strings.Contains(data, `os.path.join(REPO_ROOT, "config", "hooks")`) {
		t.Fatal("scripts/build_wheels.py no longer stages config/hooks/ into sysconf/ " +
			"(the real copytree call, not a comment) — the pip wheel would ship hooks.json with no script behind it")
	}
}

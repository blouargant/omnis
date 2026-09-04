package packaging

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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
	Archives []struct {
		Files []yaml.Node `yaml:"files"`
	} `yaml:"archives"`
	Nfpms []struct {
		Dependencies []string                     `yaml:"dependencies"`
		Overrides    map[string]nfpmsFormatConfig `yaml:"overrides"`
		Contents     []struct {
			Src      string `yaml:"src"`
			Dst      string `yaml:"dst"`
			Type     string `yaml:"type"`
			FileInfo struct {
				Mode int `yaml:"mode"`
			} `yaml:"file_info"`
		} `yaml:"contents"`
	} `yaml:"nfpms"`
	Brews []struct {
		Install      string `yaml:"install"`
		Dependencies []struct {
			Name string `yaml:"name"`
		} `yaml:"dependencies"`
	} `yaml:"brews"`
}

type nfpmsFormatConfig struct {
	Dependencies []string `yaml:"dependencies"`
}

// archivesFileSources returns the "src" of every archives[].files[] entry
// that is a {src, dst} mapping (a bare string entry like "LICENSE" carries
// no separate src and is skipped — it names itself).
func archivesFileSources(t *testing.T, files []yaml.Node) []string {
	t.Helper()
	var out []string
	for _, node := range files {
		if node.Kind != yaml.MappingNode {
			continue
		}
		var entry struct {
			Src string `yaml:"src"`
		}
		if err := node.Decode(&entry); err != nil {
			t.Fatalf("decode archives files entry: %v", err)
		}
		if entry.Src != "" {
			out = append(out, entry.Src)
		}
	}
	return out
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

// C1 (review round 2): the pip Windows wheels (win_amd64/win_arm64) still
// shipped hooks.json after round 1's fix, reproducing the exact brick —
// stage_assets() was platform-independent, and the pip launcher points
// OMNIS_SYSTEM_CONFIG_DIR at the SAME staged sysconf/ on every platform,
// Windows included. Exercised directly against stage_assets(goos), loaded as
// a module (mirrors subjectHashPy's pattern), rather than building real
// cross-compiled wheels — no cross-compiler toolchain is assumed here.
func TestPipStageAssetsExcludesHooksJSONOnWindowsOnly(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	buildWheelsPath, err := filepath.Abs(filepath.Join("..", "scripts", "build_wheels.py"))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	for _, goos := range []string{"windows", "linux", "darwin"} {
		t.Run(goos, func(t *testing.T) {
			dist := t.TempDir()
			py := `
import importlib.util, os, sys
spec = importlib.util.spec_from_file_location("build_wheels", ` + strconv.Quote(buildWheelsPath) + `)
mod = importlib.util.module_from_spec(spec)
spec.loader.exec_module(mod)
mod.DIST_STAGE = ` + strconv.Quote(dist) + `
mod.stage_assets(` + strconv.Quote(goos) + `)
sysconf = os.path.join(mod.DIST_STAGE, "sysconf")
print("hooks.json" in os.listdir(sysconf))
print(os.path.isfile(os.path.join(sysconf, "hooks", "k8s-validate.py")))
`
			cmd := exec.Command("python3", "-B", "-c", py)
			var out, errb bytes.Buffer
			cmd.Stdout, cmd.Stderr = &out, &errb
			if err := cmd.Run(); err != nil {
				t.Fatalf("stage_assets(%q) failed: %v\nstderr: %s", goos, err, errb.String())
			}
			lines := strings.Split(strings.TrimSpace(out.String()), "\n")
			if len(lines) != 2 {
				t.Fatalf("unexpected output: %q", out.String())
			}
			hasHooksJSON, hasScript := lines[0] == "True", lines[1] == "True"
			wantHooksJSON := goos != "windows"
			if hasHooksJSON != wantHooksJSON {
				t.Fatalf("goos=%s: hooks.json staged into sysconf/ = %v, want %v", goos, hasHooksJSON, wantHooksJSON)
			}
			if !hasScript {
				t.Fatalf("goos=%s: the hook SCRIPT must ship regardless of platform", goos)
			}
		})
	}
}

// M4 (review round 2): nothing pinned the .goreleaser.yaml `archives.files`
// entry for the hook script — the entry Critical 1's fix in round 1 added
// after discovering the Windows MSI job stages from exactly this archive.
// Losing it again would break the release BUILD itself (the MSI job's
// Copy-Item would have no source to copy), loudly, under CI — as opposed to
// the dependency pin below, which regresses silently.
func TestArchivesFilesShipsTheHookScript(t *testing.T) {
	cfg := loadGoreleaserConfig(t)
	if len(cfg.Archives) == 0 {
		t.Fatal("no archives block in .goreleaser.yaml")
	}
	srcs := archivesFileSources(t, cfg.Archives[0].Files)
	for _, s := range srcs {
		if s == "config/hooks/k8s-validate.py" {
			return
		}
	}
	t.Fatalf("archives[0].files has no entry for config/hooks/k8s-validate.py (found: %v)", srcs)
}

// M4: pin the python3 runtime dependency (Important 2) at the structural
// level nfpms actually resolves it at — the per-format `overrides` REPLACE
// rather than merge with the top-level `dependencies:`, which is exactly
// how it was silently defeated once already during this task (caught only
// by rebuilding a real .deb/.rpm and inspecting the package metadata, not by
// reading the YAML). Checks all three declaration sites: the top-level
// nfpms list, and both format overrides.
func TestNfpmsAndHomebrewDeclarePython3Dependency(t *testing.T) {
	cfg := loadGoreleaserConfig(t)
	if len(cfg.Nfpms) == 0 {
		t.Fatal("no nfpms block in .goreleaser.yaml")
	}
	has := func(deps []string) bool {
		for _, d := range deps {
			if d == "python3" {
				return true
			}
		}
		return false
	}
	if !has(cfg.Nfpms[0].Dependencies) {
		t.Fatalf("nfpms top-level dependencies does not declare python3: %v", cfg.Nfpms[0].Dependencies)
	}
	for _, format := range []string{"rpm", "deb"} {
		override, ok := cfg.Nfpms[0].Overrides[format]
		if !ok {
			t.Fatalf("nfpms has no %q override block", format)
		}
		if !has(override.Dependencies) {
			t.Fatalf("nfpms overrides.%s.dependencies does not restate python3 (%v) — "+
				"a format override REPLACES, not merges with, the top-level dependencies list, "+
				"so an empty override here would silently defeat the top-level declaration",
				format, override.Dependencies)
		}
	}
	if len(cfg.Brews) == 0 {
		t.Fatal("no brews block in .goreleaser.yaml")
	}
	var brewHasPython3 bool
	for _, d := range cfg.Brews[0].Dependencies {
		if d.Name == "python@3" {
			brewHasPython3 = true
		}
	}
	if !brewHasPython3 {
		t.Fatalf("brews[0].dependencies does not declare python@3: %v", cfg.Brews[0].Dependencies)
	}
}

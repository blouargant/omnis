package packaging

import (
	"path/filepath"
	"strings"
	"testing"
)

// The built-in soft-skills (softskills/, e.g. wrap-session) must reach the
// system config layer on every channel: omnis seeds them from
// <system-config-dir>/softskills into each user's $OMNIS_HOME/softskills at
// startup (internal/softskills.SeedBuiltins). The leaders' instructions call
// `load_softskill wrap-session`, so a channel that omits the tree turns every
// interactive session's wrap-up into a "skill not found" error — which is how
// this test came to exist.

func TestNfpmsShipsBuiltinSoftSkills(t *testing.T) {
	cfg := loadGoreleaserConfig(t)
	if len(cfg.Nfpms) == 0 {
		t.Fatal("no nfpms block in .goreleaser.yaml")
	}
	for _, c := range cfg.Nfpms[0].Contents {
		if c.Src == "softskills" && c.Dst == "/etc/omnis/softskills" && c.Type == "tree" {
			return
		}
	}
	t.Fatal("nfpms contents has no `softskills` tree installed at /etc/omnis/softskills")
}

func TestArchivesShipBuiltinSoftSkills(t *testing.T) {
	cfg := loadGoreleaserConfig(t)
	if len(cfg.Archives) == 0 {
		t.Fatal("no archives block in .goreleaser.yaml")
	}
	for _, s := range archivesFileSources(t, cfg.Archives[0].Files) {
		if s == "softskills" {
			return
		}
	}
	t.Fatal("archives[0].files does not ship softskills/ (the MSI and Homebrew channels stage from the archive)")
}

func TestHomebrewInstallsBuiltinSoftSkills(t *testing.T) {
	cfg := loadGoreleaserConfig(t)
	if len(cfg.Brews) == 0 {
		t.Fatal("no brews block in .goreleaser.yaml")
	}
	if !strings.Contains(stripLineComments(cfg.Brews[0].Install), `pkgshare.install "softskills"`) {
		t.Fatal(`the Homebrew formula does not pkgshare.install "softskills"`)
	}
}

func TestMSIStagingShipsBuiltinSoftSkills(t *testing.T) {
	staging := readStripped(t, filepath.Join("..", ".github", "workflows", "release.yml"))
	if !strings.Contains(staging, `Copy-Item "$ex\softskills" "$stage\data\softskills" -Recurse`) {
		t.Fatal(`release.yml's MSI staging step does not copy softskills\ into data\`)
	}
}

func TestPipStagesBuiltinSoftSkills(t *testing.T) {
	data := readStripped(t, filepath.Join("..", "scripts", "build_wheels.py"))
	if !strings.Contains(data, `os.path.join(REPO_ROOT, "softskills")`) {
		t.Fatal("scripts/build_wheels.py does not stage softskills/ into sysconf/")
	}
}

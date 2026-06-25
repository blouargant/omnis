package selfupdate

import (
	"runtime"
	"testing"
)

func TestSemverNewer(t *testing.T) {
	cases := []struct {
		latest, current string
		want            bool
	}{
		{"v1.2.3", "1.2.2", true},
		{"1.2.3", "v1.2.3", false},
		{"v1.3.0", "v1.2.9", true},
		{"v2.0.0", "v1.9.9", true},
		{"v1.2.3", "v1.2.4", false},
		{"v1.2.3-rc1", "1.2.2", true}, // pre-release suffix ignored for compare
		{"v1.2.3", "dev", false},      // dev current never updates
		{"v1.2.3", "", false},
		{"not-a-version", "1.0.0", false},
		{"v1.0", "1.0.0", false}, // 1.0 parses but equal -> not newer
		{"v1.0.1", "1.0", true},
	}
	for _, c := range cases {
		if got := semverNewer(c.latest, c.current); got != c.want {
			t.Errorf("semverNewer(%q,%q)=%v want %v", c.latest, c.current, got, c.want)
		}
	}
}

func TestAssetFor(t *testing.T) {
	assets := []ghAsset{
		{Name: "omnis_1.2.3_Linux_x86_64.deb", URL: "u-deb-amd64"},
		{Name: "omnis_1.2.3_Linux_arm64.deb", URL: "u-deb-arm64"},
		{Name: "omnis-1.2.3.x86_64.rpm", URL: "u-rpm-amd64"},
		{Name: "omnis-1.2.3.aarch64.rpm", URL: "u-rpm-arm64"},
		{Name: "omnis_1.2.3_windows_amd64.msi", URL: "u-msi"},
		{Name: "omnis_1.2.3_Linux_x86_64.tar.gz", URL: "u-tar"},
	}

	// brew/pip have no downloadable asset.
	if _, ok := assetFor(MethodBrew, assets); ok {
		t.Error("brew should have no asset")
	}
	if _, ok := assetFor(MethodPip, assets); ok {
		t.Error("pip should have no asset")
	}

	if runtime.GOARCH == "amd64" {
		a, ok := assetFor(MethodDeb, assets)
		if !ok || a.URL != "u-deb-amd64" {
			t.Errorf("deb amd64 asset = %+v ok=%v", a, ok)
		}
		r, ok := assetFor(MethodRpm, assets)
		if !ok || r.URL != "u-rpm-amd64" {
			t.Errorf("rpm amd64 asset = %+v ok=%v", r, ok)
		}
	}
	if runtime.GOARCH == "arm64" {
		a, ok := assetFor(MethodDeb, assets)
		if !ok || a.URL != "u-deb-arm64" {
			t.Errorf("deb arm64 asset = %+v ok=%v", a, ok)
		}
	}
}

func TestDetectMethodPaths(t *testing.T) {
	// pip detection is path-based and platform-independent.
	if m := DetectMethod("/home/u/.local/lib/python3.11/site-packages/omnis/_dist/bin/omnis-server"); m != MethodPip {
		t.Errorf("pip path detected as %v", m)
	}
	if m := DetectMethod(""); m != MethodUnknown {
		t.Errorf("empty path = %v want unknown", m)
	}
}

func TestManualInstructionsAlwaysHasReleaseLink(t *testing.T) {
	for _, m := range []Method{MethodDeb, MethodRpm, MethodBrew, MethodPip, MethodMSI, MethodRaw} {
		steps := ManualInstructions(m, "1.2.3", "asset.deb", "")
		if len(steps) == 0 {
			t.Fatalf("method %v: no steps", m)
		}
		last := steps[len(steps)-1]
		if last == "" || !contains(last, "github.com/"+Repo) {
			t.Errorf("method %v: last step %q lacks release link", m, last)
		}
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

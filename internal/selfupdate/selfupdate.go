// Package selfupdate lets the running omnis-server detect a newer stable release
// on GitHub and install it for whichever channel omnis was installed through
// (.deb/.rpm, Homebrew, Windows MSI, pip wheel, or a raw binary).
//
// The flow is host-side and server-only (CLI/TUI never call this): a background
// poller calls CheckLatest periodically; the web UI surfaces an "Update" button
// when a newer release exists; clicking it calls Install for the DetectMethod
// channel. Where automated install is impossible (raw binary) or fails, the
// caller falls back to ManualInstructions.
package selfupdate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Repo is the GitHub "owner/name" slug whose Releases drive the update check.
const Repo = "blouargant/omnis"

// ErrNotAutoInstallable is returned by Install when the detected install method
// has no automated path (raw binary / unknown). Callers should surface
// ManualInstructions instead.
var ErrNotAutoInstallable = errors.New("selfupdate: no automated install for this install method")

// Method identifies how the running binary was installed, which decides both
// the install command and the release asset to fetch.
type Method int

const (
	MethodUnknown Method = iota
	MethodDeb
	MethodRpm
	MethodBrew
	MethodMSI
	MethodPip
	MethodRaw
)

// String returns a stable lowercase identifier used in the JSON API and UI.
func (m Method) String() string {
	switch m {
	case MethodDeb:
		return "deb"
	case MethodRpm:
		return "rpm"
	case MethodBrew:
		return "brew"
	case MethodMSI:
		return "msi"
	case MethodPip:
		return "pip"
	case MethodRaw:
		return "raw"
	default:
		return "unknown"
	}
}

// NeedsSudo reports whether installing via this method requires a sudo password
// from the user (the web UI shows a password field only for these).
func (m Method) NeedsSudo() bool { return m == MethodDeb || m == MethodRpm }

// UpdateStatus is the cached result of a CheckLatest call, also the JSON shape
// returned by the server's /api/update/status route (minus the asset URL).
type UpdateStatus struct {
	Current     string   `json:"current"`
	Latest      string   `json:"latest"`
	Available   bool     `json:"available"`
	Method      string   `json:"method"`
	AssetName   string   `json:"asset_name"`
	AssetURL    string   `json:"-"`
	ManualSteps []string `json:"manual_steps"`
	ReleaseURL  string   `json:"release_url"`
	CheckedAt   string   `json:"checked_at"`
}

// ghRelease is the subset of the GitHub Releases API we consume.
type ghRelease struct {
	TagName    string    `json:"tag_name"`
	HTMLURL    string    `json:"html_url"`
	Prerelease bool      `json:"prerelease"`
	Draft      bool      `json:"draft"`
	Assets     []ghAsset `json:"assets"`
}

type ghAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

// CheckLatest queries the repo's latest stable release and builds an
// UpdateStatus relative to currentVersion and the detected install method. The
// GitHub /releases/latest endpoint already excludes drafts and prereleases, so
// only stable releases are considered. A "dev"/non-semver currentVersion never
// reports Available (so developers are not nagged).
func CheckLatest(ctx context.Context, repo, currentVersion string) (UpdateStatus, error) {
	method := DetectMethod(selfExe())
	st := UpdateStatus{
		Current:   currentVersion,
		Method:    method.String(),
		CheckedAt: time.Now().UTC().Format(time.RFC3339),
	}

	rel, err := fetchLatestRelease(ctx, repo)
	if err != nil {
		return st, err
	}
	st.Latest = strings.TrimPrefix(rel.TagName, "v")
	st.ReleaseURL = rel.HTMLURL
	if st.ReleaseURL == "" {
		st.ReleaseURL = "https://github.com/" + repo + "/releases/latest"
	}

	if semverNewer(rel.TagName, currentVersion) {
		st.Available = true
		if a, ok := assetFor(method, rel.Assets); ok {
			st.AssetName = a.Name
			st.AssetURL = a.URL
		}
	}
	st.ManualSteps = ManualInstructions(method, st.Latest, st.AssetName, st.ReleaseURL)
	return st, nil
}

func fetchLatestRelease(ctx context.Context, repo string) (ghRelease, error) {
	var rel ghRelease
	url := "https://api.github.com/repos/" + repo + "/releases/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return rel, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "omnis-selfupdate")
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return rel, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return rel, fmt.Errorf("selfupdate: github returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&rel); err != nil {
		return rel, fmt.Errorf("selfupdate: decode release: %w", err)
	}
	return rel, nil
}

// semverNewer reports whether latest is a strictly newer semantic version than
// current. Both may carry a leading "v". A non-semver or "dev" current always
// yields false, as does any parse failure — the conservative choice that avoids
// false update prompts.
func semverNewer(latest, current string) bool {
	lv, ok := parseSemver(latest)
	if !ok {
		return false
	}
	cv, ok := parseSemver(current)
	if !ok {
		return false
	}
	for i := 0; i < 3; i++ {
		if lv[i] != cv[i] {
			return lv[i] > cv[i]
		}
	}
	return false
}

// parseSemver extracts major.minor.patch from a tag like "v1.2.3" or
// "1.2.3-rc1" (the pre-release suffix is ignored for the numeric compare).
// Returns ok=false for "dev", empty, or otherwise unparseable input.
func parseSemver(s string) ([3]int, bool) {
	var out [3]int
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")
	if s == "" || strings.HasPrefix(s, "dev") {
		return out, false
	}
	// Drop any build/pre-release suffix: 1.2.3-rc1, 1.2.3+meta.
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	if len(parts) == 0 || len(parts) > 3 {
		return out, false
	}
	for i := 0; i < len(parts); i++ {
		n, err := strconv.Atoi(parts[i])
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// assetFor selects the release asset matching the install method and the
// running platform (GOOS/GOARCH). Methods that install via a package manager
// without a downloaded file (brew, pip) return ok=false.
func assetFor(method Method, assets []ghAsset) (ghAsset, bool) {
	var ext string
	switch method {
	case MethodDeb:
		ext = ".deb"
	case MethodRpm:
		ext = ".rpm"
	case MethodMSI:
		ext = ".msi"
	default:
		return ghAsset{}, false
	}
	archTokens := assetArchTokens(method)
	for _, a := range assets {
		name := strings.ToLower(a.Name)
		if !strings.HasSuffix(name, ext) {
			continue
		}
		if !matchesArchToken(name, archTokens) {
			continue
		}
		return a, true
	}
	return ghAsset{}, false
}

// assetArchTokens lists the substrings a package's filename may use for the
// current GOARCH, per packaging conventions (.deb uses Debian arch names, .rpm
// uses GNU arch names).
func assetArchTokens(method Method) []string {
	switch runtime.GOARCH {
	case "amd64":
		switch method {
		case MethodRpm:
			return []string{"x86_64", "amd64"}
		default:
			return []string{"amd64", "x86_64"}
		}
	case "arm64":
		switch method {
		case MethodRpm:
			return []string{"aarch64", "arm64"}
		default:
			return []string{"arm64", "aarch64"}
		}
	default:
		return []string{runtime.GOARCH}
	}
}

func matchesArchToken(name string, tokens []string) bool {
	for _, t := range tokens {
		if strings.Contains(name, t) {
			return true
		}
	}
	return false
}

// selfExe returns the absolute path of the running executable, or "" when it
// can't be resolved.
func selfExe() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		return resolved
	}
	return exe
}

// DetectMethod classifies how the binary at exePath was installed. Detection is
// ordered most-specific first; package-manager queries (dpkg/rpm) run only when
// the path heuristics don't already match pip/brew.
func DetectMethod(exePath string) Method {
	if exePath == "" {
		return MethodUnknown
	}
	lower := strings.ToLower(filepath.ToSlash(exePath))

	// pip wheel: the launcher execs the bundled binary under the package's
	// _dist tree inside a site-packages directory.
	if strings.Contains(lower, "/omnis/_dist/") || strings.Contains(lower, "site-packages/") {
		return MethodPip
	}

	switch runtime.GOOS {
	case "windows":
		if strings.Contains(lower, "/program files") || strings.Contains(lower, "/programdata/omnis") {
			return MethodMSI
		}
		return MethodRaw
	case "darwin":
		if strings.Contains(lower, "/cellar/omnis/") || strings.Contains(lower, "/homebrew/") {
			return MethodBrew
		}
		if prefix := brewPrefix(); prefix != "" && strings.HasPrefix(exePath, prefix) {
			return MethodBrew
		}
		return MethodRaw
	case "linux":
		if ownedByDpkg(exePath) {
			return MethodDeb
		}
		if ownedByRpm(exePath) {
			return MethodRpm
		}
		return MethodRaw
	default:
		return MethodRaw
	}
}

func brewPrefix() string {
	out, err := exec.Command("brew", "--prefix").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func ownedByDpkg(exePath string) bool {
	if _, err := exec.LookPath("dpkg-query"); err != nil {
		return false
	}
	return exec.Command("dpkg-query", "-S", exePath).Run() == nil
}

func ownedByRpm(exePath string) bool {
	if _, err := exec.LookPath("rpm"); err != nil {
		return false
	}
	return exec.Command("rpm", "-qf", exePath).Run() == nil
}

// Install attempts to install the release described by st for its method. For
// deb/rpm the sudoPassword is piped to `sudo -S`. Methods without an automated
// path return ErrNotAutoInstallable so the caller can show ManualInstructions.
// On success the new binary is on disk; the process must be restarted to run it.
func Install(ctx context.Context, st UpdateStatus, sudoPassword string) error {
	method := methodFromString(st.Method)
	switch method {
	case MethodBrew:
		return runInstall(ctx, "", "brew", "upgrade", "omnis")
	case MethodPip:
		return runInstall(ctx, "", pipExe(), "install", "-U", "omnis-agent")
	case MethodDeb:
		file, cleanup, err := downloadAsset(ctx, st.AssetURL)
		if err != nil {
			return err
		}
		defer cleanup()
		return runInstall(ctx, sudoPassword, "sudo", "-S", "apt-get", "install", "-y", "--allow-downgrades", file)
	case MethodRpm:
		file, cleanup, err := downloadAsset(ctx, st.AssetURL)
		if err != nil {
			return err
		}
		defer cleanup()
		return runInstall(ctx, sudoPassword, "sudo", "-S", rpmInstaller(), "install", "-y", file)
	case MethodMSI:
		file, cleanup, err := downloadAsset(ctx, st.AssetURL)
		if err != nil {
			return err
		}
		defer cleanup()
		return runInstall(ctx, "", "msiexec", "/i", file, "/qb")
	default:
		return ErrNotAutoInstallable
	}
}

// runInstall runs cmd with a generous timeout. When stdin is non-empty it is
// written to the process stdin (used to feed `sudo -S` the password followed by
// a newline). The password is never logged; only combined output is returned in
// the error.
func runInstall(ctx context.Context, stdin, name string, args ...string) error {
	if _, err := exec.LookPath(name); err != nil {
		return fmt.Errorf("selfupdate: %q not found on PATH: %w", name, err)
	}
	cctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(cctx, name, args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin + "\n")
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("selfupdate: %s failed: %w\n%s", name, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func methodFromString(s string) Method {
	switch s {
	case "deb":
		return MethodDeb
	case "rpm":
		return MethodRpm
	case "brew":
		return MethodBrew
	case "msi":
		return MethodMSI
	case "pip":
		return MethodPip
	case "raw":
		return MethodRaw
	default:
		return MethodUnknown
	}
}

func rpmInstaller() string {
	if _, err := exec.LookPath("dnf"); err == nil {
		return "dnf"
	}
	return "yum"
}

func pipExe() string {
	if _, err := exec.LookPath("pipx"); err == nil {
		// pipx upgrades the isolated app install; fall back to pip otherwise.
		return "pip"
	}
	if _, err := exec.LookPath("pip3"); err == nil {
		return "pip3"
	}
	return "pip"
}

// downloadAsset fetches url into a temp file and returns its path plus a cleanup
// func. The caller must invoke cleanup when done.
func downloadAsset(ctx context.Context, url string) (string, func(), error) {
	noop := func() {}
	if url == "" {
		return "", noop, errors.New("selfupdate: no download URL for this asset")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", noop, err
	}
	req.Header.Set("User-Agent", "omnis-selfupdate")
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return "", noop, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", noop, fmt.Errorf("selfupdate: download %s returned %s", url, resp.Status)
	}

	name := filepath.Base(url)
	if name == "" || name == "." || name == "/" {
		name = "omnis-update"
	}
	f, err := os.CreateTemp("", "omnis-update-*-"+name)
	if err != nil {
		return "", noop, err
	}
	cleanup := func() { os.Remove(f.Name()) }
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		cleanup()
		return "", noop, err
	}
	if err := f.Close(); err != nil {
		cleanup()
		return "", noop, err
	}
	return f.Name(), cleanup, nil
}

// ManualInstructions returns copy-paste commands the user can run themselves
// when the automated install is unavailable or fails. The list always ends with
// a link to the Releases page.
func ManualInstructions(method Method, latest, assetName, releaseURL string) []string {
	if releaseURL == "" {
		releaseURL = "https://github.com/" + Repo + "/releases/latest"
	}
	asset := assetName
	if asset == "" {
		asset = "<the asset for your platform>"
	}
	var steps []string
	switch method {
	case MethodDeb:
		steps = []string{
			fmt.Sprintf("Download %s from the release page.", asset),
			fmt.Sprintf("sudo apt-get install -y ./%s", asset),
		}
	case MethodRpm:
		steps = []string{
			fmt.Sprintf("Download %s from the release page.", asset),
			fmt.Sprintf("sudo dnf install -y ./%s", asset),
		}
	case MethodBrew:
		steps = []string{
			"brew update",
			"brew upgrade omnis",
		}
	case MethodPip:
		steps = []string{
			"pip install -U omnis-agent",
		}
	case MethodMSI:
		steps = []string{
			fmt.Sprintf("Download %s from the release page.", asset),
			fmt.Sprintf("msiexec /i %s", asset),
		}
	default: // raw / unknown
		steps = []string{
			"Download the archive for your platform from the release page.",
			"Replace the existing omnis / omnis-server binaries with the extracted ones.",
		}
	}
	steps = append(steps, "Release page: "+releaseURL)
	return steps
}

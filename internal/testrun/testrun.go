// Package testrun implements the run_tests tool: a targeted, structured test
// runner for the coding squad. It detects the project's test framework from its
// marker files (go.mod → `go test`, Cargo.toml → `cargo test`, pyproject.toml →
// `pytest`, …), runs the suite in the session's working directory through the
// same safety-floored shell as the Bash tool, and parses the output into a
// compact pass/fail summary with the failing test names — so the agent can close
// the edit→verify loop without reading a raw multi-thousand-line log.
//
// It deliberately exposes no free-form command field: the base command per
// framework is fixed (an allowlist) and the optional `scope` is charset-validated,
// so run_tests cannot be used to smuggle arbitrary shell commands past the
// permission layer. For very long suites the agent still has bash_background /
// monitor; run_tests is the quick, targeted, foreground path.
package testrun

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"

	fstools "github.com/blouargant/omnis/core/tools"
)

const (
	defaultTimeout = 5 * time.Minute
	maxTimeout     = 10 * time.Minute
	// tailBytes bounds how much raw output is echoed back after the summary.
	tailBytes = 4000
)

// framework describes one test toolchain: the marker files that identify a
// project using it, and the base command that runs its suite. detect walks this
// list in order; the first framework whose marker exists in cwd wins.
type framework struct {
	name    string
	markers []string
	// command builds the shell command for cwd + an (already-validated) scope.
	command func(cwd, scope string) string
}

// frameworks is the ordered detection table. Compiled/typed ecosystems come
// before generic script-runner ones (package.json) so a Go/Rust repo that also
// ships a web package.json still resolves to its primary toolchain.
var frameworks = []framework{
	{"go", []string{"go.mod", "go.work"}, func(_, scope string) string {
		if scope != "" {
			return "go test " + scope
		}
		return "go test ./..."
	}},
	{"cargo", []string{"Cargo.toml"}, func(_, scope string) string {
		return joinCmd("cargo test", scope)
	}},
	{"maven", []string{"pom.xml"}, func(cwd, scope string) string {
		return joinCmd(wrapperOr(cwd, "mvnw", "mvn")+" -q test", scope)
	}},
	{"gradle", []string{"build.gradle", "build.gradle.kts", "settings.gradle", "settings.gradle.kts"}, func(cwd, scope string) string {
		return joinCmd(wrapperOr(cwd, "gradlew", "gradle")+" test", scope)
	}},
	{"mix", []string{"mix.exs"}, func(_, scope string) string {
		return joinCmd("mix test", scope)
	}},
	{"zig", []string{"build.zig"}, func(_, scope string) string {
		return joinCmd("zig build test", scope)
	}},
	{"swift", []string{"Package.swift"}, func(_, scope string) string {
		return joinCmd("swift test", scope)
	}},
	{"dart", []string{"pubspec.yaml"}, func(_, scope string) string {
		return joinCmd("dart test", scope)
	}},
	{"deno", []string{"deno.json", "deno.jsonc"}, func(_, scope string) string {
		return joinCmd("deno test", scope)
	}},
	{"rspec", []string{".rspec"}, func(_, scope string) string {
		return joinCmd("bundle exec rspec", scope)
	}},
	{"rake", []string{"Rakefile"}, func(_, scope string) string {
		return joinCmd("rake test", scope)
	}},
	{"composer", []string{"composer.json"}, func(_, scope string) string {
		return joinCmd("composer test", scope)
	}},
	{"pytest", []string{"pyproject.toml", "setup.py", "setup.cfg", "pytest.ini", "tox.ini", "conftest.py"}, func(_, scope string) string {
		return joinCmd("pytest", scope)
	}},
	{"pnpm", []string{"pnpm-lock.yaml"}, func(_, scope string) string {
		return joinCmd("pnpm test", scope)
	}},
	{"yarn", []string{"yarn.lock"}, func(_, scope string) string {
		return joinCmd("yarn test", scope)
	}},
	{"npm", []string{"package.json"}, func(_, scope string) string {
		return joinCmd("npm test", scope)
	}},
}

// byName indexes frameworks for the explicit `framework` override.
var byName = func() map[string]framework {
	m := make(map[string]framework, len(frameworks))
	for _, f := range frameworks {
		m[f.name] = f
	}
	return m
}()

// scopeRe restricts the optional scope to a package/path/test-id token — no
// whitespace or shell metacharacters, so it can never inject a second command.
var scopeRe = regexp.MustCompile(`^[A-Za-z0-9._/:@=+*-]+$`)

type runTestsIn struct {
	Framework string `json:"framework,omitempty"`
	Scope     string `json:"scope,omitempty"`
	Timeout   int    `json:"timeout_seconds,omitempty"`
}
type runTestsOut struct {
	Result string `json:"result"`
}

// Tools returns the run_tests tool set (one tool).
func Tools() []tool.Tool {
	t, err := functiontool.New(functiontool.Config{
		Name: "run_tests",
		Description: "Run the project's test suite and get a structured pass/fail summary with the failing test names — " +
			"the verify step after an edit. The framework is auto-detected from the project (go.mod → go test, " +
			"Cargo.toml → cargo test, pyproject.toml → pytest, package.json → npm test, and Maven/Gradle/Mix/Zig/Swift/" +
			"Dart/Deno/RSpec/Composer). Runs in the session's working directory. " +
			"Arguments: `framework` (string, optional) — force a toolchain when detection is ambiguous " +
			"(go|cargo|maven|gradle|mix|zig|swift|dart|deno|rspec|rake|composer|pytest|pnpm|yarn|npm); " +
			"`scope` (string, optional) — narrow the run to one package/path/test (e.g. `./internal/lsp/...` for go, " +
			"`tests/test_x.py::TestY` for pytest, a test-name filter for cargo); " +
			"`timeout_seconds` (int, optional, default 300, max 600). For very long suites use bash_background instead.",
	}, func(ctx tool.Context, in runTestsIn) (runTestsOut, error) {
		cwd := fstools.CwdForContext(ctx)
		out, err := run(ctx, cwd, in)
		if err != nil {
			return runTestsOut{Result: err.Error()}, nil
		}
		return runTestsOut{Result: out}, nil
	})
	if err != nil {
		panic(fmt.Errorf("build run_tests tool: %w", err))
	}
	return []tool.Tool{t}
}

// run resolves the framework, builds the command, executes it, and summarises.
// A returned error carries a model-facing message (the tool maps it into Result).
func run(ctx context.Context, cwd string, in runTestsIn) (string, error) {
	scope := strings.TrimSpace(in.Scope)
	if scope != "" && !scopeRe.MatchString(scope) {
		return "", fmt.Errorf("run_tests: invalid scope %q — use a package/path/test id with no spaces or shell characters", scope)
	}

	var fw framework
	if name := strings.TrimSpace(in.Framework); name != "" {
		f, ok := byName[strings.ToLower(name)]
		if !ok {
			return "", fmt.Errorf("run_tests: unknown framework %q; supported: %s", name, supportedNames())
		}
		fw = f
	} else {
		f, ok := detect(cwd)
		if !ok {
			return "", fmt.Errorf("run_tests: could not detect a test framework in %s — pass `framework` explicitly (%s)", dirLabel(cwd), supportedNames())
		}
		fw = f
	}

	timeout := defaultTimeout
	if in.Timeout > 0 {
		timeout = time.Duration(in.Timeout) * time.Second
		if timeout > maxTimeout {
			timeout = maxTimeout
		}
	}

	cmd := fw.command(cwd, scope)
	res := fstools.RunShellCaptured(ctx, cmd, cwd, nil, timeout)
	return summarize(fw.name, cmd, res), nil
}

// detect returns the first framework whose marker file exists in cwd.
func detect(cwd string) (framework, bool) {
	base := cwd
	if base == "" {
		base = "."
	}
	for _, f := range frameworks {
		for _, m := range f.markers {
			if fileExists(filepath.Join(base, m)) {
				return f, true
			}
		}
	}
	return framework{}, false
}

// summarize turns a captured run into a compact, model-friendly report: a
// pass/fail status line, the framework's own summary line and failing test names
// where we can extract them, then a bounded tail of the raw output (ground truth).
func summarize(fwName, cmd string, res fstools.CapturedRun) string {
	combined := res.Stdout
	if res.Stderr != "" {
		if combined != "" {
			combined += "\n"
		}
		combined += res.Stderr
	}

	var status string
	switch {
	case res.Blocked:
		status = "✗ blocked by safety floor: " + strings.TrimSpace(res.Stderr)
	case res.TimedOut:
		status = "✗ tests timed out"
	case res.ExitCode == 0:
		status = "✓ tests passed"
	default:
		status = fmt.Sprintf("✗ tests FAILED (exit %d)", res.ExitCode)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s — %s: %s\n", status, fwName, cmd)

	if !res.Blocked {
		summaryLine, failing := extractSignals(fwName, combined)
		if summaryLine != "" {
			b.WriteString(summaryLine + "\n")
		}
		if len(failing) > 0 {
			const cap = 25
			shown := failing
			more := 0
			if len(shown) > cap {
				more = len(shown) - cap
				shown = shown[:cap]
			}
			fmt.Fprintf(&b, "Failing (%d): %s", len(failing), strings.Join(shown, ", "))
			if more > 0 {
				fmt.Fprintf(&b, " … +%d more", more)
			}
			b.WriteByte('\n')
		}
	}

	if tail := lastBytes(strings.TrimRight(combined, "\n"), tailBytes); tail != "" {
		b.WriteString("\n--- output ---\n")
		b.WriteString(tail)
	}
	return strings.TrimRight(b.String(), "\n")
}

var (
	goFailRe     = regexp.MustCompile(`^\s*--- FAIL: (\S+)`)
	goPkgFailRe  = regexp.MustCompile(`^(FAIL|ok)\s+(\S+)`)
	pytestSumRe  = regexp.MustCompile(`^=+.*\b(passed|failed|error|errors|skipped)\b.*=+$`)
	pytestFailRe = regexp.MustCompile(`^(FAILED|ERROR) (\S+)`)
	cargoSumRe   = regexp.MustCompile(`^test result:`)
	cargoFailRe  = regexp.MustCompile(`^\s*(\S+) \.\.\. FAILED`)
)

// extractSignals pulls a framework-specific summary line and the failing test
// names out of the combined output. Best-effort: unknown frameworks (and clean
// runs) return empty, and the raw tail below still carries the ground truth.
func extractSignals(fwName, combined string) (summary string, failing []string) {
	lines := strings.Split(combined, "\n")
	seen := map[string]bool{}
	add := func(name string) {
		if name != "" && !seen[name] {
			seen[name] = true
			failing = append(failing, name)
		}
	}
	for _, ln := range lines {
		t := strings.TrimRight(ln, "\r")
		switch fwName {
		case "go":
			if m := goFailRe.FindStringSubmatch(t); m != nil {
				add(m[1])
			} else if m := goPkgFailRe.FindStringSubmatch(t); m != nil && m[1] == "FAIL" {
				add(m[2])
			}
		case "pytest":
			if pytestSumRe.MatchString(strings.TrimSpace(t)) {
				summary = strings.TrimSpace(t)
			}
			if m := pytestFailRe.FindStringSubmatch(strings.TrimSpace(t)); m != nil {
				add(m[2])
			}
		case "cargo":
			if cargoSumRe.MatchString(strings.TrimSpace(t)) {
				summary = strings.TrimSpace(t)
			}
			if m := cargoFailRe.FindStringSubmatch(t); m != nil {
				add(m[1])
			}
		}
	}
	sort.Strings(failing)
	return summary, failing
}

// --- small helpers ---

// joinCmd appends a validated scope arg to a base command.
func joinCmd(base, scope string) string {
	if scope == "" {
		return base
	}
	return base + " " + scope
}

// wrapperOr returns "./"+wrapper when a build-tool wrapper script exists in cwd
// (gradlew/mvnw), else the plain tool name — matching how these projects are run.
func wrapperOr(cwd, wrapper, plain string) string {
	if cwd != "" && (fileExists(filepath.Join(cwd, wrapper)) || fileExists(filepath.Join(cwd, wrapper+".bat"))) {
		return "./" + wrapper
	}
	return plain
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func supportedNames() string {
	names := make([]string, 0, len(frameworks))
	for _, f := range frameworks {
		names = append(names, f.name)
	}
	return strings.Join(names, "|")
}

func dirLabel(cwd string) string {
	if cwd == "" {
		return "the working directory"
	}
	return cwd
}

// lastBytes returns the final n bytes of s, prefixed with an ellipsis marker
// when it had to truncate, cutting on a line boundary for readability.
func lastBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := s[len(s)-n:]
	if i := strings.IndexByte(cut, '\n'); i >= 0 && i+1 < len(cut) {
		cut = cut[i+1:]
	}
	return "…(truncated)\n" + cut
}

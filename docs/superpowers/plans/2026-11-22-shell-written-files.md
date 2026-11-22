# Files Written Through the Shell — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** When the `Bash` tool writes a file, omnis sends `file_changed`, records a new file in the session's produced files, and fuses diagnostics, as it does for `Write`/`Edit`.

**Architecture:** A text-only parser in `core/permissions` lists the paths a command may write. The `Bash` tool stats those candidates before and after the command and stores the files really written under the tool call id (`core/tools`). The server's stream consumer and the coding-efficiency plugin read that store.

**Tech Stack:** Go (stdlib only), existing packages `core/permissions`, `core/tools`, `server`, `agent`.

**Spec:** `docs/superpowers/specs/2026-11-22-shell-written-files-design.md`

## Global Constraints

- Work on a new branch `feat/shell-written-files` cut from `develop`.
- Candidates: absolute, cleaned, de-duplicated, at most **16** per command.
- Side store: at most **1024** entries, **10 minutes**, oldest dropped; a call that wrote nothing stores nothing.
- Tracking is **off by default**; only the server turns it on. Off ⇒ the `Bash` tool neither parses nor stats.
- The text of a `Bash` result never changes unless a diagnostics delta is appended.
- A panic or error in the parser or the tracking is recovered and logged with `log.Printf`; the command runs normally.
- Not covered (do not add): `revert` snapshots, per-file lock, `patch`/`git apply` targets, `bash_background`, `monitor`, the `!` escape, injected turns, any web UI change.
- `go test ./core/permissions ./core/tools ./server ./agent` and `make vet` pass after every task.

## Review Focus

1. A heredoc body or quoted string that contains `>`, `tee` or `sed -i` as text (an awk program, a Markdown report about shell) yields no candidate from that text. → Task 1 and Task 2 tests.
2. A quoted path with a space (`> "my report.md"`) is one candidate with the space, not two. → Task 1 test.
3. A file created then removed by the same command (`cat > f && rm f`) is not reported. → Task 3 test.
4. A very large command (a 1 MB heredoc body) parses in well under a second. → Task 2 test.
5. A candidate that is a directory or does not exist after the command is not reported; a symlink to a regular file is reported under the path as written. → Task 3 test.

---

### Task 1: Shell forms — `permissions.ShellWriteTargets`

**Files:**
- Create: `core/permissions/shell_writes.go`
- Test: `core/permissions/shell_writes_test.go`

**Interfaces:**
- Consumes: `unquotedMask`, `splitCompound`, `stripWrappers` (`core/permissions/match_bash.go`).
- Produces: `func ShellWriteTargets(command, cwd string) []string` — absolute cleaned paths, order of appearance, no duplicates, at most `maxShellWriteTargets = 16`. Text only; never touches the disk except `os.UserHomeDir` for `~/` and `os.Stat` on a `cp`/`mv` destination to see whether it is a directory.

- [ ] **Step 1: Write the failing test** `TestShellWriteTargetsForms`, table-driven, `cwd = "/work"`:

```go
{"cat > out.md <<'EOF'\nhello\nEOF", []string{"/work/out.md"}},
{"echo x >> /srv/a.txt", []string{"/srv/a.txt"}},
{"cmd &> all.log", []string{"/work/all.log"}},
{"cmd 2>&1 | tee -a build.log", []string{"/work/build.log"}},
{"cmd > /dev/null 2>&1", nil},
{"echo x >&2", nil},
{"sed -i 's/a/b/' src/a.go src/b.go", []string{"/work/src/a.go", "/work/src/b.go"}},
{"sed -i -e 's/a/b/' -e 's/c/d/' x.py", []string{"/work/x.py"}},
{"sed -n '1,5p' x.py", nil},
{"perl -pi -e 's/a/b/' x.pl", []string{"/work/x.pl"}},
{"cp /tmp/draft.md report.md", []string{"/work/report.md"}},
{"mv a.txt b.txt", []string{"/work/b.txt"}},
{"pandoc notes.md -o /tmp/report.docx", []string{"/tmp/report.docx"}},
{"curl -sL https://x/y --output=data.json", []string{"/work/data.json"}},
{"wget -q -O page.html https://x", []string{"/work/page.html"}},
{"zip -r out.zip src", []string{"/work/out.zip"}},
{"tar -czf /tmp/a.tgz src", []string{"/tmp/a.tgz"}},
{"tar -xzf a.tgz", nil},
{"cd /app && echo x > a.txt", []string{"/app/a.txt"}},
{"cd sub; echo x > a.txt", []string{"/work/sub/a.txt"}},
{"cd $DIR && echo x > a.txt", nil},
{"echo x > $OUT", nil},
{"echo x > out-*.txt", nil},
{"echo x > \"my report.md\"", []string{"/work/my report.md"}},
{"awk '$1 > 3 {print}' data.csv", nil},
{"grep 'a > b' f.txt", nil},
{"sudo tee /etc/x.conf", []string{"/etc/x.conf"}},
{"timeout 30 sh -c 'true' > t.txt", []string{"/work/t.txt"}},
{"echo a > f.txt; echo b >> f.txt", []string{"/work/f.txt"}},
```

Add `TestShellWriteTargetsCap` (20 redirects ⇒ 16 results), `TestShellWriteTargetsHome` (`> ~/n.txt` ⇒ `<home>/n.txt`), `TestShellWriteTargetsCpIntoDir` (destination is a `t.TempDir()` ⇒ `<dir>/draft.md`), and `TestShellWriteTargetsEmptyCwd` (`cwd == ""`: relative candidates dropped, absolute kept).

- [ ] **Step 2: Run** `go test ./core/permissions -run ShellWriteTargets` — FAIL (undefined).

- [ ] **Step 3: Implement** `ShellWriteTargets` in `core/permissions/shell_writes.go`:
  - Split with `splitCompound`; keep a running directory starting at `cwd`, updated by a segment whose first word is `cd` (resolvable argument ⇒ new directory; otherwise mark the directory unknown so later relative candidates are dropped). A `cd` inside a pipeline or subshell is ignored.
  - Per segment: scan redirects with `unquotedMask` (target word after `>`, `>>`, `>|`, `&>`, `&>>`; skip `>&N`, `>&-`); then `stripWrappers` and dispatch on the base name of the first word for `tee`, `sed`, `perl`, `cp`, `mv`, `install`, `pandoc`, `curl`, `sort`, `wget`, `zip`, `tar`, per the spec table.
  - Word rules: strip one layer of quotes; drop a word containing `$`, a backtick, `*`, `?`, `[`, `{`; expand a leading `~/`; drop `/dev/…` and `/proc/…`; `filepath.Clean`.
  - A segment the function cannot tokenise yields nothing.

- [ ] **Step 4: Run** `go test ./core/permissions` — PASS (including the existing tests).

- [ ] **Step 5: Commit** `feat(permissions): list the files a shell command writes`.

---

### Task 2: Heredocs and inline Python

**Files:**
- Modify: `core/permissions/shell_writes.go`
- Test: `core/permissions/shell_writes_test.go`

**Interfaces:**
- Consumes/Produces: same `ShellWriteTargets` signature; this task only widens what it recognises.

- [ ] **Step 1: Write the failing tests**

`TestShellWriteTargetsHeredocBodyIsNotShell`:

```go
cmd := "cat > notes.md <<'EOF'\nuse `echo x > secret.txt` or sed -i file\nEOF"
// want exactly []string{"/work/notes.md"}
```

`TestShellWriteTargetsPython`, `cwd = "/work"`:

```go
{"python3 - <<'PY'\nopen('out.json','w').write('{}')\nPY", []string{"/work/out.json"}},
{"python3 -c \"open('a.txt','a').write('x')\"", []string{"/work/a.txt"}},
{"python3 - <<'PY'\np='test/core/x.spec.ts'\ns=open(p).read()\nopen(p,'w').write(s)\nPY", []string{"/work/test/core/x.spec.ts"}},
{"python - <<'PY'\nfrom pathlib import Path\nPath('r.md').write_text('x')\nPY", []string{"/work/r.md"}},
{"python3 - <<'PY'\nprint(open('in.txt').read())\nPY", nil},
{"cd /app && python3 - <<'PY'\nopen('a.py','w').write('')\nPY", []string{"/app/a.py"}},
{"node -e \"require('fs')\" <<'JS'\nopen('x','w')\nJS", nil},
```

`TestShellWriteTargetsLargeCommand`: a command with a 1 MB heredoc body returns in under 500 ms.

- [ ] **Step 2: Run** `go test ./core/permissions -run ShellWriteTargets` — the new tests FAIL.

- [ ] **Step 3: Implement** in `shell_writes.go`:
  - `splitHeredocs(command string) (shell string, bodies []string)`: remove the body lines of `<<TAG`, `<<'TAG'`, `<<"TAG"`, `<<-TAG` (not `<<<`) up to the line equal to `TAG`; only an unquoted `<<` (per `unquotedMask`) starts one. `ShellWriteTargets` parses `shell` only.
  - When a segment's command is `python`, `python3` or `python3.N`: collect its `-c` argument and the heredoc body attached to that segment, and scan that code with regular expressions for `open('P', 'w…'|'a…'|'x…')`, `Path('P').write_text(` / `.write_bytes(`, and the same with a variable assigned a string literal earlier in the code. Resolve against the segment's directory.

- [ ] **Step 4: Run** `go test ./core/permissions` — PASS.

- [ ] **Step 5: Commit** `feat(permissions): heredoc bodies and inline Python in shell write targets`.

---

### Task 3: Disk check and side store in the `Bash` tool

**Files:**
- Create: `core/tools/shell_writes.go`
- Modify: `core/tools/tools.go:19-28` (the `Bash` handler), `core/tools/readonly.go:48-63` (the read-only `Bash` handler)
- Test: `core/tools/shell_writes_test.go`

**Interfaces:**
- Consumes: `permissions.ShellWriteTargets(command, cwd string) []string`.
- Produces:

```go
type ShellWrite struct {
	Path    string // absolute, as the parser returned it
	Created bool   // the file did not exist before the command
}
func EnableShellWriteTracking(on bool)
func ShellWrites(callID string) []ShellWrite     // read, entry kept
func TakeShellWrites(callID string) []ShellWrite // read and remove
func RecordShellWrites(callID string, ws []ShellWrite) // store (no-op when off, callID empty or ws empty)
// unexported, called by both Bash handlers around runBashStatus:
func trackShellWrites(callID, command, cwd string) (finish func())
```

- [ ] **Step 1: Write the failing tests** in `core/tools/shell_writes_test.go` (each enables tracking and restores it with `t.Cleanup`), calling `trackShellWrites` around a real `runBashStatus` in a `t.TempDir()`:

  - `TestShellWritesCreatedAndModified`: `echo a > new.txt` ⇒ `[{<dir>/new.txt, true}]`; then, on an existing file, `echo b >> old.txt` ⇒ `[{<dir>/old.txt, false}]`.
  - `TestShellWritesNothingWritten`: `ls > /dev/null`, and `false && echo x > never.txt` ⇒ `ShellWrites(id) == nil`.
  - `TestShellWritesCreatedThenRemoved`: `echo a > f.txt && rm f.txt` ⇒ nil.
  - `TestShellWritesDirectoryAndSymlink`: a candidate that is a directory ⇒ not reported; a symlink to a regular file written through ⇒ reported under the symlink's path.
  - `TestShellWritesTakeRemoves`: `ShellWrites` twice returns the entry, `TakeShellWrites` returns it once, then nil.
  - `TestShellWritesOff`: tracking off ⇒ nil, and `trackShellWrites` returns a no-op.
  - `TestShellWritesEmptyCallID`: `callID == ""` ⇒ nothing stored.
  - `TestShellWritesStoreBounded`: 1100 recorded calls ⇒ at most 1024 entries, the oldest gone.

- [ ] **Step 2: Run** `go test ./core/tools -run ShellWrites` — FAIL (undefined).

- [ ] **Step 3: Implement** `core/tools/shell_writes.go`:
  - An `atomic.Bool` switch; a mutex-guarded map `callID → {writes, at}` with the two bounds from Global Constraints (drop expired entries on each record).
  - `trackShellWrites`: when off or `callID == ""`, return a no-op. Otherwise call `permissions.ShellWriteTargets`, `os.Stat` each candidate (exists, `ModTime`, `Size`), and return a `finish` that stats again and records the candidates that are now regular files (`os.Stat`, so a symlink follows) and either did not exist before or changed `ModTime` or `Size`. Both halves run under `defer recover()` with a `log.Printf`.

- [ ] **Step 4: Wire both handlers.** In `core/tools/tools.go` and `core/tools/readonly.go`, wrap the `runBashStatus` call:

```go
finish := trackShellWrites(ctx.FunctionCallID(), in.Command, in.Cwd)
out, st, _ := runBashStatus(context.Background(), in)
finish()
```

- [ ] **Step 5: Run** `go test ./core/tools` — PASS.

- [ ] **Step 6: Commit** `feat(tools): record the files a Bash call wrote`.

---

### Task 4: Server — `file_changed`, language servers, produced files

**Files:**
- Modify: `server/sse.go` (beside `emitFileChanged`, ~line 892, and its two call sites ~999 and ~1169), `server/produced_files.go`, `server/main.go` (~line 282, next to `fstools.SetCwdResolver`)
- Test: `server/shell_writes_test.go`

**Interfaces:**
- Consumes: `fstools.TakeShellWrites(callID string) []fstools.ShellWrite`, `fstools.EnableShellWriteTracking(on bool)`.
- Produces: `func shellProducedWorthy(path string) bool` (in `server/produced_files.go`).

- [ ] **Step 1: Write the failing tests** in `server/shell_writes_test.go`:

  - `TestShellProducedWorthy`:

```go
yes := []string{"/tmp/report.docx", "/home/u/notes.md", "/work/out/data.json"}
no := []string{"/work/build.log", "/work/Makefile", "/work/a.tmp", "/work/.cache/x.md",
	"/work/node_modules/p/i.js", "/work/__pycache__/m.pyc", "/work/dist/app.js", "/work/x.pid"}
```

  - `TestStreamEmitsFileChangedForBashWrites`: drive `streamEvents` the way `server/ask_user_stream_scope_test.go` does, with one event carrying a `Bash` function call (id `c1`) and one carrying its function response; before running, enable tracking and record `[{<tmp>/report.md, Created: true}, {<tmp>/old.go, Created: false}]` for `c1` with `fstools.RecordShellWrites("c1", …)` (the two files exist on disk in a `t.TempDir()`). Assert the sink received `file_changed` twice with `tool` `"write"` then `"edit"`, that the session's produced files contain `report.md` only, and that `TakeShellWrites("c1")` is now nil.
  - `TestStreamBashWithoutWritesEmitsNothing`: same stream, nothing recorded ⇒ no `file_changed` frame.
  - `TestStreamSubAgentBashWrites`: the same through an `agent_tool_call` / `agent_tool_result` bus pair.

- [ ] **Step 2: Run** `go test ./server -run 'ShellProduced|StreamEmits|StreamBash|StreamSubAgent'` — FAIL.

- [ ] **Step 3: Implement.**
  - `shellProducedWorthy` per the spec's noise filter (no extension; `.log .out .err .tmp .pid .lock .bak .swp .pyc .o`; a hidden path component; a component among `node_modules`, `__pycache__`, `target`, `dist`, `build`).
  - In `streamEvents`, a closure `emitShellWrites(toolName, callID string)`: when `toolName == "Bash"`, for each `fstools.TakeShellWrites(callID)` entry: `emit("file_changed", {"path": p, "tool": "write"|"edit"})`, `toolkitagent.FireFileChange(p)`, and `recordProducedFile(ownSession, p)` when `Created && shellProducedWorthy(p)`. Call it at both places `emitFileChanged` is called (root tool result, sub-agent tool result). The tool name of a result is the one noted at the call; keep it in a `callID → name` map filled where `noteFileTool` is called.
  - In `server/main.go`: `fstools.EnableShellWriteTracking(true)` with a deferred `false`.

- [ ] **Step 4: Run** `go test ./server` — PASS.

- [ ] **Step 5: Commit** `feat(server): file_changed and produced files for shell-written files`.

---

### Task 5: Fused diagnostics after `Bash`, and docs

**Files:**
- Modify: `agent/coding_efficiency_plugin.go` (`afterTool` ~line 180, `fuse` ~line 257), `CLAUDE.md`, `internal/features/FEATURES.md`, `web/docs` (the page describing the Results card)
- Test: `agent/coding_efficiency_plugin_test.go`

**Interfaces:**
- Consumes: `fstools.ShellWrites(callID string) []fstools.ShellWrite`.
- Produces: `func (ce *codingEfficiency) fusePaths(tc adk.ToolContext, sid, cwd string, paths []string, result map[string]any) map[string]any` (the body of today's `fuse` after `editedPaths`; `fuse` calls it). A test seam: field `diags func(ctx context.Context, path string) ([]lsp.Diagnostic, bool)` on `codingEfficiency`, defaulting to `lspMgr.DiagnosticsIfRunning` with `fuseMaxWait`/`fuseQuiet`.

- [ ] **Step 1: Write the failing tests** in `agent/coding_efficiency_plugin_test.go`:

  - `TestFusePathsAppendsDelta`: `ce.diags` returns one diagnostic for `/w/a.go` ⇒ the returned map's dominant string ends with the delta line and starts with the original text.
  - `TestFusePathsNoServer`: `ce.diags` returns `ok == false` ⇒ nil.
  - `TestAfterToolBashWithWrites`: a recorded shell write for the call id and `ce.diags` reporting a diagnostic ⇒ `afterTool` on a `Bash` result returns the fused map and does not shape it; with `ce.diags` returning `ok == false`, the same call goes through the shaper exactly as a `Bash` result without writes (compare both outputs on a 40,000-character result).
  - `TestAfterToolBashNoWrites`: nothing recorded ⇒ identical to today's behaviour (nil for a short result).

- [ ] **Step 2: Run** `go test ./agent -run 'FusePaths|AfterToolBash'` — FAIL.

- [ ] **Step 3: Implement.** Extract `fusePaths` from `fuse`; route diagnostics through `ce.diags`. In `afterTool`, before the dedup/shaper steps:

```go
if name == "Bash" {
	if ws := fstools.ShellWrites(tc.FunctionCallID()); len(ws) > 0 {
		if m := ce.fusePaths(tc, sid, cwd, shellWritePaths(ws), result); m != nil {
			return m, nil
		}
	}
}
```

`looksLikeError` is not applied to a `Bash` result (a failing test run after a `sed -i` is exactly when the delta helps).

- [ ] **Step 4: Run** `go test ./agent` — PASS.

- [ ] **Step 5: Update the docs.**
  - `CLAUDE.md`: a short subsection "Files written through the shell" under "File tool robustness" (the parser, the store, tracking on only in the server, the noise filter, what is not covered), one line in the coding-efficiency plugin section (fusion also after `Bash`), and one in "Web UI file editor" (`file_changed` also for shell writes).
  - `internal/features/FEATURES.md`, under the in-development section: `- **Shell-written files** — files an agent writes with a shell command appear in Results and refresh the open editor.`
  - `web/docs`: one sentence where the Results card is described.

- [ ] **Step 6: Run** `make test` and `make vet` — PASS.

- [ ] **Step 7: Commit** `feat(agent): fuse diagnostics after shell edits; docs`.

---

### Task 6: Live check

**Files:** none.

- [ ] **Step 1:** Build and start the server from the branch with the repo's web assets and config (`OMNIS_WEB_DIR=$(pwd)/web`, an `OMNIS_SYSTEM_CONFIG_DIR` temp layer, `env -u OMNIS_CONFIG_PATH`).
- [ ] **Step 2:** In a chat on the Coding squad, ask: "With a single shell command, write a file report.md containing three lines, then append a line to it with another shell command." Expected: `report.md` appears in the Results card with a download card after the first command; no second Results entry after the append.
- [ ] **Step 3:** Open `report.md` in the editor, ask for one more appended line. Expected: the editor tab refreshes without a reload.
- [ ] **Step 4:** Ask for `go test ./... > /tmp/t.log 2>&1`. Expected: no Results entry.
- [ ] **Step 5:** Report what was observed; do not merge.

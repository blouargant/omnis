# Files written through the shell — design

Date: 2026-11-22. Status: proposed.

## Intent

When an agent writes a file with the `Bash` tool (`cat > f <<EOF`, `sed -i`, an
inline Python script, `pandoc -o`), omnis does not know a file changed. Only
`Write`, `Edit`, `MultiEdit` and `revert` are tracked. The web UI therefore misses
those files: the Results card and the download card stay empty, and a Monaco tab
open on the file keeps stale content.

This is common. In the 49 Harbor traces of 2026-11-07 about one edit in three went
through the shell (258 shell edits, 539 tool edits), and the `coder` on `premium`
used `Bash` only (18 trials, no `Edit` or `Write` call). The idea comes from
TokenEconomizer's `loopguard/shell_edits.py` (Microsoft Research, arXiv
2610.22732), which reads written files off the command text.

## Goals

- After a `Bash` call, know which files it wrote, and which of them are new.
- For each: send `file_changed` (Monaco refresh, Files panel, download card for a
  new file), tell running language servers (`FireFileChange`), and record a new
  file in the session's produced files (Results card).
- Append the diagnostics delta to the `Bash` result for written files, under the
  same conditions as after `Edit` (a language server already running).

## Non-goals

- No snapshot for `revert`, no per-file lock for shell writes.
- No detection of indirect writes (`python build.py`, `make`, `ruff format`,
  `go generate`). A later version may compare `git status` before and after in a
  git work tree.
- `bash_background`, `monitor`, the `!` shell escape and the terminal dock are
  not covered.
- Injected turns (mailbox, scheduled, spawned, background) emit no SSE tool
  frames today and stay as they are.
  *Amended after the first version (user, 2026-11-22):* a file the root agent of
  an injected turn writes with the shell is recorded in Results (still no
  `file_changed`, as those turns have no stream).
- No change to what the model can do, and no new permission rule.

## Anti-goals

- A file reported as written when it was not. Every candidate is checked on disk.
- Noise in the Results card (logs, temp files).
- Any change to the text of a `Bash` result when nothing was written, or to
  CLI/TUI behaviour.

## Approaches considered

- **A. Parse the command only.** Free, but reports files a failed command never
  wrote, and cannot tell a new file from a modified one.
- **B. Compare the disk before and after** (`git status`, mtimes). Catches every
  write, but only inside a git work tree. Most sessions run in the user's HOME and
  reports often land in `/tmp`. Two `git status` per `Bash` call.
- **C. Parse the command, then check the disk.** Chosen (user, 2026-11-22).
  Candidates come from the command text; each is kept only if the disk shows it
  was written. Works outside git, costs one `stat` per candidate before and after.
  Indirect writes stay invisible, as in A.

## Design

### 1. Candidates: `permissions.ShellWriteTargets(command, cwd string) []string`

New file `core/permissions/shell_writes.go`, beside `match_bash.go`, so it reuses
`unquotedMask` and `splitCompound` (`core/tools` already imports
`core/permissions`). Returns absolute, cleaned, de-duplicated paths, at most 16.

Heredoc bodies are split off first (`<<TAG`, `<<'TAG'`, `<<-TAG`; `<<<` is a
here-string and has no body) so their text is never read as shell syntax. The
command is then split into segments, and wrappers are skipped as `ToolKey` does
(`sudo`, `env`, `timeout`, `nice`, `nohup`, `time`).

Forms recognised, per segment:

| Form | Candidates |
|---|---|
| Output redirect `>`, `>>`, `>\|`, `&>`, `&>>` (not `>&N`, `>&-`) | the target word |
| `tee [-a] FILE…` | each non-option operand |
| `sed -i…` / `sed --in-place…`, `perl -…i…` | the file operands (after the script, or after `-e`/`-f` values) |
| `cp`, `mv`, `install` | the last operand; when it is an existing directory, `dir/basename(source)` for each source |
| `-o FILE`, `--output FILE`, `--output=FILE` of `pandoc`, `curl`, `sort`; `wget -O FILE` | that file |
| `zip ARCHIVE …`, `tar` with `c` and `f` in its mode | the archive |
| Python (`python`, `python3`, `python3.x`) with `-c CODE` or a heredoc body | `open('P', 'w…'\|'a…'\|'x…')`, `Path('P').write_text(`/`write_bytes(`, and the same through one variable assigned a string literal earlier in the code (`p = 'f.ts'` … `open(p, 'w')`) |
| `patch`, `git apply` | none (targets are in the patch; left to a later version) |

Path rules:

- A leading `~/` expands to the home directory. A word containing `$`, a
  backtick, `*`, `?`, `[` or `{` is dropped (not resolvable from text).
- A relative path resolves against the directory in effect: `cwd`, updated by
  each `cd DIR` segment that precedes it in a `&&` / `;` chain (the traces show
  `cd /app && …` on most commands). A `cd` whose argument is not resolvable
  drops the relative candidates that follow it.
- `/dev/…` and `/proc/…` are dropped.

The function only reads text. It never touches the disk.

### 2. Check on disk, inside the `Bash` tool

In `core/tools` (the `Bash` tool and the read-only `Bash`, in `runBashStatus`'s
callers that hold the tool context):

1. Before running: `ShellWriteTargets(command, cwd)`, then `os.Stat` each
   candidate and keep `{exists, modTime, size}`.
2. After the command returns (whatever its exit status): `os.Stat` again. A
   candidate is **written** when it is now a regular file and either did not
   exist before (`Created: true`) or its `modTime` or `size` differs.
3. Store the result under the call id: `fstools.recordShellWrites(callID,
   []ShellWrite{Path, Created})`.

This runs in the tool, not in the SSE consumer, because the consumer reads tool
calls from a buffered channel and could stat a file after a fast command already
wrote it.

Side store, in `core/tools/shell_writes.go`, modelled on `bash_exit.go`:

- `EnableShellWriteTracking()`: off by default. The server turns it on at
  startup. While off, the `Bash` tool does none of the above (CLI, TUI, tests).
- `ShellWrites(callID) []ShellWrite` (non-destructive read) and
  `TakeShellWrites(callID) []ShellWrite` (read and remove).
- Bounded: 1024 entries, 10 minutes, oldest dropped. A call that wrote nothing
  stores nothing.

The model-visible `Bash` result is unchanged by this step.

### 3. Server: `file_changed`, language servers, produced files

In `streamEvents` (`server/sse.go`), next to `emitFileChanged`, for both the root
path (`tool_result`) and the sub-agent path (`agent_tool_result`): when the tool
is `Bash`, `TakeShellWrites(callID)` and, for each entry:

- `emit("file_changed", {path, tool})` with `tool: "write"` when `Created`, else
  `"edit"`. The web UI needs no change: `write` already shows the download card
  and both refresh Monaco and the Files panel.
- `toolkitagent.FireFileChange(path)`.
- When `Created` and the file is not noise (below): `recordProducedFile`.

Noise filter for produced files (`shellProducedWorthy(path)`): skip a file with
no extension or with `.log`, `.out`, `.err`, `.tmp`, `.pid`, `.lock`, `.bak`,
`.swp`, `.pyc`, `.o`; skip a path with a hidden component, or under
`node_modules`, `__pycache__`, `target`, `dist`, `build`. Files under `/tmp` are
kept when they pass (reports are often written there).

### 4. Fused diagnostics

In the coding-efficiency plugin (`agent/coding_efficiency_plugin.go`): after a
`Bash` call, read `fstools.ShellWrites(tc.FunctionCallID())` (the non-destructive
read: the server takes the entry later). When non-empty, run the existing `fuse`
logic on those paths (`DiagnosticsIfRunning`, at most `fuseMaxFiles`, skipped when
no server runs). A fused `Bash` result is then exempt from the shaper for that
call, as edit results are. Nothing is appended when there is no diagnostic to
report, so the result is byte-identical in that case.

Sub-agents do not get this (the fusion is root-only today); they still get step 3.

## No-op contract

- Tracking off (CLI, TUI, tests): the `Bash` tool does not parse or stat.
- Tracking on, a command with no candidate: one parse, no `stat`, no store entry,
  no event.
- A parse error or a panic in the parser is recovered and logged; the command
  runs as if tracking were off.

## Tests

- `core/permissions/shell_writes_test.go`: one case per form in the table;
  quoted `>` (awk program, grep pattern) is not a redirect; heredoc body text is
  not parsed as shell; `cd` chains; dropped words (`$`, globs); the 16 cap. Use
  real commands from the Harbor traces as cases.
- `core/tools`: a `Bash` call that creates a file, modifies one, fails before
  writing, and writes nothing — checked through `ShellWrites`; tracking off
  stores nothing.
- `server`: a stream with a `Bash` tool result emits `file_changed` and records
  the produced file; a `.log` file is not recorded; a sub-agent `Bash` call too.
- `agent`: fusion after `Bash` appends the delta when a (fake) server reports
  diagnostics, and leaves the result untouched otherwise.

## Docs to update when shipped

`CLAUDE.md` ("Web UI file editor", "File tool robustness", the coding-efficiency
plugin section), `web/docs` where the Results card is described, and one bullet
in `internal/features/FEATURES.md` ("Files written by shell commands appear in
Results and refresh the editor").

## Left to the builder

- The exact option tables for `sed`/`perl` operand parsing.
- Whether `ShellWrite` carries the `stat` time, if a consumer needs it.
- Where the `Bash` handler gets the call id when it is absent (then: skip).

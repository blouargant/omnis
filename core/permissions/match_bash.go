package permissions

import (
	"regexp"
	"strings"
)

// compileBashGlob turns a Claude-style Bash glob specifier into an anchored
// regexp. Semantics (per the Claude Code permission docs):
//
//   - "*" matches any sequence of characters, including spaces, so one
//     wildcard can span multiple arguments.
//   - A trailing " *" (space then star) enforces a word boundary: the prefix
//     must be followed by whitespace or end-of-string. So "ls *" matches
//     "ls -la" and bare "ls" but not "lsof".
//   - A trailing ":*" is equivalent to a trailing " *".
//   - "ls*" (no space) has no boundary and matches both "ls -la" and "lsof".
func compileBashGlob(arg string) (*regexp.Regexp, error) {
	boundary := false
	switch {
	case strings.HasSuffix(arg, ":*"):
		arg = arg[:len(arg)-2]
		boundary = true
	case strings.HasSuffix(arg, " *"):
		arg = arg[:len(arg)-2]
		boundary = true
	}

	var b strings.Builder
	b.WriteString("(?s)^")
	for i, seg := range strings.Split(arg, "*") {
		if i > 0 {
			b.WriteString(".*")
		}
		b.WriteString(regexp.QuoteMeta(seg))
	}
	if boundary {
		b.WriteString(`(?:\s.*)?`)
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}

// bashGlobMatch reports whether a Bash spec matches a single (already
// wrapper-stripped) subcommand string.
func (s *Spec) bashGlobMatch(sub string) bool {
	sub = strings.TrimSpace(sub)
	if s.Bare {
		return true
	}
	if s.glob == nil {
		return false
	}
	return s.glob.MatchString(sub)
}

// compoundSeps are the shell operators Claude Code recognises as command
// separators. A permission rule must match each subcommand independently.
var compoundOps = []string{"&&", "||", "|&", ";", "\n", "|", "&"}

// splitCompound breaks a shell command line into its subcommands, respecting
// single and double quotes so an operator inside a quoted string is not
// treated as a separator. Order is preserved; empty fragments are dropped.
func splitCompound(cmd string) []string {
	var out []string
	var cur strings.Builder
	var quote byte // 0, '\'' or '"'
	flush := func() {
		s := strings.TrimSpace(cur.String())
		if s != "" {
			out = append(out, s)
		}
		cur.Reset()
	}
	for i := 0; i < len(cmd); i++ {
		c := cmd[i]
		if quote != 0 {
			cur.WriteByte(c)
			if c == quote {
				quote = 0
			}
			continue
		}
		if c == '\'' || c == '"' {
			quote = c
			cur.WriteByte(c)
			continue
		}
		matched := ""
		for _, op := range compoundOps {
			if strings.HasPrefix(cmd[i:], op) {
				matched = op
				break
			}
		}
		// A lone '&' that belongs to a file-descriptor redirection
		// (2>&1, >&2, >&-, &>file, &>>file) is NOT a command separator —
		// splitting it would invent a bogus "1"/"2" subcommand that no
		// allow rule covers, forcing an otherwise-allowed command to ask.
		if matched == "&" && isRedirectAmp(cmd, i) {
			matched = ""
		}
		if matched != "" {
			flush()
			i += len(matched) - 1
			continue
		}
		cur.WriteByte(c)
	}
	flush()
	if len(out) == 0 {
		return []string{strings.TrimSpace(cmd)}
	}
	return out
}

// isRedirectAmp reports whether the single '&' at cmd[i] is part of a
// file-descriptor redirection rather than a background/compound operator. It
// is a redirect when it directly follows a '>' (2>&1, >&2, >&-) or directly
// precedes one (&>file, &>>file); intervening spaces/tabs are skipped.
func isRedirectAmp(cmd string, i int) bool {
	j := i - 1
	for j >= 0 && (cmd[j] == ' ' || cmd[j] == '\t') {
		j--
	}
	if j >= 0 && cmd[j] == '>' {
		return true
	}
	k := i + 1
	for k < len(cmd) && (cmd[k] == ' ' || cmd[k] == '\t') {
		k++
	}
	return k < len(cmd) && cmd[k] == '>'
}

// commandWrappers are process wrappers Claude Code strips before matching, so
// a rule like Bash(npm test *) also matches "timeout 30 npm test".
var commandWrappers = map[string]bool{
	"timeout": true, "time": true, "nice": true, "nohup": true, "stdbuf": true,
}

// durationOrNumber loosely matches a wrapper argument like "30", "30s", "1m".
var durationOrNumber = regexp.MustCompile(`^\d+[a-zA-Z]*$`)

// awkWritesFile heuristically detects an awk program that writes a file or runs
// a command: an append redirect (>>), a redirect to a quoted filename (> "f"),
// a system() call, or a pipe to a command (| "cmd"). Numeric / line-range
// comparisons (NR>=10, $1 > 5) use >= or > followed by a number, so they don't
// match — keeping common read-only awk filters auto-allowed.
var awkWritesFile = regexp.MustCompile(`>>|>[ \t]*"|system[ \t]*\(|\|[ \t]*"`)

// stripWrappers removes recognised process wrappers (and their immediate
// option/duration arguments) from the front of a subcommand, returning the
// inner command. Bare "xargs" (no flags) is also stripped; "xargs -n1 …" is
// left intact (matched as an xargs command, per the docs).
func stripWrappers(sub string) string {
	for {
		fields := strings.Fields(sub)
		if len(fields) == 0 {
			return sub
		}
		head := fields[0]
		if head == "xargs" {
			if len(fields) > 1 && strings.HasPrefix(fields[1], "-") {
				return sub // xargs with flags — not stripped
			}
			sub = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(sub), "xargs"))
			continue
		}
		if !commandWrappers[head] {
			return sub
		}
		// Drop the wrapper token plus following options / duration args.
		rest := fields[1:]
		idx := 0
		for idx < len(rest) {
			tok := rest[idx]
			if strings.HasPrefix(tok, "-") || durationOrNumber.MatchString(tok) {
				idx++
				continue
			}
			break
		}
		sub = strings.Join(rest[idx:], " ")
	}
}

// bashSubcommands splits a command line and strips wrappers from each
// subcommand, yielding the inner commands to match rules against.
func bashSubcommands(command string) []string {
	parts := splitCompound(command)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, stripWrappers(p))
	}
	return out
}

// readOnlyCommands are the base commands Claude Code runs without a prompt in
// every mode. Mutating-capable tools (sed, sort, find with -delete or -exec,
// git non-read subcommands) are handled with extra care below.
var readOnlyCommands = map[string]bool{
	"ls": true, "cat": true, "echo": true, "pwd": true, "head": true,
	"tail": true, "grep": true, "wc": true, "which": true, "diff": true,
	"stat": true, "du": true, "cd": true, "file": true, "true": true,
	// Read-only text filters (stdout-only; no file-writing mode). sed/sort/awk
	// have mutating modes and are guarded in isReadOnlyCommand below instead.
	"cut": true, "tr": true, "jq": true, "tac": true, "rev": true,
	"nl": true, "column": true, "fold": true, "fmt": true, "paste": true,
	"comm": true, "expand": true, "unexpand": true,
	// Harmless no-op / inspection utilities (no side effects). `env` is NOT
	// here — `env FOO=bar cmd` executes cmd — and `hostname NAME` / `date -s`
	// can mutate, so date is guarded below and hostname is omitted.
	"sleep": true, "seq": true, "false": true, ":": true,
	"test": true, "[": true, "basename": true, "dirname": true,
	"realpath": true, "readlink": true, "printenv": true,
	"whoami": true, "id": true, "uname": true,
}

// findWriteActions are the find actions that write a file or run a command
// interactively; any of them makes the find mutating.
var findWriteActions = map[string]bool{
	"-delete": true, "-ok": true, "-okdir": true,
	"-fprint": true, "-fprint0": true, "-fprintf": true, "-fls": true,
}

// findIsReadOnly reports whether find's arguments are read-only. An
// -exec/-execdir action is accepted when the command it runs is itself
// read-only (`find … -exec grep -l pat {} \;`). The command runs up to its
// terminator: `;`, `\;`, `';'`, `+`, or a trailing `\` — splitCompound cuts an
// unquoted `\;` at the `;`, leaving the backslash as the segment's last token.
// A command that is the found file itself (`-exec {} \;`) is refused.
func findIsReadOnly(args []string) bool {
	for i := 0; i < len(args); i++ {
		f := args[i]
		if findWriteActions[f] {
			return false
		}
		if f != "-exec" && f != "-execdir" {
			continue
		}
		j := i + 1
		for j < len(args) && !isFindExecTerminator(args[j]) {
			j++
		}
		inner := args[i+1 : j]
		if len(inner) == 0 || strings.Contains(inner[0], "{}") {
			return false
		}
		if !isReadOnlyCommand(strings.Join(inner, " ")) {
			return false
		}
		i = j
	}
	return true
}

func isFindExecTerminator(tok string) bool {
	switch tok {
	case ";", `\;`, `';'`, `";"`, "+", `\`:
		return true
	}
	return false
}

// readOnlyGitSubcommands are the git subcommands that do not mutate the working
// tree or remote.
var readOnlyGitSubcommands = map[string]bool{
	"status": true, "log": true, "diff": true, "show": true, "branch": true,
	"remote": true, "ls-files": true, "describe": true, "blame": true,
	"rev-parse": true, "cat-file": true, "shortlog": true, "tag": true,
}

// isReadOnlyCommand reports whether a single (wrapper-stripped) subcommand is a
// recognised read-only command. Conservative: a "find" that writes a file,
// deletes, or -exec's a non-read-only command, or a non-read git subcommand, is
// not treated as read-only.
func isReadOnlyCommand(sub string) bool {
	fields := strings.Fields(sub)
	if len(fields) == 0 {
		return false
	}
	cmd := fields[0]
	if readOnlyCommands[cmd] {
		return true
	}
	switch cmd {
	case "find":
		return findIsReadOnly(fields[1:])
	case "date":
		// date prints the time and is read-only unless it sets the system
		// clock (-s / --set). -s is the only single-letter date flag with 's'.
		for _, f := range fields[1:] {
			if strings.HasPrefix(f, "--set") {
				return false
			}
			if strings.HasPrefix(f, "-") && !strings.HasPrefix(f, "--") && strings.ContainsRune(f, 's') {
				return false
			}
		}
		return true
	case "sed", "gsed":
		// sed is read-only unless it edits in place (-i / --in-place). -i is
		// the only single-letter sed flag containing 'i', so any short-flag
		// bundle with an 'i' (e.g. -ni) also enables in-place editing.
		for _, f := range fields[1:] {
			if strings.HasPrefix(f, "--in-place") {
				return false
			}
			if strings.HasPrefix(f, "-") && !strings.HasPrefix(f, "--") && strings.ContainsRune(f, 'i') {
				return false
			}
		}
		return true
	case "sort":
		// sort is read-only unless it writes an output file (-o / --output).
		// -o is the only single-letter sort flag containing 'o'.
		for _, f := range fields[1:] {
			if strings.HasPrefix(f, "--output") {
				return false
			}
			if strings.HasPrefix(f, "-") && !strings.HasPrefix(f, "--") && strings.ContainsRune(f, 'o') {
				return false
			}
		}
		return true
	case "awk", "gawk", "mawk":
		// awk is read-only unless its program writes a file or runs a command.
		return !awkWritesFile.MatchString(sub)
	case "git":
		if len(fields) >= 2 {
			return readOnlyGitSubcommands[fields[1]]
		}
	case "command":
		// `command -v NAME` / `command -V NAME` only look NAME up and never
		// execute it, so they are read-only. Bare `command NAME args…` *runs*
		// NAME (e.g. `command rm -rf /`), so it must not be auto-allowed. Only
		// leading option tokens count — a `-v` after the name is an argument to
		// NAME, not to `command`.
		for _, f := range fields[1:] {
			if !strings.HasPrefix(f, "-") {
				break // reached NAME; no -v/-V option means command executes it
			}
			if strings.ContainsAny(f, "vV") {
				return true
			}
		}
	}
	return false
}

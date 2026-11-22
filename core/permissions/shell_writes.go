package permissions

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// maxShellWriteTargets caps the candidates one command yields.
const maxShellWriteTargets = 16

// ShellWriteTargets lists the files a shell command may write, read off its
// text: output redirections, tee, sed -i / perl -i, cp/mv/install
// destinations, the output option of pandoc/curl/sort/wget, zip and tar -c
// archives. Paths are absolute and cleaned, in order of appearance, without
// duplicates, at most maxShellWriteTargets. A relative path resolves against
// cwd, updated by each `cd DIR` segment that precedes it; a word the shell
// expands ($, backtick, glob) is dropped, and so is /dev/… and /proc/….
//
// It reads text only, except os.UserHomeDir for `~/` and an os.Stat of a
// cp/mv/install destination to see whether it is a directory. The result is a
// list of candidates: the caller checks on disk which ones were written.
func ShellWriteTargets(command, cwd string) []string {
	if strings.TrimSpace(command) == "" {
		return nil
	}
	// Heredoc bodies are data, never shell syntax: cut them off first.
	shell, bodies := splitHeredocs(command)
	c := &targetCollector{seen: map[string]bool{}}
	dir := cwd
	segs := splitWithOps(shell)
	bodyIdx := 0
	for _, sg := range segs {
		if c.full() {
			break
		}
		lx := lexSegment(sg.text)
		var segBodies []string
		if lx.heredocs > 0 {
			end := bodyIdx + lx.heredocs
			if end > len(bodies) {
				end = len(bodies)
			}
			if bodyIdx < end {
				segBodies = bodies[bodyIdx:end]
			}
			bodyIdx += lx.heredocs
		}
		words := lx.words
		// A cd changes the directory of what follows, unless it runs in a
		// pipeline, a subshell or the background.
		inPipe := sg.before == "|" || sg.before == "|&" || sg.after == "|" || sg.after == "|&" || sg.after == "&"
		if len(words) > 0 && words[0].val == "cd" && len(lx.redirects) == 0 {
			if !inPipe && !lx.subshell {
				dir = cdTarget(words[1:], dir)
			}
			continue
		}
		for _, w := range lx.redirects {
			c.add(resolveWord(w, dir))
		}
		words = skipWrappers(words)
		if len(words) == 0 {
			continue
		}
		for _, p := range commandTargets(words, dir) {
			c.add(p)
		}
		if pythonName.MatchString(filepath.Base(words[0].val)) {
			for _, p := range pythonTargets(words[1:], segBodies, dir) {
				c.add(p)
			}
		}
	}
	if len(c.out) == 0 {
		return nil
	}
	return c.out
}

type targetCollector struct {
	out  []string
	seen map[string]bool
}

func (c *targetCollector) full() bool { return len(c.out) >= maxShellWriteTargets }

func (c *targetCollector) add(p string) {
	if p == "" || c.full() || c.seen[p] {
		return
	}
	c.seen[p] = true
	c.out = append(c.out, p)
}

// opSegment is one subcommand with the operators around it.
type opSegment struct {
	text, before, after string
}

// splitWithOps is splitCompound keeping the operator on each side of a segment.
func splitWithOps(cmd string) []opSegment {
	var out []opSegment
	var cur strings.Builder
	prev := ""
	flush := func(op string) {
		s := strings.TrimSpace(cur.String())
		if s != "" {
			out = append(out, opSegment{text: s, before: prev, after: op})
		}
		prev = op
		cur.Reset()
	}
	active := unquotedMask(cmd)
	for i := 0; i < len(cmd); i++ {
		ch := cmd[i]
		if !active[i] {
			cur.WriteByte(ch)
			continue
		}
		matched := ""
		for _, op := range compoundOps {
			if strings.HasPrefix(cmd[i:], op) {
				matched = op
				break
			}
		}
		if matched == "&" && isRedirectAmp(cmd, i) {
			matched = ""
		}
		// `>|` is a redirect (clobber), not a pipe.
		if matched == "|" && i > 0 && cmd[i-1] == '>' && active[i-1] {
			matched = ""
		}
		if matched != "" {
			flush(matched)
			i += len(matched) - 1
			continue
		}
		cur.WriteByte(ch)
	}
	flush("")
	return out
}

// shellWord is one word of a segment: val is what the shell passes (quotes
// removed), raw is the text as written.
type shellWord struct {
	val, raw string
}

// lexedSegment is a segment cut into command words and redirection targets.
type lexedSegment struct {
	words     []shellWord
	redirects []shellWord // output redirection targets
	heredocs  int         // number of << operators (heredoc starts)
	subshell  bool        // the segment opens with '('
}

// lexSegment tokenises one subcommand. Output redirection targets go to
// redirects; input redirections, heredoc tags and fd duplications are
// skipped; everything else is a command word.
func lexSegment(seg string) lexedSegment {
	var lx lexedSegment
	n := len(seg)
	i := 0
	first := true
	isSpace := func(b byte) bool { return b == ' ' || b == '\t' || b == '\n' || b == '\r' }
	for i < n {
		for i < n && isSpace(seg[i]) {
			i++
		}
		if i >= n {
			break
		}
		ch := seg[i]
		if ch == '(' || ch == ')' {
			if ch == '(' && first {
				lx.subshell = true
			}
			i++
			first = false
			continue
		}
		// Redirection operator: optional fd digits, then < or >; or &>.
		j := i
		for j < n && seg[j] >= '0' && seg[j] <= '9' {
			j++
		}
		amp := false
		if j == i && ch == '&' && i+1 < n && seg[i+1] == '>' {
			amp = true
			j = i + 1
		}
		if j < n && (seg[j] == '>' || seg[j] == '<') {
			first = false
			if seg[j] == '<' {
				k := j + 1
				switch {
				case strings.HasPrefix(seg[j:], "<<<"):
					k = j + 3
				case strings.HasPrefix(seg[j:], "<<"):
					k = j + 2
					if k < n && seg[k] == '-' {
						k++
					}
					lx.heredocs++
				case strings.HasPrefix(seg[j:], "<("):
					i = j + 1
					continue
				case strings.HasPrefix(seg[j:], "<>"):
					k = j + 2
				}
				_, k = readWord(seg, k)
				i = k
				continue
			}
			// Output.
			k := j + 1
			dup := false
			if !amp && k < n && (seg[k] == '>' || seg[k] == '|') {
				k++
			} else if amp && k < n && seg[k] == '>' {
				k++
			} else if !amp && k < n && seg[k] == '&' {
				k++
				dup = true
			}
			if k < n && seg[k] == '(' { // process substitution >(…)
				i = k
				continue
			}
			w, k2 := readWord(seg, k)
			i = k2
			if w.raw == "" {
				continue
			}
			if dup && (w.raw == "-" || fdNumber.MatchString(w.raw)) {
				continue
			}
			lx.redirects = append(lx.redirects, w)
			continue
		}
		w, k := readWord(seg, i)
		if k == i { // operator char we do not know: skip it
			i++
			continue
		}
		i = k
		first = false
		lx.words = append(lx.words, w)
	}
	return lx
}

// readWord reads one shell word starting at i (after skipping blanks),
// removing one layer of quotes and backslash escapes. It stops at an unquoted
// blank or one of < > ( ).
func readWord(s string, i int) (shellWord, int) {
	n := len(s)
	for i < n && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	start := i
	var val strings.Builder
	for i < n {
		ch := s[i]
		switch {
		case ch == '\'':
			end := strings.IndexByte(s[i+1:], '\'')
			if end < 0 {
				val.WriteString(s[i+1:])
				i = n
				continue
			}
			val.WriteString(s[i+1 : i+1+end])
			i += end + 2
			continue
		case ch == '"':
			i++
			for i < n && s[i] != '"' {
				if s[i] == '\\' && i+1 < n && strings.IndexByte(`"\$`+"`", s[i+1]) >= 0 {
					val.WriteByte(s[i+1])
					i += 2
					continue
				}
				val.WriteByte(s[i])
				i++
			}
			i++ // closing quote
			continue
		case ch == '\\' && i+1 < n:
			val.WriteByte(s[i+1])
			i += 2
			continue
		case ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' || strings.IndexByte("<>()", ch) >= 0:
			return shellWord{val: val.String(), raw: s[start:i]}, i
		}
		val.WriteByte(ch)
		i++
	}
	if i > n {
		i = n
	}
	return shellWord{val: val.String(), raw: s[start:i]}, i
}

// wrapperArgs are the options of a wrapper that take a value.
var wrapperArgs = map[string]map[string]bool{
	"sudo":    {"-u": true, "-g": true, "-C": true, "-h": true, "-p": true, "-r": true, "-t": true, "-U": true},
	"env":     {"-u": true, "-C": true, "-S": true},
	"timeout": {"-s": true, "-k": true},
	"nice":    {"-n": true},
	"stdbuf":  {},
	"nohup":   {},
	"time":    {"-o": true, "-f": true},
}

// skipWrappers drops leading VAR=value assignments and process wrappers
// (sudo, env, timeout, nice, nohup, time, stdbuf) with their options.
func skipWrappers(words []shellWord) []shellWord {
	for len(words) > 0 {
		head := words[0].val
		if isAssignment(head) {
			words = words[1:]
			continue
		}
		valued, ok := wrapperArgs[filepath.Base(head)]
		if !ok {
			return words
		}
		name := filepath.Base(head)
		words = words[1:]
		for len(words) > 0 {
			v := words[0].val
			if strings.HasPrefix(v, "-") {
				words = words[1:]
				if valued[v] && len(words) > 0 {
					words = words[1:]
				}
				continue
			}
			if name == "env" && isAssignment(v) {
				words = words[1:]
				continue
			}
			if name == "timeout" && durationOrNumber.MatchString(v) {
				words = words[1:]
				// only the first duration
				break
			}
			break
		}
	}
	return words
}

func isAssignment(w string) bool {
	eq := strings.IndexByte(w, '=')
	if eq <= 0 {
		return false
	}
	for i := 0; i < eq; i++ {
		c := w[i]
		if !(c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || i > 0 && c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

// cdTarget is the directory after `cd ARGS` run in dir; "" when unknown.
func cdTarget(args []shellWord, dir string) string {
	var ops []shellWord
	for _, a := range args {
		if strings.HasPrefix(a.val, "-") && a.val != "-" {
			continue
		}
		ops = append(ops, a)
	}
	if len(ops) == 0 {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		return home
	}
	if ops[0].val == "-" {
		return ""
	}
	return resolveRaw(ops[0], dir, true)
}

// resolveWord turns a word into an absolute candidate path, or "".
func resolveWord(w shellWord, dir string) string {
	return resolveRaw(w, dir, false)
}

func resolveRaw(w shellWord, dir string, allowDevProc bool) string {
	if w.val == "" || w.val == "-" || strings.ContainsAny(w.raw, "$`*?[{") {
		return ""
	}
	p := w.val
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return ""
		}
		p = filepath.Join(home, strings.TrimPrefix(p, "~"))
	} else if strings.HasPrefix(p, "~") {
		return "" // ~user
	}
	if !filepath.IsAbs(p) {
		if dir == "" {
			return ""
		}
		p = filepath.Join(dir, p)
	}
	p = filepath.Clean(p)
	if !allowDevProc {
		for _, root := range []string{"/dev", "/proc"} {
			if p == root || strings.HasPrefix(p, root+"/") {
				return ""
			}
		}
	}
	return p
}

// commandTargets returns the files a (wrapper-stripped) command writes by
// itself, from its arguments.
func commandTargets(words []shellWord, dir string) []string {
	name := filepath.Base(words[0].val)
	args := words[1:]
	res := func(ws ...shellWord) []string {
		var out []string
		for _, w := range ws {
			if p := resolveWord(w, dir); p != "" {
				out = append(out, p)
			}
		}
		return out
	}
	switch name {
	case "tee":
		var ops []shellWord
		for _, a := range args {
			if strings.HasPrefix(a.val, "-") && a.val != "-" {
				continue
			}
			ops = append(ops, a)
		}
		return res(ops...)
	case "sed", "gsed":
		return res(sedFiles(args)...)
	case "perl":
		return res(perlFiles(args)...)
	case "cp", "mv", "install":
		return copyTargets(name, args, dir)
	case "pandoc", "curl", "sort":
		return res(outputOption(args, "o", []string{"--output"})...)
	case "wget":
		return res(outputOption(args, "O", []string{"--output-document"})...)
	case "zip":
		for i := 0; i < len(args); i++ {
			a := args[i]
			if strings.HasPrefix(a.val, "-") {
				continue
			}
			if !strings.Contains(filepath.Base(a.val), ".") {
				a = shellWord{val: a.val + ".zip", raw: a.raw + ".zip"}
			}
			return res(a)
		}
	case "tar":
		return res(tarArchive(args)...)
	}
	return nil
}

// sedFiles returns the file operands of an in-place sed, nil otherwise.
func sedFiles(args []shellWord) []shellWord {
	inPlace := false
	hasScript := false
	var ops []shellWord
	for i := 0; i < len(args); i++ {
		v := args[i].val
		switch {
		case v == "--":
			ops = append(ops, args[i+1:]...)
			i = len(args)
		case strings.HasPrefix(v, "--"):
			if strings.HasPrefix(v, "--in-place") {
				inPlace = true
			}
			if v == "--expression" || v == "--file" || v == "--line-length" {
				hasScript = hasScript || v != "--line-length"
				i++
			} else if strings.HasPrefix(v, "--expression=") || strings.HasPrefix(v, "--file=") {
				hasScript = true
			}
		case strings.HasPrefix(v, "-") && len(v) > 1:
			// A short bundle: -i[SUFFIX], -n, -E, -e SCRIPT, -f FILE, -l N.
			for k := 1; k < len(v); k++ {
				c := v[k]
				if c == 'i' {
					inPlace = true
					break // the rest is the backup suffix
				}
				if c == 'e' || c == 'f' || c == 'l' {
					if c != 'l' {
						hasScript = true
					}
					if k == len(v)-1 {
						i++ // value is the next word
					}
					break
				}
			}
		default:
			ops = append(ops, args[i])
		}
	}
	if !inPlace {
		return nil
	}
	if !hasScript && len(ops) > 0 {
		ops = ops[1:]
	}
	return ops
}

// perlFiles returns the file operands of an in-place perl, nil otherwise.
func perlFiles(args []shellWord) []shellWord {
	inPlace := false
	hasScript := false
	var ops []shellWord
	for i := 0; i < len(args); i++ {
		v := args[i].val
		switch {
		case v == "--":
			ops = append(ops, args[i+1:]...)
			i = len(args)
		case strings.HasPrefix(v, "-") && len(v) > 1:
			for k := 1; k < len(v); k++ {
				c := v[k]
				if c == 'i' {
					inPlace = true
					break // the rest is the backup suffix
				}
				if c == 'e' || c == 'E' {
					hasScript = true
					if k == len(v)-1 {
						i++
					}
					break
				}
				if c == 'M' || c == 'm' || c == 'I' || c == 'x' || c == 'l' || c == '0' {
					break // glued value
				}
			}
		default:
			ops = append(ops, args[i])
		}
	}
	if !inPlace {
		return nil
	}
	if !hasScript && len(ops) > 0 {
		ops = ops[1:]
	}
	return ops
}

// copyValued are the cp/mv/install options that take a value.
var copyValued = map[string]bool{
	"-m": true, "-o": true, "-g": true, "-S": true, "--suffix": true, "--mode": true, "--owner": true, "--group": true,
}

// copyTargets returns the destination files of cp, mv or install.
func copyTargets(name string, args []shellWord, dir string) []string {
	var ops []shellWord
	targetDir := ""
	for i := 0; i < len(args); i++ {
		v := args[i].val
		switch {
		case v == "--":
			ops = append(ops, args[i+1:]...)
			i = len(args)
		case v == "-t" || v == "--target-directory":
			if i+1 < len(args) {
				targetDir = resolveWord(args[i+1], dir)
				if targetDir == "" {
					return nil
				}
				i++
			}
		case strings.HasPrefix(v, "--target-directory="):
			targetDir = resolveWord(shellWord{val: strings.TrimPrefix(v, "--target-directory="), raw: args[i].raw}, dir)
			if targetDir == "" {
				return nil
			}
		case name == "install" && (v == "-d" || v == "--directory"):
			return nil
		case strings.HasPrefix(v, "-") && len(v) > 1:
			if name == "install" && copyValued[v] {
				i++
			}
		default:
			ops = append(ops, args[i])
		}
	}
	var sources []shellWord
	if targetDir == "" {
		if len(ops) < 2 {
			return nil
		}
		dest := resolveWord(ops[len(ops)-1], dir)
		if dest == "" {
			return nil
		}
		sources = ops[:len(ops)-1]
		if fi, err := os.Stat(dest); err == nil && fi.IsDir() {
			targetDir = dest
		} else {
			if len(sources) > 1 {
				return nil // several sources need a directory
			}
			return []string{dest}
		}
	} else {
		sources = ops
	}
	var out []string
	for _, s := range sources {
		if s.val == "" || strings.ContainsAny(s.raw, "$`*?[{") {
			continue
		}
		base := filepath.Base(filepath.Clean(s.val))
		if base == "." || base == "/" || base == ".." {
			continue
		}
		out = append(out, filepath.Join(targetDir, base))
	}
	return out
}

// outputOption returns the value of a -X FILE / -XFILE / --long FILE /
// --long=FILE output option. A short bundle ending in X (curl -sLo f) counts.
func outputOption(args []shellWord, short string, long []string) []shellWord {
	var out []shellWord
	for i := 0; i < len(args); i++ {
		v := args[i].val
		if strings.HasPrefix(v, "--") {
			for _, l := range long {
				if v == l && i+1 < len(args) {
					out = append(out, args[i+1])
					i++
				} else if strings.HasPrefix(v, l+"=") {
					out = append(out, shellWord{val: strings.TrimPrefix(v, l+"="), raw: strings.TrimPrefix(args[i].raw, l+"=")})
				}
			}
			continue
		}
		if !strings.HasPrefix(v, "-") || len(v) < 2 {
			continue
		}
		idx := strings.Index(v[1:], short)
		if idx < 0 {
			continue
		}
		idx++
		if idx == len(v)-1 {
			if i+1 < len(args) {
				out = append(out, args[i+1])
				i++
			}
		} else if idx == 1 {
			out = append(out, shellWord{val: v[2:], raw: args[i].raw[2:]})
		}
	}
	return out
}

// tarArchive returns the archive tar creates, nil when it does not create one.
func tarArchive(args []shellWord) []shellWord {
	create := false
	var archive []shellWord
	needF := 0 // number of 'f' values still to take, in order
	for i := 0; i < len(args); i++ {
		v := args[i].val
		switch {
		case v == "--create":
			create = true
		case v == "--file" && i+1 < len(args):
			archive = []shellWord{args[i+1]}
			i++
		case strings.HasPrefix(v, "--file="):
			archive = []shellWord{{val: strings.TrimPrefix(v, "--file="), raw: strings.TrimPrefix(args[i].raw, "--file=")}}
		case strings.HasPrefix(v, "--"):
		case i == 0 || strings.HasPrefix(v, "-"):
			bundle := strings.TrimPrefix(v, "-")
			if i == 0 && !strings.HasPrefix(v, "-") && strings.ContainsAny(bundle, "/.") {
				continue
			}
			if strings.ContainsRune(bundle, 'c') {
				create = true
			}
			if k := strings.IndexByte(bundle, 'f'); k >= 0 {
				if k < len(bundle)-1 && strings.HasPrefix(v, "-") {
					archive = []shellWord{{val: bundle[k+1:], raw: bundle[k+1:]}}
				} else {
					needF++
				}
			}
		default:
			if needF > 0 {
				archive = []shellWord{args[i]}
				needF--
			}
		}
	}
	if !create {
		return nil
	}
	return archive
}

// heredocTag is a pending heredoc: its terminator and whether leading tabs
// are stripped (<<-).
type heredocTag struct {
	name  string
	strip bool
}

// splitHeredocs removes the body lines of each heredoc (<<TAG, <<'TAG',
// <<"TAG", <<-TAG; not the <<< here-string) up to the line equal to TAG, and
// returns the remaining shell text and the bodies in order. Only an unquoted
// << starts a heredoc. An operator with no tag, or a body never terminated,
// still yields one (possibly partial) body so the count matches the lexer's.
func splitHeredocs(cmd string) (string, []string) {
	if !strings.Contains(cmd, "<<") && !strings.Contains(cmd, "((") && !strings.Contains(cmd, "#") {
		return cmd, nil
	}
	var out strings.Builder
	var bodies []string
	var pending []heredocTag
	var quote byte
	n := len(cmd)
	for i := 0; i < n; i++ {
		ch := cmd[i]
		if quote == 0 && ch == '\\' && i+1 < n {
			out.WriteByte(ch)
			out.WriteByte(cmd[i+1])
			i++
			continue
		}
		if quote != 0 {
			if ch == '\\' && quote == '"' && i+1 < n {
				out.WriteByte(ch)
				out.WriteByte(cmd[i+1])
				i++
				continue
			}
			if ch == quote {
				quote = 0
			}
			out.WriteByte(ch)
			continue
		}
		switch {
		case ch == '\'' || ch == '"':
			quote = ch
			out.WriteByte(ch)
		case ch == '#' && (i == 0 || strings.IndexByte(" \t\n;&|()", cmd[i-1]) >= 0):
			// A comment runs to the end of the line; it is dropped so its text
			// (a `<<`, a `>`) is never read as shell.
			for i+1 < n && cmd[i+1] != '\n' {
				i++
			}
		case strings.HasPrefix(cmd[i:], "(("):
			// Arithmetic ((…)) or $((…)): copied with < and > blanked, so
			// a shift is neither a heredoc nor a redirect.
			depth := 0
			for ; i < n; i++ {
				c := cmd[i]
				switch c {
				case '(':
					depth++
				case ')':
					depth--
				case '<', '>':
					c = ' '
				}
				out.WriteByte(c)
				if depth == 0 {
					break
				}
			}
		case strings.HasPrefix(cmd[i:], "<<<"):
			out.WriteString("<<<")
			i += 2
		case strings.HasPrefix(cmd[i:], "<<"):
			j := i + 2
			strip := false
			if j < n && cmd[j] == '-' {
				strip = true
				j++
			}
			w, k := readWord(cmd, j)
			out.WriteString(cmd[i:k])
			pending = append(pending, heredocTag{name: w.val, strip: strip})
			i = k - 1
		case ch == '\n' && len(pending) > 0:
			out.WriteByte('\n')
			pos := i + 1
			for _, tg := range pending {
				if tg.name == "" {
					bodies = append(bodies, "")
					continue
				}
				var body strings.Builder
				for pos < n {
					end := strings.IndexByte(cmd[pos:], '\n')
					line := cmd[pos:]
					next := n
					if end >= 0 {
						line = cmd[pos : pos+end]
						next = pos + end + 1
					}
					cmp := line
					if tg.strip {
						cmp = strings.TrimLeft(cmp, "\t")
					}
					pos = next
					if cmp == tg.name {
						break
					}
					body.WriteString(line)
					body.WriteByte('\n')
				}
				bodies = append(bodies, body.String())
			}
			pending = nil
			i = pos - 1
		default:
			out.WriteByte(ch)
		}
	}
	for range pending {
		bodies = append(bodies, "")
	}
	return out.String(), bodies
}

// pythonName matches python, python3 and python3.N.
var pythonName = regexp.MustCompile(`^python(3(\.[0-9]+)?)?$`)

// pyMaxMatches bounds the regexp matches read from one piece of code, so a
// very large script costs one linear scan, not one allocation per match.
const pyMaxMatches = 256

const pyStr = `(?:'([^'\n]*)'|"([^"\n]*)")`

var (
	pyOpenAt = regexp.MustCompile(`^open\(\s*(?:'([^'\n]*)'|"([^"\n]*)"|([A-Za-z_][A-Za-z0-9_]*))\s*,\s*(?:mode\s*=\s*)?` + pyStr)
	pyPathAt = regexp.MustCompile(`^Path\(\s*(?:'([^'\n]*)'|"([^"\n]*)"|([A-Za-z_][A-Za-z0-9_]*))\s*\)\s*\.\s*write_(?:text|bytes)\s*\(`)
)

// pythonTargets scans the code of a python command (its -c argument and the
// heredoc bodies of its segment) for files opened for writing:
// open('P', 'w'|'a'|'x'…), Path('P').write_text/write_bytes, and the same
// through a variable assigned a string literal earlier in the code.
func pythonTargets(args []shellWord, bodies []string, dir string) []string {
	var codes []string
	for i := 0; i < len(args); i++ {
		if args[i].val == "-c" && i+1 < len(args) {
			codes = append(codes, args[i+1].val)
			break
		}
		if strings.HasPrefix(args[i].val, "-c") && len(args[i].val) > 2 {
			codes = append(codes, args[i].val[2:])
			break
		}
	}
	codes = append(codes, bodies...)
	var out []string
	for _, code := range codes {
		out = append(out, pythonCodeTargets(code, dir)...)
	}
	return out
}

type pyHit struct {
	pos  int
	path string
}

// pyWindow is how far past "open(" / "Path(" a call is matched.
const pyWindow = 512

func isIdentByte(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

// pythonCodeTargets finds the calls with strings.Index and matches each with
// an anchored regexp on a short window, so a very large script costs a few
// linear scans and at most pyMaxMatches regexp runs.
func pythonCodeTargets(code, dir string) []string {
	type assign struct {
		pos int
		val string
	}
	// Each variable's string assignments are found once, with one scan.
	varCache := map[string][]assign{}
	lookup := func(name string, pos int) (string, bool) {
		as, seen := varCache[name]
		if !seen {
			re, err := regexp.Compile(`(?:^|[^A-Za-z0-9_.])` + regexp.QuoteMeta(name) + `\s*=\s*` + pyStr)
			if err == nil {
				for _, m := range re.FindAllStringSubmatchIndex(code, pyMaxMatches) {
					if m[2] >= 0 {
						as = append(as, assign{m[0], code[m[2]:m[3]]})
					} else if m[4] >= 0 {
						as = append(as, assign{m[0], code[m[4]:m[5]]})
					}
				}
			}
			varCache[name] = as
		}
		val, ok := "", false
		for _, a := range as {
			if a.pos >= pos {
				break
			}
			val, ok = a.val, true
		}
		return val, ok
	}
	var hits []pyHit
	budget := pyMaxMatches
	scan := func(needle string, re *regexp.Regexp, accept func(w string, m []int) bool) {
		from := 0
		for budget > 0 {
			k := strings.Index(code[from:], needle)
			if k < 0 {
				return
			}
			pos := from + k
			from = pos + len(needle)
			if pos > 0 && (isIdentByte(code[pos-1])) {
				continue
			}
			budget--
			end := pos + pyWindow
			if end > len(code) {
				end = len(code)
			}
			w := code[pos:end]
			m := re.FindStringSubmatchIndex(w)
			if m == nil || !accept(w, m) {
				continue
			}
			g := func(k int) (string, bool) {
				if m[2*k] < 0 {
					return "", false
				}
				return w[m[2*k]:m[2*k+1]], true
			}
			var p string
			ok := false
			if v, has := g(1); has {
				p, ok = v, true
			} else if v, has := g(2); has {
				p, ok = v, true
			} else if v, has := g(3); has {
				p, ok = lookup(v, pos)
			}
			if ok {
				hits = append(hits, pyHit{pos, p})
			}
		}
	}
	scan("open(", pyOpenAt, func(w string, m []int) bool {
		mode := ""
		if m[8] >= 0 {
			mode = w[m[8]:m[9]]
		} else if m[10] >= 0 {
			mode = w[m[10]:m[11]]
		}
		return strings.ContainsAny(mode, "wax")
	})
	scan("Path(", pyPathAt, func(string, []int) bool { return true })
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].pos < hits[j].pos })
	var out []string
	for _, h := range hits {
		if p := resolveWord(shellWord{val: h.path, raw: h.path}, dir); p != "" {
			out = append(out, p)
		}
	}
	return out
}

package lessons

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
	"unicode/utf8"
)

var (
	reQuoted = regexp.MustCompile("\"[^\"]*\"|'[^']*'|`[^`]*`")
	// rePath matches a path after a non-path character (start, space, "(",
	// "=", ":", …): an absolute or ./ ../ path, a relative path with a
	// directory, or a dotted name — replaced only when its extension is a
	// known file extension (see replacePath).
	rePath      = regexp.MustCompile(`(^|[^\w./-])(?:\.{0,2}/[^\s)\]},;]*|[\w.-]+(?:/[\w.-]+)+/?|[\w-]+(?:\.[\w-]+)*\.[a-z][a-z0-9]{0,7})`)
	reUUID      = regexp.MustCompile(`\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)
	reHex       = regexp.MustCompile(`\b0x[0-9a-f]+\b|\b[0-9a-f]{8,}\b`)
	reTimestamp = regexp.MustCompile(`\d{4}-\d{2}-\d{2}[t ]\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:z|[+-]\d{2}:?\d{2})?`)
	reDigits    = regexp.MustCompile(`\d+`)
	reSpace     = regexp.MustCompile(`\s+`)
	reAssign    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)
)

// normalise strips the volatile parts of an error text so two occurrences of
// the same failure compare equal.
func normalise(text string) string {
	// Only the head can reach the hash; normalising it alone bounds the cost.
	s := strings.ToLower(truncateRunes(text, 8*SignatureTextChars))
	s = reQuoted.ReplaceAllString(s, "S")
	s = reTimestamp.ReplaceAllString(s, "N")
	s = rePath.ReplaceAllStringFunc(s, replacePath)
	s = reUUID.ReplaceAllString(s, "H")
	s = reHex.ReplaceAllString(s, "H")
	s = reDigits.ReplaceAllString(s, "N")
	s = strings.TrimSpace(reSpace.ReplaceAllString(s, " "))
	return truncateRunes(s, SignatureTextChars)
}

// fileExtensions are the extensions that make a bare dotted name (no "/") a
// file name. Anything else — strings.builder, http.client, deployments.apps —
// is an identifier and stays in the signature. Hostnames (api.example.com) are
// deliberately not paths either: which host failed is part of the error.
var fileExtensions = map[string]bool{
	"go": true, "py": true, "js": true, "ts": true, "tsx": true, "jsx": true,
	"json": true, "yaml": true, "yml": true, "toml": true, "md": true, "txt": true,
	"sh": true, "rs": true, "java": true, "kt": true, "c": true, "h": true,
	"cc": true, "cpp": true, "hpp": true, "cs": true, "rb": true, "php": true,
	"html": true, "css": true, "scss": true, "xml": true, "sql": true,
	"proto": true, "lock": true, "mod": true, "sum": true, "cfg": true,
	"conf": true, "ini": true, "env": true, "log": true, "csv": true,
	"tf": true, "gradle": true,
	"mjs": true, "cjs": true, "vue": true, "svelte": true, "swift": true,
	"scala": true, "kts": true, "dart": true, "lua": true, "pl": true, "r": true,
	"bash": true, "zsh": true, "ps1": true, "j2": true, "tpl": true, "tmpl": true,
	"hcl": true, "tfvars": true, "properties": true, "jsonl": true, "ndjson": true,
	"ipynb": true, "rst": true, "pem": true, "pdf": true, "png": true, "jpg": true,
	"jpeg": true, "gif": true, "svg": true, "zip": true, "gz": true, "tar": true,
	"tgz": true,
}

// replacePath replaces one rePath match by the path placeholder, keeping the
// non-path character before it. A match with no "/" is a bare dotted name: it
// is a path only when its extension is in fileExtensions.
func replacePath(m string) string {
	prefix := ""
	if r, size := utf8.DecodeRuneInString(m); !isPathStart(r) {
		prefix = m[:size]
	}
	p := m[len(prefix):]
	if !strings.Contains(p, "/") && !fileExtensions[p[strings.LastIndexByte(p, '.')+1:]] {
		return m
	}
	return prefix + "P"
}

// isPathStart reports whether r can begin a path: a word character, ".", "/"
// or "-" (the characters rePath's prefix group excludes).
func isPathStart(r rune) bool {
	return r == '_' || r == '.' || r == '/' || r == '-' ||
		(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}

// Signature identifies a kind of failure: the hex sha256 of the tool key, a
// line break and the normalised error text.
func Signature(toolKey, errText string) string {
	sum := sha256.Sum256([]byte(toolKey + "\n" + normalise(errText)))
	return hex.EncodeToString(sum[:])
}

// ToolKey is "Bash:<first word>" for a Bash call, the tool name otherwise.
func ToolKey(tool string, args map[string]any) string {
	if tool != "Bash" {
		return tool
	}
	cmd, _ := args["command"].(string)
	if w := firstWord(cmd); w != "" {
		return "Bash:" + w
	}
	return "Bash"
}

// firstWord returns the program a shell command runs, skipping NAME=value
// assignments and the wrappers sudo, env, timeout <n>, nice, nohup and time.
func firstWord(cmd string) string {
	f := strings.Fields(cmd)
	for i := 0; i < len(f); i++ {
		w := f[i]
		switch {
		case reAssign.MatchString(w):
		case w == "sudo", w == "env", w == "nice", w == "nohup", w == "time":
		case w == "timeout":
			i++ // its duration
		default:
			return w
		}
	}
	return ""
}

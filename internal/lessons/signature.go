package lessons

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
)

var (
	reQuoted    = regexp.MustCompile("\"[^\"]*\"|'[^']*'|`[^`]*`")
	rePath      = regexp.MustCompile(`(^|\s)(?:/|\./)\S*`)
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
	s := strings.ToLower(text)
	s = reQuoted.ReplaceAllString(s, "S")
	s = rePath.ReplaceAllString(s, "${1}P")
	s = reUUID.ReplaceAllString(s, "H")
	s = reHex.ReplaceAllString(s, "H")
	s = reTimestamp.ReplaceAllString(s, "N")
	s = reDigits.ReplaceAllString(s, "N")
	s = strings.TrimSpace(reSpace.ReplaceAllString(s, " "))
	return truncateRunes(s, SignatureTextChars)
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

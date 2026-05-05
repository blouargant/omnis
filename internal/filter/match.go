package filter

import (
	"path/filepath"
	"strings"
)

// ApplyForCommand matches a command line against the registry and applies its
// pipeline to the given output. Returns (output, applied, error).
func ApplyForCommand(reg *Registry, commandLine, output string) (string, bool, error) {
	if reg == nil || strings.TrimSpace(commandLine) == "" {
		return output, false, nil
	}

	parts := strings.Fields(commandLine)
	if len(parts) == 0 {
		return output, false, nil
	}

	cmd := filepath.Base(parts[0])
	rest := []string{}
	if len(parts) > 1 {
		rest = parts[1:]
	}

	subcommand := ""
	args := rest
	if len(rest) > 0 {
		subcommand = rest[0]
		args = rest[1:]
	}

	f := reg.Match(cmd, subcommand, args)
	if f == nil {
		return output, false, nil
	}

	filtered, err := ApplyPipeline(f, output)
	if err != nil {
		if f.OnError == "passthrough" || f.OnError == "" {
			return output, true, nil
		}
		if f.OnError == "empty" {
			return "", true, nil
		}
		return output, true, err
	}
	return filtered, true, nil
}

package filter

import (
	"fmt"
	"strings"
)

// ApplyPipeline executes filter actions sequentially on input.
func ApplyPipeline(f *Filter, input string) (string, error) {
	lines := strings.Split(input, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	for i, l := range lines {
		if len(l) > 0 && l[len(l)-1] == '\r' {
			lines[i] = l[:len(l)-1]
		}
	}

	result := ActionResult{Lines: lines, Metadata: make(map[string]any)}
	for i, action := range f.Pipeline {
		fn, ok := GetAction(action.ActionName)
		if !ok {
			return "", fmt.Errorf("unknown action %q at pipeline[%d]", action.ActionName, i)
		}
		var err error
		result, err = fn(result, action.Params)
		if err != nil {
			return "", fmt.Errorf("pipeline[%d] %s: %w", i, action.ActionName, err)
		}
	}

	return strings.Join(result.Lines, "\n") + "\n", nil
}

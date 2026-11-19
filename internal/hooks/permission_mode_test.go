package hooks

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunFillsPermissionMode(t *testing.T) {
	skipOnWindows(t)
	SetPermissionModeResolver(func(string) string { return "plan" })
	t.Cleanup(func() { SetPermissionModeResolver(nil) })

	capture := func(sid string) string {
		outFile := filepath.Join(t.TempDir(), "in.json")
		cfg, err := Parse([]byte(`{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"command":"cat > ` + outFile + `"}]}]}}`))
		if err != nil {
			t.Fatal(err)
		}
		cfg.Run(context.Background(), PreToolUse, "Bash", Input{SessionID: sid, ToolName: "Bash"}, t.TempDir(), 10*time.Second)
		b, _ := os.ReadFile(outFile)
		return string(b)
	}
	if got := capture("s1"); !strings.Contains(got, `"permission_mode":"plan"`) {
		t.Fatalf("stdin = %s, want permission_mode plan", got)
	}
	if got := capture(""); strings.Contains(got, "permission_mode") {
		t.Fatalf("empty session id must leave the field absent: %s", got)
	}
}

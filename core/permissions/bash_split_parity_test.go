package permissions

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// pySegments runs the shipped k8s-validate.py hook script's segments()
// function against cmd (by importing the file directly, so main() never
// runs) and returns its result, for comparison against splitCompound.
func pySegments(t *testing.T, cmd string) []string {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	scriptPath := filepath.Join("..", "..", "config", "hooks", "k8s-validate.py")
	const snippet = `
import importlib.util, json, sys
spec = importlib.util.spec_from_file_location("k8s_validate", sys.argv[1])
mod = importlib.util.module_from_spec(spec)
spec.loader.exec_module(mod)
print(json.dumps(mod.segments(sys.argv[2])))
`
	out, err := exec.Command("python3", "-c", snippet, scriptPath, cmd).Output()
	if err != nil {
		t.Fatalf("pySegments(%q): %v", cmd, err)
	}
	var got []string
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("pySegments(%q): unmarshal %q: %v", cmd, out, err)
	}
	return got
}

// The script reimplements the compound-splitting that match_bash.go already does
// in Go. That duplication is accepted (see the spec, §3), so it is pinned: both
// must segment the same corpus identically.
func TestPythonAndGoSegmentTheSameCorpus(t *testing.T) {
	corpus := []string{
		"kubectl get pods && kubectl delete pod x",
		"sudo kubectl apply -f a.yaml; echo done",
		"kubectl get pods | grep Running",
		"helm upgrade r c -n ns || kubectl rollout undo deploy/x",
		"echo hi",
	}
	for _, cmd := range corpus {
		got := pySegments(t, cmd)
		want := splitCompound(cmd)
		if len(got) != len(want) {
			t.Fatalf("%q: python segments %v, go segments %v", cmd, got, want)
		}
		for i := range want {
			if strings.TrimSpace(got[i]) != strings.TrimSpace(want[i]) {
				t.Fatalf("%q: segment %d python=%q go=%q", cmd, i, got[i], want[i])
			}
		}
	}
}

package registries

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPermissionSetName(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"permissions/kubectl-readonly/permissions.json": "kubectl-readonly",
		"kubectl-readonly/permissions.json":             "kubectl-readonly",
		"a/b/c/permissions.json":                        "c",
		"kubectl-readonly":                              "kubectl-readonly",
	}
	for in, want := range cases {
		if got := PermissionSetName(in); got != want {
			t.Errorf("PermissionSetName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMergePermissionsFileDedupes(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	base := filepath.Join(dir, "permissions.json")
	if err := os.WriteFile(base, []byte(`{
		"always_allow": [{"pattern": "kubectl get", "reason": "ro"}],
		"ask_user": []
	}`), 0o644); err != nil {
		t.Fatal(err)
	}

	incoming := []byte(`{
		"always_allow": [
			{"pattern": "kubectl get", "reason": "dup"},
			{"pattern": "kubectl logs", "reason": "new"}
		],
		"ask_user": [{"pattern": "kubectl delete", "reason": "danger"}]
	}`)

	added, err := MergePermissionsFile(base, base, incoming)
	if err != nil {
		t.Fatal(err)
	}
	// "kubectl get" is a duplicate (skipped); "kubectl logs" + "kubectl delete" are new.
	if added != 2 {
		t.Errorf("added = %d, want 2", added)
	}

	// Old-format remote rules convert to regex-escape-hatch rules; their
	// canonical keys are "regex|<tools>|<pattern>|<cwd>".
	patterns := InstalledPermissionPatterns(base)
	for _, want := range []string{"regex||kubectl get|", "regex||kubectl logs|", "regex||kubectl delete|"} {
		if !patterns[want] {
			t.Errorf("pattern %q missing after merge", want)
		}
	}

	// Re-merging the same incoming set is idempotent (nothing new).
	added2, err := MergePermissionsFile(base, base, incoming)
	if err != nil {
		t.Fatal(err)
	}
	if added2 != 0 {
		t.Errorf("second merge added = %d, want 0 (idempotent)", added2)
	}
}

func TestParseFrontmatterDeps(t *testing.T) {
	t.Parallel()
	raw := []byte("---\nname: k8s-triage\ndescription: triage\ncommands:\n  - k8s-rollback\npermissions:\n  - kubectl-readonly\n---\nbody")
	fm, err := ParseFrontmatter(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(fm.Commands) != 1 || fm.Commands[0] != "k8s-rollback" {
		t.Errorf("Commands = %v, want [k8s-rollback]", fm.Commands)
	}
	if len(fm.Permissions) != 1 || fm.Permissions[0] != "kubectl-readonly" {
		t.Errorf("Permissions = %v, want [kubectl-readonly]", fm.Permissions)
	}
}

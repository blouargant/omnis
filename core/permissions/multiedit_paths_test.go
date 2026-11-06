package permissions

import "testing"

// MultiEdit carries its paths in files[].file_path, not in a top-level
// file_path. Every path rule and the acceptEdits working-dir check must apply
// to each of those files, exactly as for Edit.
func TestMultiEditPathsAreCheckedPerFile(t *testing.T) {
	multi := func(paths ...string) map[string]any {
		files := make([]any, 0, len(paths))
		for _, p := range paths {
			files = append(files, map[string]any{"file_path": p, "edits": []any{map[string]any{"old_string": "a", "new_string": "b"}}})
		}
		return map[string]any{"files": files}
	}
	cases := []struct {
		name   string
		perms  string
		args   map[string]any
		want   Decision
		defAsk bool
	}{
		{"deny rule hits one of the files", `{"permissions":{"deny":["Edit(.env)"],"defaultMode":"acceptEdits"}}`,
			multi("/w/p/main.go", "/w/p/.env"), DecisionDeny, false},
		{"acceptEdits does not cover a file outside the working dir", `{"permissions":{"defaultMode":"acceptEdits"}}`,
			multi("/w/p/main.go", "/etc/hosts"), DecisionAsk, true},
		{"acceptEdits covers files all inside the working dir", `{"permissions":{"defaultMode":"acceptEdits"}}`,
			multi("/w/p/a.go", "/w/p/sub/b.go"), DecisionAllow, false},
		{"explicit ask on one file wins over allow on the others", `{"permissions":{"allow":["Edit"],"ask":["Edit(//w/p/secret/**)"]}}`,
			multi("/w/p/a.go", "/w/p/secret/k.txt"), DecisionAsk, false},
		{"allow rule covering every file allows", `{"permissions":{"allow":["Edit(//w/p/**)"]}}`,
			multi("/w/p/a.go", "/w/p/b.go"), DecisionAllow, false},
		{"allow rule covering only some files asks", `{"permissions":{"allow":["Edit(//w/p/**)"]}}`,
			multi("/w/p/a.go", "/w/q/b.go"), DecisionAsk, true},
		{"no files falls back to the whole call", `{"permissions":{"deny":["MultiEdit"]}}`,
			map[string]any{}, DecisionDeny, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg, err := ParseBytes([]byte(c.perms))
			if err != nil {
				t.Fatal(err)
			}
			v := cfg.CheckArgsVerdict("MultiEdit", c.args, "/w/p")
			if v.Decision != c.want {
				t.Fatalf("decision = %v (%s), want %v", v.Decision, v.Reason, c.want)
			}
			if v.DefaultAsk != c.defAsk {
				t.Fatalf("DefaultAsk = %v, want %v", v.DefaultAsk, c.defAsk)
			}
		})
	}
}

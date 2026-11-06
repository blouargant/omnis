package tools

import (
	"reflect"
	"testing"
)

func TestRepairToolArgs(t *testing.T) {
	cases := []struct {
		name string
		tool string
		in   map[string]any
		want map[string]any
	}{
		{
			name: "Edit path and camelCase aliases",
			tool: "Edit",
			in:   map[string]any{"path": "a.go", "oldText": "x", "newText": "y"},
			want: map[string]any{"file_path": "a.go", "old_string": "x", "new_string": "y"},
		},
		{
			name: "Edit with a one-element edits list",
			tool: "Edit",
			in:   map[string]any{"file_path": "a.go", "edits": []any{map[string]any{"oldText": "x", "newText": "y"}}},
			want: map[string]any{"file_path": "a.go", "old_string": "x", "new_string": "y"},
		},
		{
			name: "Edit with several edits becomes nothing it cannot express",
			tool: "Edit",
			in:   map[string]any{"file_path": "a.go", "edits": []any{map[string]any{"old_string": "x", "new_string": "y"}, map[string]any{"old_string": "z", "new_string": "w"}}},
			want: map[string]any{"file_path": "a.go", "edits": []any{map[string]any{"old_string": "x", "new_string": "y"}, map[string]any{"old_string": "z", "new_string": "w"}}},
		},
		{
			name: "canonical keys are never overwritten",
			tool: "Edit",
			in:   map[string]any{"file_path": "a.go", "path": "b.go", "old_string": "x", "new_string": "y"},
			want: map[string]any{"file_path": "a.go", "path": "b.go", "old_string": "x", "new_string": "y"},
		},
		{
			name: "MultiEdit files sent as a JSON string",
			tool: "MultiEdit",
			in:   map[string]any{"files": `[{"file_path":"a.go","edits":[{"old_string":"x","new_string":"y"}]}]`},
			want: map[string]any{"files": []any{map[string]any{"file_path": "a.go", "edits": []any{map[string]any{"old_string": "x", "new_string": "y"}}}}},
		},
		{
			name: "MultiEdit top-level file_path + edits string",
			tool: "MultiEdit",
			in:   map[string]any{"file_path": "a.go", "edits": `{"oldText":"x","newText":"y"}`},
			want: map[string]any{"files": []any{map[string]any{"file_path": "a.go", "edits": []any{map[string]any{"old_string": "x", "new_string": "y"}}}}},
		},
		{
			name: "MultiEdit a single file object",
			tool: "MultiEdit",
			in:   map[string]any{"files": map[string]any{"path": "a.go", "edits": []any{map[string]any{"old_string": "x", "new_string": "y"}}}},
			want: map[string]any{"files": []any{map[string]any{"file_path": "a.go", "edits": []any{map[string]any{"old_string": "x", "new_string": "y"}}}}},
		},
		{
			name: "Read offset/limit",
			tool: "Read",
			in:   map[string]any{"path": "a.go", "offset": float64(10), "limit": float64(5)},
			want: map[string]any{"file_path": "a.go", "start_line": float64(10), "end_line": float64(14)},
		},
		{
			name: "Write path alias",
			tool: "Write",
			in:   map[string]any{"path": "a.go", "content": "z"},
			want: map[string]any{"file_path": "a.go", "content": "z"},
		},
		{
			name: "other tools are untouched",
			tool: "Bash",
			in:   map[string]any{"path": "x", "cmd": "ls"},
			want: map[string]any{"path": "x", "cmd": "ls"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			RepairToolArgs(c.tool, c.in)
			if !reflect.DeepEqual(c.in, c.want) {
				t.Fatalf("got %#v\nwant %#v", c.in, c.want)
			}
		})
	}
}

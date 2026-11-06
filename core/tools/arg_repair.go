package tools

import (
	"encoding/json"
	"strings"
)

// RepairToolArgs rewrites, in place, the argument shapes models commonly send
// to the file tools instead of the declared schema: a `path` for `file_path`,
// camelCase or `*_text` names for `old_string`/`new_string`, an `edits` list
// handed to Edit, `files`/`edits` sent as a JSON string or as a single object
// instead of an array, Read's `offset`/`limit`. Without this the schema check
// rejects the call and a weak model often never recovers.
//
// It is meant to run on the model's function call BEFORE any callback sees it
// (see llm.RepairToolCalls): the permission gate reads `file_path`, so an
// alias repaired only inside the tool would let a path rule be bypassed.
// A canonical key already present always wins; other tools are untouched.
func RepairToolArgs(name string, args map[string]any) {
	if args == nil {
		return
	}
	switch name {
	case "Read":
		renameKey(args, "file_path", "path", "filePath", "file")
		if _, ok := args["start_line"]; !ok {
			if v, ok := args["offset"]; ok {
				args["start_line"] = v
				delete(args, "offset")
			}
		}
		if _, ok := args["end_line"]; !ok {
			lim, lok := args["limit"].(float64)
			start, sok := args["start_line"].(float64)
			if lok && lim > 0 {
				if !sok || start < 1 {
					start = 1
				}
				args["end_line"] = start + lim - 1
				delete(args, "limit")
			}
		}
	case "Write":
		renameKey(args, "file_path", "path", "filePath", "file")
	case "Edit":
		renameKey(args, "file_path", "path", "filePath", "file")
		renameEditKeys(args)
		if _, ok := args["old_string"]; !ok {
			if edits := asList(args["edits"]); len(edits) == 1 {
				if e, ok := edits[0].(map[string]any); ok {
					renameEditKeys(e)
					for k, v := range e {
						if _, taken := args[k]; !taken {
							args[k] = v
						}
					}
					delete(args, "edits")
				}
			}
		}
	case "MultiEdit":
		if _, ok := args["files"]; !ok {
			if _, ok := args["edits"]; ok {
				file := map[string]any{"edits": args["edits"]}
				for _, k := range []string{"file_path", "path", "filePath", "file"} {
					if v, ok := args[k]; ok {
						file[k] = v
						delete(args, k)
					}
				}
				delete(args, "edits")
				args["files"] = []any{file}
			}
		}
		files := asList(args["files"])
		if files == nil {
			return
		}
		for _, f := range files {
			fm, ok := f.(map[string]any)
			if !ok {
				continue
			}
			renameKey(fm, "file_path", "path", "filePath", "file")
			edits := asList(fm["edits"])
			if edits == nil {
				continue
			}
			for _, e := range edits {
				if em, ok := e.(map[string]any); ok {
					renameEditKeys(em)
				}
			}
			fm["edits"] = edits
		}
		args["files"] = files
	}
}

func renameEditKeys(m map[string]any) {
	renameKey(m, "old_string", "oldText", "old_text", "oldString", "old")
	renameKey(m, "new_string", "newText", "new_text", "newString", "new")
}

// renameKey moves the first present alias to canonical, unless canonical is
// already set (then the aliases are left alone and the schema decides).
func renameKey(m map[string]any, canonical string, aliases ...string) {
	if _, ok := m[canonical]; ok {
		return
	}
	for _, a := range aliases {
		if v, ok := m[a]; ok {
			m[canonical] = v
			delete(m, a)
			return
		}
	}
}

// asList normalises v to a JSON array: a JSON-encoded string is decoded, a
// single object becomes a one-element list. Anything else returns nil.
func asList(v any) []any {
	switch x := v.(type) {
	case []any:
		return x
	case map[string]any:
		return []any{x}
	case string:
		s := strings.TrimSpace(x)
		if s == "" || (s[0] != '[' && s[0] != '{') {
			return nil
		}
		var decoded any
		if json.Unmarshal([]byte(s), &decoded) != nil {
			return nil
		}
		return asList(decoded)
	}
	return nil
}

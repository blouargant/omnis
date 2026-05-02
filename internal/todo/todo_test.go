package todo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewStoreUsesDefaultPathWhenEmpty(t *testing.T) {
	t.Parallel()

	store := NewStore("")
	if store.defaultPath != DefaultPath {
		t.Fatalf("defaultPath = %q, want %q", store.defaultPath, DefaultPath)
	}
}

func TestStoreWriteReadAndUpdateAt(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "todo.json")
	store := NewStore(path)

	msg, err := store.WriteAt(path, []string{"inspect repo", "write tests"})
	if err != nil {
		t.Fatalf("WriteAt() error = %v", err)
	}
	if !strings.Contains(msg, "[0] inspect repo") || !strings.Contains(msg, "[1] write tests") {
		t.Fatalf("WriteAt() message = %q", msg)
	}

	plan, err := store.ReadAt(path)
	if err != nil {
		t.Fatalf("ReadAt() error = %v", err)
	}
	if !strings.Contains(plan, "[pending") || !strings.Contains(plan, "inspect repo") {
		t.Fatalf("ReadAt() = %q, want pending tasks", plan)
	}

	updated, err := store.UpdateAt(path, 1, StatusDone)
	if err != nil {
		t.Fatalf("UpdateAt() error = %v", err)
	}
	if updated != "Task 1 marked done" {
		t.Fatalf("UpdateAt() = %q", updated)
	}

	plan, err = store.ReadAt(path)
	if err != nil {
		t.Fatalf("ReadAt() after update error = %v", err)
	}
	if !strings.Contains(plan, "[done") || !strings.Contains(plan, "write tests") {
		t.Fatalf("ReadAt() after update = %q, want updated task", plan)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !strings.Contains(string(data), "\"status\": \"done\"") {
		t.Fatalf("saved file = %s, want persisted done status", string(data))
	}
	if !strings.Contains(string(data), "\"id\": 0") || !strings.Contains(string(data), "\"id\": 1") {
		t.Fatalf("saved file = %s, want sequential ids", string(data))
	}
	if !strings.Contains(string(data), "\"task\": \"inspect repo\"") {
		t.Fatalf("saved file = %s, want first task persisted", string(data))
	}
	if !strings.Contains(string(data), "\"task\": \"write tests\"") {
		t.Fatalf("saved file = %s, want second task persisted", string(data))
	}
	if !strings.Contains(string(data), "\"status\": \"pending\"") {
		t.Fatalf("saved file = %s, want untouched task to remain pending", string(data))
	}
	if !strings.Contains(string(data), "\"status\": \"done\"") {
		t.Fatalf("saved file = %s, want updated task to be done", string(data))
	}
	if strings.Count(string(data), "\"status\": \"pending\"") != 1 {
		t.Fatalf("saved file = %s, want exactly one pending task", string(data))
	}
	if strings.Count(string(data), "\"status\": \"done\"") != 1 {
		t.Fatalf("saved file = %s, want exactly one done task", string(data))
	}
	if strings.Count(string(data), "\"id\":") != 2 {
		t.Fatalf("saved file = %s, want exactly two tasks", string(data))
	}
	if strings.Count(string(data), "\"task\":") != 2 {
		t.Fatalf("saved file = %s, want exactly two task entries", string(data))
	}
	if strings.Count(string(data), "\"status\":") != 2 {
		t.Fatalf("saved file = %s, want exactly two status entries", string(data))
	}
	if strings.Index(string(data), "inspect repo") > strings.Index(string(data), "write tests") {
		t.Fatalf("saved file = %s, want tasks to preserve order", string(data))
	}
	if strings.Index(plan, "inspect repo") > strings.Index(plan, "write tests") {
		t.Fatalf("plan = %q, want tasks to preserve order", plan)
	}
	if strings.Count(plan, "[") < 4 {
		t.Fatalf("plan = %q, want formatted task rows", plan)
	}
	if !strings.HasSuffix(strings.TrimSpace(plan), "write tests") {
		t.Fatalf("plan = %q, want second task last", plan)
	}
	if !strings.HasPrefix(msg, "Plan written:") {
		t.Fatalf("WriteAt() message = %q, want plan header", msg)
	}
	if !strings.Contains(plan, "[pending     ] inspect repo") {
		t.Fatalf("plan = %q, want aligned pending row", plan)
	}
	if !strings.Contains(plan, "[done        ] write tests") {
		t.Fatalf("plan = %q, want aligned done row", plan)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if store.lockFor(path) != store.lockFor(path) {
		t.Fatal("lockFor() should return the same mutex for the same path")
	}
	if store.lockFor(filepath.Join(dir, "other.json")) == store.lockFor(path) {
		t.Fatal("lockFor() should create distinct mutexes for different paths")
	}
	emptyStore := NewStore(filepath.Join(dir, "empty.json"))
	if got, err := emptyStore.Read(); err != nil || got != "(no plan)" {
		t.Fatalf("Read() on untouched store = (%q, %v), want no plan", got, err)
	}
}

func TestStoreReadAtMissingFileAndOutOfRangeUpdate(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "missing.json")
	store := NewStore(path)

	plan, err := store.ReadAt(path)
	if err != nil {
		t.Fatalf("ReadAt() error = %v", err)
	}
	if plan != "(no plan)" {
		t.Fatalf("ReadAt() = %q, want no plan", plan)
	}

	msg, err := store.UpdateAt(path, 5, StatusDone)
	if err != nil {
		t.Fatalf("UpdateAt() error = %v", err)
	}
	if msg != "Error: index 5 out of range" {
		t.Fatalf("UpdateAt() = %q", msg)
	}
}
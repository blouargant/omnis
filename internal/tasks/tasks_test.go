package tasks

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestNewUsesDefaultPathWhenEmpty(t *testing.T) {
	t.Parallel()

	g := New("")
	if g.defaultPath != DefaultPath {
		t.Fatalf("defaultPath = %q, want %q", g.defaultPath, DefaultPath)
	}
}

func TestGraphCreateListAndClaimNextAt(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "tasks.json")
	g := New(path)

	blockedID, err := g.CreateAt(path, "blocked", []string{"missing-dep"}, PriorityHigh)
	if err != nil {
		t.Fatalf("CreateAt(blocked) error = %v", err)
	}
	rootID, err := g.CreateAt(path, "root", nil, PriorityMedium)
	if err != nil {
		t.Fatalf("CreateAt(root) error = %v", err)
	}
	if rootID == "" || blockedID == "" || rootID == blockedID {
		t.Fatalf("unexpected ids: root=%q blocked=%q", rootID, blockedID)
	}
	leafID, err := g.CreateAt(path, "leaf", []string{rootID}, PriorityLow)
	if err != nil {
		t.Fatalf("CreateAt(leaf) error = %v", err)
	}
	if leafID == "" {
		t.Fatal("CreateAt(leaf) returned empty id")
	}

	list, err := g.ListAt(path)
	if err != nil {
		t.Fatalf("ListAt() error = %v", err)
	}
	if !strings.Contains(list, "blocked") || !strings.Contains(list, "root") || !strings.Contains(list, "leaf") {
		t.Fatalf("ListAt() = %q, want all tasks", list)
	}

	claimed, err := g.ClaimNextAt(path, "agent-a")
	if err != nil {
		t.Fatalf("ClaimNextAt() error = %v", err)
	}
	if claimed == nil {
		t.Fatal("ClaimNextAt() returned nil, want root task")
	}
	if claimed.ID != rootID || claimed.ClaimedBy != "agent-a" || claimed.Status != StatusInProgress {
		t.Fatalf("ClaimNextAt() = %+v, want root claimed by agent-a", claimed)
	}

	next, err := g.ClaimNextAt(path, "agent-b")
	if err != nil {
		t.Fatalf("ClaimNextAt(second) error = %v", err)
	}
	if next != nil {
		t.Fatalf("ClaimNextAt(second) = %+v, want nil because remaining tasks are blocked", next)
	}
}

func TestGraphListAtEmpty(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "tasks.json")
	g := New(path)

	list, err := g.ListAt(path)
	if err != nil {
		t.Fatalf("ListAt() error = %v", err)
	}
	if list != "(no tasks)" {
		t.Fatalf("ListAt() = %q, want no tasks", list)
	}
}

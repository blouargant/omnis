package sessions

import "testing"

func TestNewForAndListFor(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	r := NewEmptyRegistry()
	a := r.NewFor("alice", "system")
	b := r.NewFor("bob", "system")
	if a.UserID != "alice" || b.UserID != "bob" {
		t.Fatalf("owners: %q %q", a.UserID, b.UserID)
	}
	if got := r.ListFor("alice"); len(got) != 1 || got[0].ID != a.ID {
		t.Fatalf("ListFor(alice) = %v", got)
	}
	if got := r.ListFor(""); len(got) != 2 {
		t.Fatalf("ListFor(\"\") must list everything, got %d", len(got))
	}
	if m := r.NewFor("", "system"); m.UserID != UserID() {
		t.Fatalf("empty owner must fall back to UserID(), got %q", m.UserID)
	}
}

func TestSetConversationOwnerOverridesStamp(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	_ = SetConversationSquad("s1", "system") // stamps the process default
	if err := SetConversationOwner("s1", "alice"); err != nil {
		t.Fatal(err)
	}
	f, err := LoadConversationFile("s1")
	if err != nil || f.UserID != "alice" {
		t.Fatalf("user_id=%q err=%v", f.UserID, err)
	}
}

package sessions

import (
	"testing"
)

func TestNormalizeCollectionName(t *testing.T) {
	cases := map[string]string{
		"":          "",
		"  ":        "",
		"General":   "",
		"general":   "",
		" GENERAL ": "",
		"Work":      "Work",
		"  Work  ":  "Work",
	}
	for in, want := range cases {
		if got := NormalizeCollectionName(in); got != want {
			t.Errorf("NormalizeCollectionName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestValidCollectionName(t *testing.T) {
	valid := []string{"Work", "Client A", "2026 Q3", "déjà"}
	for _, n := range valid {
		if !ValidCollectionName(n) {
			t.Errorf("ValidCollectionName(%q) = false, want true", n)
		}
	}
	invalid := []string{"", "   ", "General", "general", "a/b", "a\\b", "x\ny"}
	for _, n := range invalid {
		if ValidCollectionName(n) {
			t.Errorf("ValidCollectionName(%q) = true, want false", n)
		}
	}
	// Over the length cap.
	long := ""
	for i := 0; i <= MaxCollectionNameLen; i++ {
		long += "x"
	}
	if ValidCollectionName(long) {
		t.Errorf("ValidCollectionName(<%d chars>) = true, want false", len(long))
	}
}

func TestCollectionsCRUD(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())

	// Empty to start.
	if got, err := ListCollections(); err != nil || len(got) != 0 {
		t.Fatalf("ListCollections empty: got %v err %v", got, err)
	}

	// Add two, in order.
	if _, added, err := AddCollection("Work"); err != nil || !added {
		t.Fatalf("AddCollection Work: added=%v err=%v", added, err)
	}
	if _, added, err := AddCollection("Personal"); err != nil || !added {
		t.Fatalf("AddCollection Personal: added=%v err=%v", added, err)
	}
	got, err := ListCollections()
	if err != nil {
		t.Fatalf("ListCollections: %v", err)
	}
	if len(got) != 2 || got[0] != "Work" || got[1] != "Personal" {
		t.Fatalf("order not preserved: %v", got)
	}

	// Duplicate (case-insensitive) is idempotent, not an error.
	if _, added, err := AddCollection("work"); err != nil || added {
		t.Fatalf("duplicate add: added=%v err=%v", added, err)
	}
	if got, _ := ListCollections(); len(got) != 2 {
		t.Fatalf("duplicate changed the list: %v", got)
	}

	// Reserved / invalid names rejected.
	if _, _, err := AddCollection("General"); err == nil {
		t.Fatalf("expected error adding reserved 'General'")
	}

	// Rename Work → Job, position preserved.
	if _, ok, err := RenameCollection("Work", "Job"); err != nil || !ok {
		t.Fatalf("RenameCollection: ok=%v err=%v", ok, err)
	}
	got, _ = ListCollections()
	if got[0] != "Job" || got[1] != "Personal" {
		t.Fatalf("rename lost order: %v", got)
	}

	// Rename onto an existing different name is rejected.
	if _, _, err := RenameCollection("Job", "Personal"); err == nil {
		t.Fatalf("expected collision error renaming onto existing")
	}

	// Rename a missing collection reports not-found (ok=false), no error.
	if _, ok, err := RenameCollection("Nope", "Whatever"); err != nil || ok {
		t.Fatalf("rename missing: ok=%v err=%v", ok, err)
	}

	// Remove one.
	if _, ok, err := RemoveCollection("Personal"); err != nil || !ok {
		t.Fatalf("RemoveCollection: ok=%v err=%v", ok, err)
	}
	got, _ = ListCollections()
	if len(got) != 1 || got[0] != "Job" {
		t.Fatalf("after remove: %v", got)
	}

	// Remove missing is a no-op (ok=false).
	if _, ok, _ := RemoveCollection("Personal"); ok {
		t.Fatalf("removing absent collection reported ok=true")
	}
}

func TestValidCollectionColor(t *testing.T) {
	valid := []string{"", "blue", "amber", "deep-teal", "gray12"}
	for _, c := range valid {
		if !ValidCollectionColor(c) {
			t.Errorf("ValidCollectionColor(%q) = false, want true", c)
		}
	}
	invalid := []string{"Blue", "#3b82f6", "a b", "under_score", "way-too-long-a-color-token-value"}
	for _, c := range invalid {
		if ValidCollectionColor(c) {
			t.Errorf("ValidCollectionColor(%q) = true, want false", c)
		}
	}
}

func TestCollectionColors(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())

	if _, _, err := AddCollection("Work"); err != nil {
		t.Fatalf("AddCollection: %v", err)
	}
	// Colouring a missing collection is an error.
	if err := SetCollectionColor("Nope", "blue"); err == nil {
		t.Fatalf("expected error colouring a missing collection")
	}
	// An invalid colour is rejected.
	if err := SetCollectionColor("Work", "Blue"); err == nil {
		t.Fatalf("expected error for invalid colour token")
	}
	// Set + read back (case-insensitive name lookup, canonical key stored).
	if err := SetCollectionColor("work", "blue"); err != nil {
		t.Fatalf("SetCollectionColor: %v", err)
	}
	colors, err := CollectionColors()
	if err != nil || colors["Work"] != "blue" {
		t.Fatalf("CollectionColors: %v err=%v", colors, err)
	}
	// The name list must survive the colour write (regression: save dropped names).
	if got, _ := ListCollections(); len(got) != 1 || got[0] != "Work" {
		t.Fatalf("colour write clobbered the name list: %v", got)
	}

	// Rename migrates the colour to the new name.
	if _, ok, err := RenameCollection("Work", "Job"); err != nil || !ok {
		t.Fatalf("RenameCollection: ok=%v err=%v", ok, err)
	}
	colors, _ = CollectionColors()
	if colors["Job"] != "blue" || colors["Work"] != "" {
		t.Fatalf("rename did not migrate colour: %v", colors)
	}

	// Clearing removes the entry.
	if err := SetCollectionColor("Job", ""); err != nil {
		t.Fatalf("clear colour: %v", err)
	}
	if colors, _ := CollectionColors(); colors["Job"] != "" {
		t.Fatalf("colour not cleared: %v", colors)
	}

	// Remove drops any colour with the collection.
	if err := SetCollectionColor("Job", "green"); err != nil {
		t.Fatalf("re-set colour: %v", err)
	}
	if _, ok, err := RemoveCollection("Job"); err != nil || !ok {
		t.Fatalf("RemoveCollection: ok=%v err=%v", ok, err)
	}
	if colors, _ := CollectionColors(); colors["Job"] != "" {
		t.Fatalf("remove left an orphan colour: %v", colors)
	}
}

func TestSetConversationCollectionPersists(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	id := "teaching-kite"
	if err := AppendConversationTurn(id, "hi", "hello"); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := SetConversationCollection(id, "Work"); err != nil {
		t.Fatalf("SetConversationCollection: %v", err)
	}
	f, err := LoadConversationFile(id)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if f.Collection != "Work" {
		t.Fatalf("Collection = %q, want Work", f.Collection)
	}
	// LoadPersistedSessions surfaces it on the meta.
	metas := LoadPersistedSessions()
	if len(metas) != 1 || metas[0].Collection != "Work" {
		t.Fatalf("persisted meta collection: %+v", metas)
	}
}

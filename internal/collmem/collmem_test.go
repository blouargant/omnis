package collmem

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestSaveCreatesThenUpdatesInPlace(t *testing.T) {
	s := In(t.TempDir())
	m := Memory{Name: "no-force-push", Description: "Never force-push", Type: TypeFeedback, Body: "Rebase instead."}
	got, created, err := s.Save("Proxy", m)
	if err != nil || !created || got.Updated == "" {
		t.Fatalf("first save: %v %v %+v", err, created, got)
	}
	m.Body = "Rebase, then ask."
	_, created, err = s.Save("Proxy", m)
	if err != nil || created {
		t.Fatalf("second save must update: %v %v", err, created)
	}
	if l := s.List("Proxy"); len(l) != 1 || l[0].Body != "Rebase, then ask." {
		t.Fatalf("list = %+v", l)
	}
	if _, err := os.Stat(filepath.Join(s.Dir("Proxy"), "no-force-push.md")); err != nil {
		t.Fatal(err)
	}
}

func TestGeneralLivesOutsideCollections(t *testing.T) {
	root := t.TempDir()
	s := In(root)
	if got, want := s.Dir(""), filepath.Join(root, "general-memory"); got != want {
		t.Fatalf("Dir(\"\") = %q, want %q", got, want)
	}
	if got, want := s.Dir("Proxy"), filepath.Join(root, "collections", "Proxy", "memory"); got != want {
		t.Fatalf("got %q", got)
	}
	for _, bad := range []string{"..", ".", "a/b", `a\b`} {
		if s.Dir(bad) != "" {
			t.Fatalf("Dir(%q) must be empty", bad)
		}
	}
}

func TestNormalizeName(t *testing.T) {
	ok := map[string]string{"No Force Push": "no-force-push", "notes.md": "notes", "a_b": "a-b", "x": "x"}
	for in, want := range ok {
		if got, valid := NormalizeName(in); !valid || got != want {
			t.Errorf("%q → %q,%v want %q", in, got, valid, want)
		}
	}
	for _, bad := range []string{"", "../x", "a/b", "-lead", strings.Repeat("a", 65), "é"} {
		if _, valid := NormalizeName(bad); valid {
			t.Errorf("%q must be invalid", bad)
		}
	}
}

func TestSaveRejects(t *testing.T) {
	s := In(t.TempDir())
	base := Memory{Name: "a", Description: "d", Type: TypeUser, Body: "b"}
	cases := []struct {
		mut  func(*Memory)
		want error
	}{
		{func(m *Memory) { m.Name = "../x" }, ErrInvalidName},
		{func(m *Memory) { m.Type = "note" }, ErrInvalidType},
		{func(m *Memory) { m.Body = strings.Repeat("x", MaxBodyChars+1) }, ErrTooLarge},
	}
	for _, c := range cases {
		m := base
		c.mut(&m)
		if _, _, err := s.Save("", m); !errors.Is(err, c.want) {
			t.Errorf("got %v want %v", err, c.want)
		}
	}
	if _, _, err := s.Save("..", base); !errors.Is(err, ErrCollection) {
		t.Errorf("collection: %v", err)
	}
}

func TestSaveRefusesBeyondMaxButStillUpdates(t *testing.T) {
	s := In(t.TempDir())
	for i := 0; i < MaxMemories; i++ {
		if _, _, err := s.Save("", Memory{Name: fmt.Sprintf("m%d", i), Description: "d", Type: TypeProject, Body: "b"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := s.Save("", Memory{Name: "one-more", Description: "d", Type: TypeProject, Body: "b"}); !errors.Is(err, ErrFull) {
		t.Fatalf("got %v", err)
	}
	if _, created, err := s.Save("", Memory{Name: "m0", Description: "d2", Type: TypeProject, Body: "b"}); err != nil || created {
		t.Fatalf("update at the cap must work: %v", err)
	}
}

func TestConcurrentSavesAllLand(t *testing.T) {
	s := In(t.TempDir())
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _, err := s.Save("", Memory{Name: fmt.Sprintf("m%d", i%5), Description: "d", Type: TypeFeedback, Body: fmt.Sprintf("body %d", i)})
			if err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	l := s.List("")
	if len(l) != 5 {
		t.Fatalf("want 5 memories, got %d", len(l))
	}
	for _, m := range l {
		if !strings.HasPrefix(m.Body, "body ") {
			t.Fatalf("torn file: %+v", m)
		}
	}
}

func TestListSkipsBrokenFilesAndKeepsDashesInBody(t *testing.T) {
	s := In(t.TempDir())
	s.Save("", Memory{Name: "good", Description: "d", Type: TypeFeedback, Body: "line\n---\nafter"})
	dir := s.Dir("")
	os.WriteFile(filepath.Join(dir, "no-front-matter.md"), []byte("just text"), 0o644)
	os.WriteFile(filepath.Join(dir, "bad-type.md"), []byte("---\nname: bad-type\ndescription: d\ntype: nope\n---\nx"), 0o644)
	os.WriteFile(filepath.Join(dir, "README.txt"), []byte("x"), 0o644)
	l := s.List("")
	if len(l) != 1 || l[0].Name != "good" || l[0].Body != "line\n---\nafter" {
		t.Fatalf("list = %+v", l)
	}
}

func TestListOrderAndDelete(t *testing.T) {
	s := In(t.TempDir())
	for _, m := range []Memory{{Name: "z", Type: TypeReference}, {Name: "b", Type: TypeFeedback}, {Name: "a", Type: TypeFeedback}, {Name: "u", Type: TypeUser}, {Name: "p", Type: TypeProject}} {
		m.Description, m.Body = "d", "b"
		s.Save("", m)
	}
	var names []string
	for _, m := range s.List("") {
		names = append(names, m.Name)
	}
	if got := strings.Join(names, ","); got != "a,b,u,p,z" {
		t.Fatalf("order = %s", got)
	}
	if ok, err := s.Delete("", "a"); !ok || err != nil {
		t.Fatal(ok, err)
	}
	if ok, _ := s.Delete("", "a"); ok {
		t.Fatal("second delete must report false")
	}
	if _, ok := s.Get("", "a"); ok {
		t.Fatal("still there")
	}
}

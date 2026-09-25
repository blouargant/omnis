package sessions

import "testing"

// IsArchived reads the flag under the registry lock, so it is race-free
// against a concurrent SetArchived (run with -race).
func TestIsArchivedConcurrentWithSetArchived(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	r := NewRegistry()
	m := r.New("")
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			r.SetArchived(m.ID, i%2 == 1)
		}
	}()
	for i := 0; i < 200; i++ {
		if _, ok := r.IsArchived(m.ID); !ok {
			t.Error("known session reported unknown")
			break
		}
	}
	<-done
	if a, ok := r.IsArchived(m.ID); !ok || !a {
		t.Fatalf("final state: archived=%v ok=%v, want true/true", a, ok)
	}
	if _, ok := r.IsArchived("nope"); ok {
		t.Fatal("unknown session must report ok=false")
	}
}

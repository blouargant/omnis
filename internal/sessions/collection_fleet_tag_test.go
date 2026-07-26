package sessions

import "testing"

func TestCollectionProfileFleetTagRoundTrips(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())

	if _, _, err := AddCollection("api"); err != nil {
		t.Fatalf("AddCollection: %v", err)
	}
	if err := UpdateCollectionProfile("api", func(p *CollectionProfileData) {
		p.Role = "project"
		p.Engine = "omnis"
		p.Fleet = "Payments"
	}); err != nil {
		t.Fatalf("UpdateCollectionProfile: %v", err)
	}
	got := CollectionProfileFull("api")
	if got.Fleet != "Payments" {
		t.Fatalf("Fleet = %q, want %q", got.Fleet, "Payments")
	}

	// A profile carrying ONLY a fleet tag must persist (isEmpty must count Fleet),
	// otherwise saveFileLocked prunes it and the tag is lost.
	if _, _, err := AddCollection("bare"); err != nil {
		t.Fatalf("AddCollection bare: %v", err)
	}
	if err := UpdateCollectionProfile("bare", func(p *CollectionProfileData) {
		p.Fleet = "X"
	}); err != nil {
		t.Fatalf("UpdateCollectionProfile bare: %v", err)
	}
	if got := CollectionProfileFull("bare").Fleet; got != "X" {
		t.Fatalf("bare Fleet = %q, want %q (profile with only Fleet was dropped)", got, "X")
	}
}

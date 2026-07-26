package sessions

import "testing"

func TestFleetRegistryCoreRoundTrips(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())

	// Empty to start.
	if got, err := ListFleets(); err != nil || len(got) != 0 {
		t.Fatalf("ListFleets empty: got %v err %v", got, err)
	}

	// Add with metadata.
	names, added, err := AddFleet("Payments", FleetMetaData{Color: "blue", DefaultEngine: "omnis"})
	if err != nil || !added || len(names) != 1 || names[0] != "Payments" {
		t.Fatalf("AddFleet: names=%v added=%v err=%v", names, added, err)
	}
	// Idempotent re-add.
	if _, added, _ := AddFleet("Payments", FleetMetaData{}); added {
		t.Fatalf("AddFleet twice reported added=true")
	}
	// Metadata round-trips.
	if m := FleetMetaFor("Payments"); m.Color != "blue" || m.DefaultEngine != "omnis" {
		t.Fatalf("FleetMetaFor = %+v", m)
	}
	// Partial update preserves the untouched field.
	if err := UpdateFleetMeta("Payments", func(m *FleetMetaData) { m.Description = "billing rails" }); err != nil {
		t.Fatalf("UpdateFleetMeta: %v", err)
	}
	if m := FleetMetaFor("Payments"); m.Description != "billing rails" || m.Color != "blue" {
		t.Fatalf("after partial update: %+v", m)
	}

	// Validation.
	for _, bad := range []string{"", "General", "Ungrouped", "a/b"} {
		if ValidFleetName(bad) {
			t.Fatalf("ValidFleetName(%q) = true, want false", bad)
		}
	}
	if !ValidFleetName("Billing") {
		t.Fatalf("ValidFleetName(Billing) = false")
	}
	if _, _, err := AddFleet("Ungrouped", FleetMetaData{}); err == nil {
		t.Fatalf("AddFleet(Ungrouped) should error")
	}
	if !ValidDefaultEngine("") || !ValidDefaultEngine("omnis") || !ValidDefaultEngine("claude") || ValidDefaultEngine("gpt") {
		t.Fatalf("ValidDefaultEngine wrong")
	}
}

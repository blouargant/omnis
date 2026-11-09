package usercommands

import "testing"

// The /memory command is built into the web UI; a user command named
// "memory" would shadow it.
func TestMemoryIsReserved(t *testing.T) {
	if _, ok := ReservedNames["memory"]; !ok {
		t.Fatal(`"memory" is not in ReservedNames`)
	}
	c := Command{Name: "memory", Prompt: "x"}
	if err := Validate(&c); err == nil {
		t.Fatal(`Validate accepted a user command named "memory"`)
	}
}

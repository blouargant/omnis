package tui

import "testing"

func TestStripTerminalControlSequences(t *testing.T) {
	in := "hello \x1b[31mred\x1b[0m world \x1b]11;rgb:1c1c/1c1c/1f1f\x07 done"
	got := stripTerminalControlSequences(in)
	want := "hello red world  done"
	if got != want {
		t.Fatalf("stripTerminalControlSequences() = %q, want %q", got, want)
	}
}

func TestSanitizeInputText_RemovesOSCColorArtifacts(t *testing.T) {
	in := "11;rgb:1c1c/1c1c/1f1f what are the pods"
	got := sanitizeInputText(in)
	want := "what are the pods"
	if got != want {
		t.Fatalf("sanitizeInputText() = %q, want %q", got, want)
	}
}

func TestSanitizeInputText_KeepsRegularText(t *testing.T) {
	in := "list pods in test-system"
	got := sanitizeInputText(in)
	if got != in {
		t.Fatalf("sanitizeInputText() changed valid input: got %q want %q", got, in)
	}
}

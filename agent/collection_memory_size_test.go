package agent

import (
	"strings"
	"testing"
)

func TestSizeWordLimit(t *testing.T) {
	cases := map[string]int{"small": 200, "medium": 350, "large": 700, "": 350, "bogus": 350, "LARGE": 700}
	for in, want := range cases {
		if got := SizeWordLimit(in); got != want {
			t.Errorf("SizeWordLimit(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestBuildDistillRequestInjectsWordTarget(t *testing.T) {
	req := buildDistillRequest("prior facts", "## Session: s\nUser: hi\nAssistant: yo\n", 200)
	if req == nil || len(req.Contents) == 0 || len(req.Contents[0].Parts) == 0 {
		t.Fatal("nil/empty request")
	}
	body := req.Contents[0].Parts[0].Text
	if !strings.Contains(body, "200 words") {
		t.Fatalf("word target missing from body:\n%s", body)
	}
	if !strings.Contains(body, "prior facts") {
		t.Fatal("current memory missing from body")
	}
}

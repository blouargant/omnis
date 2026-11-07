package agent

import (
	"strings"
	"testing"
	"time"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

func TestTimeNoteThresholds(t *testing.T) {
	limit := 30 * time.Minute
	if n := timeNote(20*time.Minute, limit); n != "" {
		t.Fatalf("before half: %q", n)
	}
	if n := timeNote(12*time.Minute, limit); !strings.Contains(n, "12 min left") || !strings.Contains(n, "30 min limit") || strings.Contains(n, "Finish now") {
		t.Fatalf("past half: %q", n)
	}
	if n := timeNote(4*time.Minute, limit); !strings.Contains(n, "Finish now") || !strings.Contains(n, "no new review") {
		t.Fatalf("last fifth: %q", n)
	}
	if n := timeNote(-time.Minute, limit); !strings.Contains(n, "past its time limit") {
		t.Fatalf("overrun: %q", n)
	}
	if n := timeNote(5*time.Second, 20*time.Second); !strings.Contains(n, "5 s left") || !strings.Contains(n, "20 s limit") {
		t.Fatalf("short limit: %q", n)
	}
	if timeNote(time.Minute, 0) != "" {
		t.Fatal("no limit, no note")
	}
}

func TestAppendUserNoteNeverDoublesUserTurns(t *testing.T) {
	req := &model.LLMRequest{Contents: []*genai.Content{{Role: "model", Parts: []*genai.Part{{Text: "x"}}}}}
	appendUserNote(req, "[Time: …]")
	if len(req.Contents) != 2 || req.Contents[1].Role != "user" {
		t.Fatalf("after a model turn a user turn is added: %+v", req.Contents)
	}
	appendUserNote(req, "[again]")
	if len(req.Contents) != 2 || len(req.Contents[1].Parts) != 2 {
		t.Fatal("a trailing user turn must be extended, not followed by another")
	}
}

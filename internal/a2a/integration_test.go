//go:build integration

// Live round-trip against a real A2A endpoint. Skipped unless the
// `integration` build tag is set:
//
//	OMNIS_A2A_TEST_URL=http://127.0.0.1:8091/ \
//	  go test -tags integration -v -run TestLive ./internal/a2a
//
// Optional env vars:
//
//	OMNIS_A2A_TEST_URL     endpoint base URL (required)
//	OMNIS_A2A_TEST_TOKEN   Bearer token sent in the Authorization header
//	OMNIS_A2A_TEST_PROMPT  prompt text (default "reply with the literal token PONG")

package a2a

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestLiveSendTaskRoundTrip(t *testing.T) {
	url := strings.TrimSpace(os.Getenv("OMNIS_A2A_TEST_URL"))
	if url == "" {
		t.Skip("OMNIS_A2A_TEST_URL not set")
	}

	prompt := os.Getenv("OMNIS_A2A_TEST_PROMPT")
	if prompt == "" {
		prompt = "reply with the literal token PONG"
	}

	agent := Agent{Name: "live", URL: url}
	if token := strings.TrimSpace(os.Getenv("OMNIS_A2A_TEST_TOKEN")); token != "" {
		agent.Headers = map[string]string{"Authorization": "Bearer " + token}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	t.Logf("→ %s tasks/send  prompt=%q", url, prompt)
	resp, err := SendTask(ctx, agent, prompt, "", "", false)
	if err != nil {
		t.Fatalf("SendTask: %v", err)
	}
	if strings.TrimSpace(resp) == "" {
		t.Fatal("empty response from remote agent")
	}
	t.Logf("← %d chars: %s", len(resp), truncate(resp, 200))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

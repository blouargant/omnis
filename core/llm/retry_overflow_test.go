package llm

import "testing"

// LiteLLM reports a backend's context overflow as HTTP 500; retrying the same
// request cannot succeed.
func TestContextOverflowIsNotRetried(t *testing.T) {
	body := `{"error":{"message":"litellm.APIConnectionError: ScalewayException - Error code: 400 - {'error': {'message': \"This model's maximum context length is 262144 tokens.\"}}","code":"500"}}`
	if shouldRetryHTTPError(500, body) {
		t.Fatal("a context overflow was marked retryable")
	}
	if !shouldRetryHTTPError(500, `{"error":"Internal server error"}`) {
		t.Fatal("a plain 500 must still be retried")
	}
}

package llm

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const maxGenerateAttempts = 3

var generateRetryBaseDelay = 750 * time.Millisecond

func shouldRetryHTTPError(statusCode int, body string) bool {
	if statusCode == http.StatusRequestTimeout || statusCode == http.StatusConflict || statusCode == http.StatusTooEarly || statusCode == http.StatusTooManyRequests {
		return true
	}
	if statusCode >= http.StatusInternalServerError && statusCode != http.StatusNotImplemented {
		return true
	}
	lowerBody := strings.ToLower(body)
	return strings.Contains(lowerBody, "overloaded") || strings.Contains(lowerBody, "rate_limit") || strings.Contains(lowerBody, "temporarily unavailable")
}

func retryDelay(resp *http.Response, attempt int) time.Duration {
	if resp != nil {
		if raw := strings.TrimSpace(resp.Header.Get("Retry-After")); raw != "" {
			if seconds, err := strconv.Atoi(raw); err == nil && seconds >= 0 {
				return time.Duration(seconds) * time.Second
			}
			if when, err := http.ParseTime(raw); err == nil {
				if delay := time.Until(when); delay > 0 {
					return delay
				}
				return 0
			}
		}
	}
	delay := generateRetryBaseDelay
	for i := 0; i < attempt; i++ {
		delay *= 2
	}
	return delay
}

func waitBeforeRetry(ctx context.Context, delay time.Duration) bool {
	if delay <= 0 {
		return true
	}
	if ctx == nil {
		ctx = context.Background()
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func contextDone(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	err := ctx.Err()
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

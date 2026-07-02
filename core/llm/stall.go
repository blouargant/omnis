package llm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync/atomic"
	"time"
)

// defaultStreamStallTimeout bounds how long a streaming response may go
// without any new bytes before the read is aborted. A misbehaving upstream
// (connection held open mid-stream — no further data, no `[DONE]`, no close)
// would otherwise hang until the client's 5-minute total timeout, freezing the
// turn "mid sentence". Override via OMNIS_LLM_STREAM_STALL_TIMEOUT (a Go
// duration such as "20s" or "90s"); a value <= 0 disables the guard.
const defaultStreamStallTimeout = 10 * time.Minute

// errStreamStalled is yielded when the stall guard aborts a streaming read.
var errStreamStalled = errors.New("The session has been running for too long without an update")

func streamStallTimeout() time.Duration {
	if v := strings.TrimSpace(os.Getenv("OMNIS_LLM_STREAM_STALL_TIMEOUT")); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return defaultStreamStallTimeout
}

// defaultHTTPClientTimeout caps the total duration of a single LLM HTTP request
// (connection + reading the whole, possibly streamed, body). It sits *above* the
// stream stall guard (defaultStreamStallTimeout, 10m) so a genuinely frozen
// stream is caught by the guard first — with a clear message — rather than by
// this raw client timeout, while a slow-but-alive generation on a sluggish
// backend is not strangled. The previous hard 5-minute cap fired *before* the
// stall guard and killed legitimate long generations on slow backends (e.g. a
// Scaleway-hosted model whose generation intermittently blocks for minutes under
// load) with "Client.Timeout ... while reading body". Override via
// OMNIS_LLM_HTTP_TIMEOUT (a Go duration such as "20m"); "0" disables the cap
// entirely, leaving only the stall guard + request context in control.
const defaultHTTPClientTimeout = 15 * time.Minute

func httpClientTimeout() time.Duration {
	if v := strings.TrimSpace(os.Getenv("OMNIS_LLM_HTTP_TIMEOUT")); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d >= 0 {
			return d
		}
	}
	return defaultHTTPClientTimeout
}

// stallGuard wraps a streaming response body so that if no Read makes progress
// within `timeout`, the request context is cancelled — which unblocks the
// in-flight Read with an error. Stalled() then reports whether the abort was a
// stall (so callers can surface a clear message instead of a bare
// context.Canceled). When timeout <= 0 the guard is a transparent pass-through.
type stallGuard struct {
	r        io.Reader
	timeout  time.Duration
	activity chan struct{}
	stalled  atomic.Bool
}

// newStallGuard wraps r and starts the idle watchdog. ctx is the (cancelable)
// request context; cancel is its CancelFunc. The watchdog exits when ctx is
// done, so the caller's deferred cancel() also tears it down on normal
// completion.
func newStallGuard(ctx context.Context, r io.Reader, cancel context.CancelFunc, timeout time.Duration) *stallGuard {
	g := &stallGuard{r: r, timeout: timeout, activity: make(chan struct{}, 1)}
	if timeout <= 0 {
		return g
	}
	go func() {
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-g.activity:
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(timeout)
			case <-timer.C:
				g.stalled.Store(true)
				cancel()
				return
			}
		}
	}()
	return g
}

func (g *stallGuard) Read(p []byte) (int, error) {
	n, err := g.r.Read(p)
	if n > 0 && g.timeout > 0 {
		// Signal progress to the watchdog (non-blocking).
		select {
		case g.activity <- struct{}{}:
		default:
		}
	}
	if err != nil && g.stalled.Load() {
		return n, fmt.Errorf("%w (no response for %s). Please try again.", errStreamStalled, g.timeout)
	}
	return n, err
}

// Stalled reports whether the guard aborted the stream due to inactivity.
func (g *stallGuard) Stalled() bool { return g.stalled.Load() }

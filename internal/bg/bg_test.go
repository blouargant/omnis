package bg

import (
	"context"
	"testing"
	"time"
)

func TestQueueDrainAndWait(t *testing.T) {
	t.Parallel()

	q := NewQueue(1)
	q.ch <- Notification{Label: "job", Status: "completed", Output: "ok"}

	n, ok := q.Wait(context.Background())
	if !ok {
		t.Fatal("Wait() = false, want notification")
	}
	if n.Label != "job" || n.Output != "ok" {
		t.Fatalf("Wait() = %+v", n)
	}

	if drained := q.Drain(); len(drained) != 0 {
		t.Fatalf("Drain() = %+v, want empty after Wait", drained)
	}
}

func TestQueueStartProducesCompletedNotification(t *testing.T) {
	q := NewQueue(1)
	q.Start("echo-test", "printf ok", time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	n, ok := q.Wait(ctx)
	if !ok {
		t.Fatal("Wait() = false, want background result")
	}
	if n.Status != "completed" {
		t.Fatalf("Status = %q, want completed", n.Status)
	}
	if n.Output != "ok" {
		t.Fatalf("Output = %q, want ok", n.Output)
	}
	if n.Ended.Before(n.Started) {
		t.Fatalf("notification timestamps invalid: %+v", n)
	}
}

func TestFormatNotification(t *testing.T) {
	t.Parallel()

	msg := FormatNotification(Notification{Label: "job", Status: "failed", Output: "boom", Started: time.Unix(0, 0), Ended: time.Unix(2, 0)})
	if msg == "" || msg[:17] != "[Background \"job\"" {
		t.Fatalf("FormatNotification() = %q", msg)
	}
}
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

func TestQueueStartEnforcesSafetyFloor(t *testing.T) {
	q := NewQueue(1)
	q.Start("danger", "mkfs.ext4 /dev/sdb", time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	n, ok := q.Wait(ctx)
	if !ok {
		t.Fatal("Wait() = false, want blocked result")
	}
	if n.Status != "blocked" {
		t.Fatalf("Status = %q, want blocked (safety floor must apply to the background queue)", n.Status)
	}
}

func TestFormatNotification(t *testing.T) {
	t.Parallel()

	msg := FormatNotification(Notification{Label: "job", Status: "failed", Output: "boom", Started: time.Unix(0, 0), Ended: time.Unix(2, 0)})
	if msg == "" || msg[:17] != "[Background \"job\"" {
		t.Fatalf("FormatNotification() = %q", msg)
	}
}

// TestCancelStopsTheCommandsChildren: the shell runs `sleep` as a child (a
// second command follows it), so killing only the shell would leave sleep
// holding the output pipe and the task "running" until it exits. Cancelling
// must end the whole process group at once.
func TestCancelStopsTheCommandsChildren(t *testing.T) {
	t.Parallel()

	q := NewQueue(4)
	id := q.Start("sleeper", "sleep 30; echo done", time.Minute)
	time.Sleep(100 * time.Millisecond) // let the shell start its child
	if _, ok := q.Cancel(id); !ok {
		t.Fatal("Cancel() = false for a known task")
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, info, _ := q.Output(id); info.Status == "cancelled" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("a stopped task kept running: its children outlived the shell")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

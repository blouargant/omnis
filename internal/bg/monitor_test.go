package bg

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestMonitorEmitsMatchedLinesBatched(t *testing.T) {
	q := NewQueue(8)
	id, err := q.StartMonitor("m", `printf 'INFO a\nERROR b\nINFO c\nERROR d\n'`, "ERROR", 5*time.Second, false)
	if err != nil {
		t.Fatalf("StartMonitor: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

	n, ok := q.Wait(ctx)
	if !ok {
		t.Fatal("Wait() = false, want a monitor event")
	}
	if n.Kind != KindMonitor || n.Status != "event" {
		t.Fatalf("first notification = %+v, want a monitor event", n)
	}
	if !strings.Contains(n.Output, "ERROR b") || !strings.Contains(n.Output, "ERROR d") {
		t.Fatalf("event output = %q, want both matched lines (batched)", n.Output)
	}
	if strings.Contains(n.Output, "INFO") {
		t.Fatalf("filter leaked non-matching lines: %q", n.Output)
	}

	n2, ok := q.Wait(ctx)
	if !ok || n2.Kind != KindMonitor || n2.Status != "completed" {
		t.Fatalf("terminal notification = %+v ok=%v, want monitor completed", n2, ok)
	}

	out, info, ok := q.Output(id)
	if !ok {
		t.Fatal("Output() = false, task should be registered")
	}
	if !strings.Contains(out, "ERROR d") {
		t.Fatalf("task output buffer = %q, want retained matches", out)
	}
	if info.Kind != KindMonitor {
		t.Fatalf("task info kind = %q", info.Kind)
	}
}

func TestTaskRegistryListAndOutput(t *testing.T) {
	q := NewQueue(4)
	id := q.Start("job", "printf hello", 2*time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, ok := q.Wait(ctx); !ok {
		t.Fatal("Wait() = false, want completion")
	}

	tasks := q.ListTasks()
	if len(tasks) != 1 || tasks[0].ID != id {
		t.Fatalf("ListTasks() = %+v, want one task with id %s", tasks, id)
	}
	out, info, ok := q.Output(id)
	if !ok {
		t.Fatal("Output() = false")
	}
	if out != "hello" {
		t.Fatalf("Output() = %q, want hello", out)
	}
	if info.Status != "completed" || info.Kind != KindCommand {
		t.Fatalf("task info = %+v", info)
	}
}

// TestToolNamesAvoidPlanningCollision guards the rename that fixed a duplicate
// "task_list" between the bg lifecycle tools and the planning group's
// task-graph tools (internal/tasks registers task_list/task_next/TaskCreate/
// TaskUpdate). The leader mounts both groups, so a bg tool in the task_* / Task*
// namespace makes the squad fail to build with a "duplicate tool" error.
func TestToolNamesAvoidPlanningCollision(t *testing.T) {
	t.Parallel()
	s := NewSessionQueues(4)
	want := map[string]bool{
		"bash_background": false, "monitor": false,
		"bg_list": false, "bg_cancel": false, "bg_output": false,
	}
	for _, tl := range s.Tools() {
		name := tl.Name()
		if strings.HasPrefix(name, "task_") || strings.HasPrefix(name, "Task") {
			t.Errorf("bg tool %q uses the planning task_*/Task* namespace — collides with the task-graph tools", name)
		}
		if _, ok := want[name]; !ok {
			t.Errorf("unexpected bg tool name %q", name)
			continue
		}
		want[name] = true
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("missing expected bg tool %q", name)
		}
	}
}

func TestMonitorCancel(t *testing.T) {
	q := NewQueue(16)
	id, err := q.StartMonitor("tail", `for i in $(seq 1 100); do echo tick; sleep 0.05; done`, "tick", 0, true)
	if err != nil {
		t.Fatalf("StartMonitor: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	n, ok := q.Wait(ctx)
	if !ok || n.Status != "event" {
		t.Fatalf("first notification = %+v ok=%v, want an event", n, ok)
	}
	if _, ok := q.Cancel(id); !ok {
		t.Fatal("Cancel() = false, want the task")
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		n, ok := q.Wait(ctx)
		if !ok {
			break
		}
		if n.Kind == KindMonitor && n.Status == "cancelled" {
			return
		}
	}
	t.Fatal("no cancelled terminal notification after Cancel()")
}

package bg

import (
	"strings"
	"testing"
	"time"
)

func TestStartPassesEnv(t *testing.T) {
	q := NewQueue(8)
	id := q.Start("t", `echo "v=$OMNIS_T"`, 10*time.Second, "OMNIS_T=abc")
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if out, _, ok := q.Output(id); ok && strings.Contains(out, "v=abc") {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("background task did not see the env")
}

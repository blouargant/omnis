package softskills

import (
	"testing"
	"time"

	"github.com/blouargant/yoke/internal/compress"
)

func TestClassifyMessage(t *testing.T) {
	cases := []struct {
		in   string
		want Tristate
	}{
		{"thanks, perfect", Positive},
		{"this works great", Positive},
		{"not great", Unknown},
		{"doesn't work", Negative},
		{"no, that's wrong", Negative},
		{"broken again", Negative},
		{"failed at step 2", Negative},
		{"", Unknown},
		{"plain neutral text", Unknown},
	}
	for _, c := range cases {
		got := classifyMessage(c.in)
		if got != c.want {
			t.Errorf("classifyMessage(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestReflectHeuristicPositiveSession(t *testing.T) {
	t0 := time.Now()
	out := ReflectHeuristic(HeuristicInputs{
		StateLog: &compress.StateLog{
			Decisions: []string{"use k8s drain"},
			Tools:     map[string]int{"run_bash": 3},
		},
		LastUserMessages: []string{"thanks, perfect"},
		LoadedSkills: []LoadedSkill{
			{Key: "investigator/k8s-pod-evidence", When: t0},
			{Key: "wrap-session", When: t0.Add(time.Second)},
		},
	})
	if out.Success != Positive {
		t.Fatalf("Success = %v, want Positive (signals=%v)", out.Success, out.Signals)
	}
	for _, k := range []string{"investigator/k8s-pod-evidence", "wrap-session"} {
		if out.Tags[k] != "helpful" {
			t.Errorf("Tags[%s] = %q, want helpful", k, out.Tags[k])
		}
	}
}

func TestReflectHeuristicNegativeWithErrorAfterLoad(t *testing.T) {
	loadAt := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	errAt := loadAt.Add(30 * time.Second)
	out := ReflectHeuristic(HeuristicInputs{
		StateLog: &compress.StateLog{
			OpenIssues: []string{"deployment still failing"},
			Tools:      map[string]int{"run_bash": 2},
		},
		LastUserMessages: []string{"no that broke"},
		ToolErrors: []ToolError{
			{Tool: "run_bash", Error: "exit 1", When: errAt},
		},
		LoadedSkills: []LoadedSkill{
			{Key: "foo", When: loadAt},
			// loaded AFTER the error → harm not attributable
			{Key: "bar", When: errAt.Add(time.Second)},
		},
	})
	if out.Success != Negative {
		t.Fatalf("Success = %v, want Negative (signals=%v)", out.Success, out.Signals)
	}
	if out.Tags["foo"] != "harmful" {
		t.Errorf("Tags[foo] = %q, want harmful", out.Tags["foo"])
	}
	if out.Tags["bar"] != "neutral" {
		t.Errorf("Tags[bar] = %q, want neutral (loaded after the error)", out.Tags["bar"])
	}
}

func TestReflectHeuristicSubAgentRetryFlipsHarmful(t *testing.T) {
	loadAt := time.Now()
	out := ReflectHeuristic(HeuristicInputs{
		LoadedSkills: []LoadedSkill{
			{Key: "investigator/foo", When: loadAt},
		},
		SubAgentRetried: true,
	})
	if out.Success != Negative {
		t.Fatalf("Success = %v, want Negative; signals=%v", out.Success, out.Signals)
	}
	if out.Tags["investigator/foo"] != "harmful" {
		t.Errorf("retry should flag the loaded skill harmful, got %q", out.Tags["investigator/foo"])
	}
}

func TestReflectHeuristicAmbiguous(t *testing.T) {
	out := ReflectHeuristic(HeuristicInputs{
		StateLog: &compress.StateLog{
			Decisions: []string{"d"},
			Tools:     map[string]int{"x": 1},
		},
		// Both a clean state and a "broken" message → ambiguous.
		LastUserMessages: []string{"broken"},
		LoadedSkills: []LoadedSkill{
			{Key: "z", When: time.Now()},
		},
	})
	if out.Success != Ambiguous {
		t.Errorf("Success = %v, want Ambiguous; signals=%v", out.Success, out.Signals)
	}
	if out.Tags["z"] != "neutral" {
		t.Errorf("Tags[z] = %q, want neutral on ambiguous", out.Tags["z"])
	}
}

func TestReflectHeuristicUnknownWhenNothingSeen(t *testing.T) {
	out := ReflectHeuristic(HeuristicInputs{})
	if out.Success != Unknown {
		t.Errorf("empty inputs → Success = %v, want Unknown", out.Success)
	}
	if len(out.Tags) != 0 {
		t.Errorf("expected no tags on empty inputs, got %+v", out.Tags)
	}
}

func TestReflectHeuristicExplicitFeedbackOverridesUserMessage(t *testing.T) {
	out := ReflectHeuristic(HeuristicInputs{
		// Last user message would scan as positive, but the wrap-up
		// answer (explicit feedback) is negative.
		LastUserMessages: []string{"thanks"},
		ExplicitFeedback: "no, the deploy step was wrong",
		StateLog: &compress.StateLog{
			Tools: map[string]int{"x": 1},
		},
		LoadedSkills: []LoadedSkill{
			{Key: "deploy-skill", When: time.Now()},
		},
	})
	if out.Success != Negative {
		t.Fatalf("explicit negative feedback should drive Negative verdict; got %v signals=%v", out.Success, out.Signals)
	}
}

func TestReflectHeuristicConfidence(t *testing.T) {
	// All-positive: clean statelog + positive user msg + no errors.
	out := ReflectHeuristic(HeuristicInputs{
		StateLog: &compress.StateLog{
			Decisions: []string{"d"},
			Tools:     map[string]int{"x": 1},
		},
		LastUserMessages: []string{"perfect"},
	})
	if out.Confidence != 1.0 {
		t.Errorf("confidence = %v, want 1.0 for all-positive signals", out.Confidence)
	}
	// Tied: positive user message vs. open-issues statelog. The Tools
	// map is empty so the "no recent errors" positive does not fire and
	// the signals balance exactly.
	out2 := ReflectHeuristic(HeuristicInputs{
		StateLog: &compress.StateLog{
			OpenIssues: []string{"o"},
		},
		LastUserMessages: []string{"perfect"},
	})
	if out2.Success != Ambiguous {
		t.Fatalf("expected Ambiguous on tied signals, got %v signals=%v", out2.Success, out2.Signals)
	}
	if out2.Confidence != 0 {
		t.Errorf("confidence = %v, want 0 for evenly-tied signals", out2.Confidence)
	}
}

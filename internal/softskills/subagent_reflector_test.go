package softskills

import (
	"testing"
)

func TestClassifyLeaderReactionRetry(t *testing.T) {
	cases := []string{
		"That didn't work, let me ask again.",
		"failed, will retask",
		"that's wrong; please try with the staging endpoint",
		"investigator returned an empty result; let me try again",
	}
	for _, text := range cases {
		got := ClassifyLeaderReaction("investigator", text)
		if got != LeaderRetried {
			t.Errorf("ClassifyLeaderReaction(%q) = %v, want LeaderRetried", text, got)
		}
	}
}

func TestClassifyLeaderReactionApproval(t *testing.T) {
	cases := []string{
		"Per investigator, the deployment uses HPA scaling.",
		"according to investigator, no errors in the last hour",
		"investigator's findings show the queue is empty.",
		"The investigator reported a 503 on /healthz.",
		"investigator confirmed the pod is running.",
		"investigator found two stale config maps.",
	}
	for _, text := range cases {
		got := ClassifyLeaderReaction("investigator", text)
		if got != LeaderApproved {
			t.Errorf("ClassifyLeaderReaction(%q) = %v, want LeaderApproved", text, got)
		}
	}
}

func TestClassifyLeaderReactionUnknown(t *testing.T) {
	cases := []string{
		"",
		"   ",
		"Let me think about this.",
		"I'll handle the next step myself.",
	}
	for _, text := range cases {
		got := ClassifyLeaderReaction("investigator", text)
		if got != LeaderUnknown {
			t.Errorf("ClassifyLeaderReaction(%q) = %v, want LeaderUnknown", text, got)
		}
	}
}

func TestTagInvocationApprovedHelpful(t *testing.T) {
	got := TagInvocation(SubAgentInvocation{
		Agent:          "investigator",
		LoadedSkills:   []string{"investigator/k8s"},
		OutputText:     "Found two stale pods; details inline.",
		LeaderReaction: LeaderApproved,
	})
	if got["investigator/k8s"] != "helpful" {
		t.Errorf("got %q, want helpful", got["investigator/k8s"])
	}
}

func TestTagInvocationRetryHarmful(t *testing.T) {
	got := TagInvocation(SubAgentInvocation{
		Agent:        "investigator",
		LoadedSkills: []string{"investigator/k8s"},
		OutputText:   "...",
		Retried:      true,
	})
	if got["investigator/k8s"] != "harmful" {
		t.Errorf("got %q, want harmful (retry)", got["investigator/k8s"])
	}
}

func TestTagInvocationErrorOutputHarmful(t *testing.T) {
	got := TagInvocation(SubAgentInvocation{
		Agent:        "investigator",
		LoadedSkills: []string{"investigator/k8s"},
		OutputText:   "Error: kubectl returned non-zero",
	})
	if got["investigator/k8s"] != "harmful" {
		t.Errorf("got %q, want harmful (Error: prefix)", got["investigator/k8s"])
	}
}

func TestTagInvocationEmptyOutputHarmful(t *testing.T) {
	got := TagInvocation(SubAgentInvocation{
		Agent:        "investigator",
		LoadedSkills: []string{"investigator/k8s"},
		OutputText:   "",
	})
	if got["investigator/k8s"] != "harmful" {
		t.Errorf("got %q, want harmful (empty output)", got["investigator/k8s"])
	}
}

func TestTagInvocationLeaderRetriedHarmful(t *testing.T) {
	got := TagInvocation(SubAgentInvocation{
		Agent:          "investigator",
		LoadedSkills:   []string{"investigator/k8s"},
		OutputText:     "Detailed findings inline.",
		LeaderReaction: LeaderRetried,
	})
	if got["investigator/k8s"] != "harmful" {
		t.Errorf("got %q, want harmful (LeaderRetried)", got["investigator/k8s"])
	}
}

func TestTagInvocationDefaultsToNeutral(t *testing.T) {
	got := TagInvocation(SubAgentInvocation{
		Agent:          "investigator",
		LoadedSkills:   []string{"investigator/k8s"},
		OutputText:     "Detailed findings inline — no clear leader citation.",
		LeaderReaction: LeaderUnknown,
	})
	if got["investigator/k8s"] != "neutral" {
		t.Errorf("got %q, want neutral", got["investigator/k8s"])
	}
}

func TestTagInvocationToolErrorTrumpsApproval(t *testing.T) {
	got := TagInvocation(SubAgentInvocation{
		Agent:          "investigator",
		LoadedSkills:   []string{"investigator/k8s"},
		OutputText:     "Looked at logs, found nothing.",
		ToolErrors:     []ToolError{{Tool: "run_bash", Error: "exit 1"}},
		LeaderReaction: LeaderApproved,
	})
	if got["investigator/k8s"] != "harmful" {
		t.Errorf("got %q, want harmful (tool error trumps approval)", got["investigator/k8s"])
	}
}

func TestTagInvocationEmptyLoadedSkillsNoOp(t *testing.T) {
	got := TagInvocation(SubAgentInvocation{Agent: "investigator"})
	if len(got) != 0 {
		t.Errorf("expected empty map, got %v", got)
	}
}

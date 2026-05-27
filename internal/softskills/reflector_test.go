package softskills

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseReflectorOutputHappy(t *testing.T) {
	body := `{
  "reasoning": "Open issues empty, user said thanks.",
  "success": "positive",
  "key_insight": "always run validate after apply",
  "bullet_tags": [
    {"key": "investigator/k8s-pod-evidence", "tag": "helpful", "reason": "evidence-gathering directly produced the diagnosis"},
    {"key": "wrap-session", "tag": "neutral", "reason": "loaded but didn't drive outcome"}
  ]
}`
	out, err := parseReflectorOutput(body, []string{"investigator/k8s-pod-evidence", "wrap-session"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if out.Success != Positive {
		t.Errorf("Success = %v, want Positive", out.Success)
	}
	want := map[string]string{
		"investigator/k8s-pod-evidence": "helpful",
		"wrap-session":                  "neutral",
	}
	if !reflect.DeepEqual(out.Tags, want) {
		t.Errorf("Tags = %v, want %v", out.Tags, want)
	}
	if out.KeyInsight != "always run validate after apply" {
		t.Errorf("KeyInsight = %q", out.KeyInsight)
	}
	if got := out.TagReasons["investigator/k8s-pod-evidence"]; got != "evidence-gathering directly produced the diagnosis" {
		t.Errorf("TagReasons[k8s] = %q", got)
	}
	if _, ok := out.TagReasons["wrap-session"]; !ok {
		t.Errorf("TagReasons missing wrap-session")
	}
}

func TestParseReflectorOutputStripsCodeFence(t *testing.T) {
	body := "```json\n{\"success\":\"negative\",\"key_insight\":\"\",\"reasoning\":\"...\",\"bullet_tags\":[]}\n```"
	out, err := parseReflectorOutput(body, nil)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if out.Success != Negative {
		t.Errorf("Success = %v, want Negative", out.Success)
	}
}

func TestParseReflectorOutputAmbiguous(t *testing.T) {
	body := `{"success":"ambiguous","key_insight":"","reasoning":"","bullet_tags":[]}`
	out, err := parseReflectorOutput(body, nil)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if out.Success != Ambiguous {
		t.Errorf("Success = %v, want Ambiguous", out.Success)
	}
	if out.Confidence != 0 {
		t.Errorf("Confidence = %v, want 0 on ambiguous", out.Confidence)
	}
}

func TestParseReflectorOutputDropsUnknownKeys(t *testing.T) {
	body := `{
  "success":"positive","reasoning":"","key_insight":"",
  "bullet_tags":[
    {"key":"phantom/skill","tag":"helpful","reason":"hallucinated"},
    {"key":"real-skill","tag":"helpful","reason":"actually loaded"}
  ]
}`
	out, err := parseReflectorOutput(body, []string{"real-skill"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, present := out.Tags["phantom/skill"]; present {
		t.Errorf("hallucinated key was not dropped: %v", out.Tags)
	}
	if out.Tags["real-skill"] != "helpful" {
		t.Errorf("real-skill tag = %q, want helpful", out.Tags["real-skill"])
	}
}

func TestParseReflectorOutputNormalisesUnknownTag(t *testing.T) {
	body := `{
  "success":"positive","reasoning":"","key_insight":"",
  "bullet_tags":[{"key":"x","tag":"awesome","reason":"r"}]
}`
	out, err := parseReflectorOutput(body, []string{"x"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if out.Tags["x"] != "neutral" {
		t.Errorf("unknown tag should fall back to neutral, got %q", out.Tags["x"])
	}
}

func TestParseReflectorOutputErrors(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{"empty", ""},
		{"not json", "I think the session went well."},
		{"missing success", `{"reasoning":"","key_insight":"","bullet_tags":[]}`},
		{"bad success value", `{"success":"meh","reasoning":"","key_insight":"","bullet_tags":[]}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := parseReflectorOutput(c.input, nil); err == nil {
				t.Errorf("expected error on %q, got nil", c.input)
			}
		})
	}
}

func TestMergeOutcomesLLMWins(t *testing.T) {
	heur := Outcome{
		Success: Negative,
		Tags:    map[string]string{"foo": "neutral", "bar": "harmful"},
		Signals: []string{"heur:open_issues"},
	}
	llm := Outcome{
		Success:    Positive,
		Tags:       map[string]string{"foo": "helpful"}, // overrides heur
		Signals:    []string{"llm:reasoning"},
		KeyInsight: "X always works after Y",
		TagReasons: map[string]string{"foo": "actually drove the fix"},
	}
	merged := MergeOutcomes(heur, llm)
	if merged.Success != Positive {
		t.Errorf("Success = %v, want Positive (LLM wins)", merged.Success)
	}
	if merged.Tags["foo"] != "helpful" {
		t.Errorf("Tags[foo] = %q, want helpful", merged.Tags["foo"])
	}
	if merged.Tags["bar"] != "harmful" {
		t.Errorf("Tags[bar] = %q, want harmful (heur fills the gap)", merged.Tags["bar"])
	}
	if got := strings.Join(merged.Signals, ","); !strings.Contains(got, "heur:open_issues") || !strings.Contains(got, "llm:reasoning") {
		t.Errorf("merged signals = %v, want both heur and llm", merged.Signals)
	}
	if merged.KeyInsight != "X always works after Y" {
		t.Errorf("KeyInsight = %q (LLM should propagate)", merged.KeyInsight)
	}
	if merged.TagReasons["foo"] != "actually drove the fix" {
		t.Errorf("TagReasons[foo] = %q", merged.TagReasons["foo"])
	}
}

func TestMergeOutcomesLLMUnknownFallsBackToHeuristic(t *testing.T) {
	heur := Outcome{Success: Negative, Tags: map[string]string{"x": "harmful"}}
	llm := Outcome{Success: Unknown, Tags: map[string]string{}}
	merged := MergeOutcomes(heur, llm)
	if merged.Success != Negative {
		t.Errorf("Success = %v, want Negative (heur fallback)", merged.Success)
	}
}

func TestStripJSONFences(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"```json\n{\"x\":1}\n```", "{\"x\":1}"},
		{"```\n{\"x\":1}\n```", "{\"x\":1}"},
		{"{\"x\":1}", "{\"x\":1}"},
		{"```json{\"x\":1}```", "{\"x\":1}"},
	}
	for _, c := range cases {
		got := stripJSONFences(c.in)
		if got != c.want {
			t.Errorf("stripJSONFences(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

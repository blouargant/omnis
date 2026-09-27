package llm

import (
	"context"
	"iter"
	"strings"
	"testing"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

// scriptedLLM replays one scripted stream per GenerateContent call and records
// the requests it received.
type scriptedLLM struct {
	scripts [][]*model.LLMResponse
	calls   []*model.LLMRequest
}

func (s *scriptedLLM) Name() string { return "scripted" }

func (s *scriptedLLM) GenerateContent(_ context.Context, req *model.LLMRequest, _ bool) iter.Seq2[*model.LLMResponse, error] {
	i := len(s.calls)
	s.calls = append(s.calls, req)
	return func(yield func(*model.LLMResponse, error) bool) {
		if i >= len(s.scripts) {
			return
		}
		for _, r := range s.scripts[i] {
			if !yield(r, nil) {
				return
			}
		}
	}
}

func partial(text string) *model.LLMResponse {
	return &model.LLMResponse{Partial: true, Content: &genai.Content{Role: "model", Parts: []*genai.Part{{Text: text}}}}
}

func final(text string) *model.LLMResponse {
	return &model.LLMResponse{TurnComplete: true, Content: &genai.Content{Role: "model", Parts: []*genai.Part{{Text: text}}}}
}

func finalCall(name string) *model.LLMResponse {
	return &model.LLMResponse{TurnComplete: true, Content: &genai.Content{Role: "model", Parts: []*genai.Part{
		{FunctionCall: &genai.FunctionCall{Name: name, Args: map[string]any{"kind": "text"}}},
	}}}
}

// streamOf splits text into chunks the way a real stream does, then the final.
func streamOf(chunks ...string) []*model.LLMResponse {
	var out []*model.LLMResponse
	for _, c := range chunks {
		out = append(out, partial(c))
	}
	return append(out, final(strings.Join(chunks, "")))
}

func toolRequest() *model.LLMRequest {
	return &model.LLMRequest{
		Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "quels GPU sont disponibles ?"}}}},
		Config: &genai.GenerateContentConfig{Tools: []*genai.Tool{{FunctionDeclarations: []*genai.FunctionDeclaration{
			{Name: "AskUserQuestion"},
		}}}},
	}
}

// collect runs the guard and returns every yielded response plus all text
// the stream ever exposed (partials and finals).
func collect(t *testing.T, g model.LLM, req *model.LLMRequest, stream bool) ([]*model.LLMResponse, string) {
	t.Helper()
	var out []*model.LLMResponse
	var seen strings.Builder
	for r, err := range g.GenerateContent(context.Background(), req, stream) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		out = append(out, r)
		if r.Content != nil {
			for _, p := range r.Content.Parts {
				seen.WriteString(p.Text)
			}
		}
	}
	return out, seen.String()
}

func lastFinal(t *testing.T, rs []*model.LLMResponse) *model.LLMResponse {
	t.Helper()
	for i := len(rs) - 1; i >= 0; i-- {
		if !rs[i].Partial {
			return rs[i]
		}
	}
	t.Fatal("no final response")
	return nil
}

// Ordinary text — including a "<" that is not tool markup — passes through
// untouched and triggers no second call.
func TestToolTextGuardPassesOrdinaryText(t *testing.T) {
	for _, chunks := range [][]string{
		{"Voici vos ", "projets : alpha, beta."},
		{"Si a < b alors ", "on garde <div> tel quel", " et <b>gras</b>."},
		{"Le fichier <", "config.yaml> est prêt."},
	} {
		inner := &scriptedLLM{scripts: [][]*model.LLMResponse{streamOf(chunks...)}}
		rs, seen := collect(t, GuardToolText(inner), toolRequest(), true)
		want := strings.Join(chunks, "")
		if len(inner.calls) != 1 {
			t.Errorf("%q: %d model calls, want 1", want, len(inner.calls))
		}
		if got := lastFinal(t, rs).Content.Parts[0].Text; got != want {
			t.Errorf("final = %q, want %q", got, want)
		}
		var partials strings.Builder
		for _, r := range rs {
			if r.Partial {
				partials.WriteString(r.Content.Parts[0].Text)
			}
		}
		if partials.String() != want {
			t.Errorf("streamed %q, want %q", partials.String(), want)
		}
		_ = seen
	}
}

// The observed failure: a tool call leaks as Qwen XML text. The markup is
// never exposed, and the request is retried once with a corrective nudge whose
// real tool call is what the caller receives.
func TestToolTextGuardRetriesLeakedToolCall(t *testing.T) {
	leak := streamOf("\n\n<param", "eter=kind>\ntext\n</parameter>\n</function>\n</tool_call>")
	inner := &scriptedLLM{scripts: [][]*model.LLMResponse{leak, {finalCall("AskUserQuestion")}}}
	rs, seen := collect(t, GuardToolText(inner), toolRequest(), true)
	if strings.Contains(seen, "<parameter") || strings.Contains(seen, "</tool_call") {
		t.Fatalf("leaked markup was exposed: %q", seen)
	}
	if len(inner.calls) != 2 {
		t.Fatalf("%d model calls, want 2 (original + one retry)", len(inner.calls))
	}
	retry := inner.calls[1]
	if n := len(retry.Contents); n != len(toolRequest().Contents)+1 {
		t.Fatalf("retry has %d contents, want the original plus one nudge", n)
	}
	nudge := retry.Contents[len(retry.Contents)-1]
	if nudge.Role != "user" || !strings.Contains(nudge.Parts[0].Text, "function-calling") {
		t.Errorf("retry nudge = %+v", nudge)
	}
	if fc := lastFinal(t, rs).Content.Parts[0].FunctionCall; fc == nil || fc.Name != "AskUserQuestion" {
		t.Errorf("final should carry the retry's real tool call, got %+v", lastFinal(t, rs).Content.Parts[0])
	}
}

// Leaking twice ends with an explicit message — never raw markup, never an
// empty turn — and no third call.
func TestToolTextGuardFallsBackAfterTwoLeaks(t *testing.T) {
	leak := streamOf("<tool_call>\n<function=Bash>\n<parameter=command>ls</parameter>\n</function>\n</tool_call>")
	inner := &scriptedLLM{scripts: [][]*model.LLMResponse{leak, leak, leak}}
	rs, seen := collect(t, GuardToolText(inner), toolRequest(), true)
	if len(inner.calls) != 2 {
		t.Fatalf("%d model calls, want 2", len(inner.calls))
	}
	if strings.Contains(seen, "<tool_call") || strings.Contains(seen, "<parameter") {
		t.Fatalf("markup exposed: %q", seen)
	}
	got := lastFinal(t, rs).Content.Parts[0].Text
	if got != ToolTextFallback {
		t.Errorf("final = %q, want the fallback message", got)
	}
}

// Prose streamed before the markup is kept; only the markup is withheld.
func TestToolTextGuardKeepsProseBeforeMarkup(t *testing.T) {
	leak := streamOf("Je vérifie les GPU.", "\n<tool_call>\n<function=Bash>")
	inner := &scriptedLLM{scripts: [][]*model.LLMResponse{leak, {finalCall("Bash")}}}
	_, seen := collect(t, GuardToolText(inner), toolRequest(), true)
	if !strings.Contains(seen, "Je vérifie les GPU.") {
		t.Errorf("prose before the markup was dropped: %q", seen)
	}
	if strings.Contains(seen, "<tool_call") {
		t.Errorf("markup exposed: %q", seen)
	}
}

// A request without tools cannot leak a tool call: the guard is inert, even
// when the text contains the markup (a model explaining the format).
func TestToolTextGuardInertWithoutTools(t *testing.T) {
	text := "Le format est <tool_call><function=x></function></tool_call>."
	inner := &scriptedLLM{scripts: [][]*model.LLMResponse{streamOf(text)}}
	req := toolRequest()
	req.Config.Tools = nil
	rs, _ := collect(t, GuardToolText(inner), req, true)
	if len(inner.calls) != 1 || lastFinal(t, rs).Content.Parts[0].Text != text {
		t.Errorf("guard must be inert without tools: calls=%d final=%q", len(inner.calls), lastFinal(t, rs).Content.Parts[0].Text)
	}
}

// Non-streaming: one final response, same detection and retry.
func TestToolTextGuardNonStreaming(t *testing.T) {
	inner := &scriptedLLM{scripts: [][]*model.LLMResponse{
		{final("<parameter=kind>text</parameter></function></tool_call>")},
		{finalCall("AskUserQuestion")},
	}}
	rs, seen := collect(t, GuardToolText(inner), toolRequest(), false)
	if len(inner.calls) != 2 || strings.Contains(seen, "<parameter") {
		t.Fatalf("calls=%d seen=%q", len(inner.calls), seen)
	}
	if lastFinal(t, rs).Content.Parts[0].FunctionCall == nil {
		t.Error("expected the retry's tool call")
	}
}

// A real function call alongside stray markup text is not a leak: the tool
// runs, nothing is retried.
func TestToolTextGuardRealCallIsNotALeak(t *testing.T) {
	resp := finalCall("Bash")
	resp.Content.Parts = append([]*genai.Part{{Text: "<tool_call>"}}, resp.Content.Parts...)
	inner := &scriptedLLM{scripts: [][]*model.LLMResponse{{resp}}}
	_, _ = collect(t, GuardToolText(inner), toolRequest(), false)
	if len(inner.calls) != 1 {
		t.Errorf("%d calls, want 1", len(inner.calls))
	}
}

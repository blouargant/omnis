package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"google.golang.org/genai"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/runner"

	"github.com/blouargant/yoke/core/agentkit"
	"github.com/blouargant/yoke/core/events"
	fstools "github.com/blouargant/yoke/core/tools"
	"github.com/blouargant/yoke/internal/compress"
)

type turnResult struct {
	text      string
	toolCalls int
}

type compressionStats struct {
	mu             sync.Mutex
	startCount     int
	endCount       int
	skippedCount   int
	maxReduction   int
	totalReduction int
}

func (s *compressionStats) onStart() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.startCount++
}

func (s *compressionStats) onEnd(payload map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.endCount++
	before := toInt(payload["tokens_before"])
	after := toInt(payload["tokens_after"])
	reduction := before - after
	if reduction > s.maxReduction {
		s.maxReduction = reduction
	}
	if reduction > 0 {
		s.totalReduction += reduction
	}
}

func (s *compressionStats) onSkipped() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.skippedCount++
}

func toInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	default:
		return 0
	}
}

func runTurn(ctx context.Context, r *runner.Runner, prompt string) (turnResult, error) {
	seq := r.Run(ctx, "k8s-e2e-user", "k8s-e2e-session",
		&genai.Content{Role: "user", Parts: []*genai.Part{{Text: prompt}}},
		agent.RunConfig{},
	)
	var out strings.Builder
	toolCalls := 0
	for ev, err := range seq {
		if err != nil {
			return turnResult{}, err
		}
		if ev == nil || ev.Content == nil {
			continue
		}
		for _, p := range ev.Content.Parts {
			if p == nil {
				continue
			}
			if p.FunctionCall != nil {
				toolCalls++
			}
			if ev.Content.Role == "model" && p.Text != "" && !ev.LLMResponse.Partial {
				out.WriteString(p.Text)
			}
		}
	}
	return turnResult{text: strings.TrimSpace(out.String()), toolCalls: toolCalls}, nil
}

func containsAll(text string, values ...string) bool {
	text = strings.ToLower(text)
	for _, v := range values {
		if !strings.Contains(text, strings.ToLower(v)) {
			return false
		}
	}
	return true
}

func main() {
	var (
		namespace = flag.String("namespace", "context-e2e", "Kubernetes namespace to inspect")
		pod       = flag.String("pod", "cm-loggen", "Pod name to inspect")
		marker    = flag.String("marker", "", "Unique marker expected in logs")
		auditPath = flag.String("audit-path", ".agent_memory_k8s_e2e.md", "Audit file path")
	)
	flag.Parse()
	if strings.TrimSpace(*marker) == "" {
		fmt.Fprintln(os.Stderr, "--marker is required")
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()

	llm, err := agentkit.NewModel(ctx)
	must(err)

	bus := events.NewBus()
	stats := &compressionStats{}
	bus.On(events.EventCompressionStart, func(_ string, _ map[string]any) { stats.onStart() })
	bus.On(events.EventCompressionEnd, func(_ string, p map[string]any) { stats.onEnd(p) })
	bus.On(events.EventCompressionSkipped, func(_ string, _ map[string]any) { stats.onSkipped() })

	cmp, compactTools, _, err := compress.PluginWithTools("compress", compress.Config{
		WindowTokens:       2200,
		SoftRatio:          0.35,
		HardRatio:          0.55,
		KeepHeadTurns:      1,
		KeepRecentTurns:    2,
		ToolResultMaxBytes: 1800,
		LLM:                llm,
		EventBus:           bus,
		AuditPath:          *auditPath,
	})
	must(err)

	tools := append(fstools.New(), compactTools...)
	a, err := agentkit.New(agentkit.AgentConfig{
		Name:        "s24_k8s_context_e2e",
		Model:       llm,
		Tools:       tools,
		Instruction: "Use kubectl via bash for read-only investigation. Never mutate cluster resources. Keep answers concise.",
	})
	must(err)

	r, err := agentkit.Runner("s24-k8s-context-e2e", a, cmp)
	must(err)

	turns := []string{
		fmt.Sprintf("Inspect pod logs and discover a unique marker. Run: kubectl -n %s logs %s --tail=500. Reply with exactly: DISCOVERED_MARKER=<value> and remember it for later.", *namespace, *pod),
		fmt.Sprintf("Now gather high-noise context: run kubectl -n %s describe pod %s; kubectl -n %s get events --sort-by=.lastTimestamp | tail -n 120; kubectl get pods -A -o wide. Give a 6-bullet summary.", *namespace, *pod, *namespace),
		"Call compact_now with reason 'finished noisy collection step'. Then reply with one sentence confirming you called it.",
		fmt.Sprintf("Continue with more noise: run kubectl -n %s logs %s --tail=800; kubectl get nodes -o wide; kubectl get pods -A --show-labels. Summarize in 5 bullets.", *namespace, *pod),
		"Without calling any tools, return exactly one line JSON with keys marker, namespace, pod based only on conversation memory.",
	}

	for i, prompt := range turns[:len(turns)-1] {
		res, err := runTurn(ctx, r, prompt)
		must(err)
		fmt.Printf("turn_%d_response:\n%s\n\n", i+1, res.text)
	}

	final, err := runTurn(ctx, r, turns[len(turns)-1])
	must(err)
	fmt.Printf("final_response:\n%s\n\n", final.text)

	stats.mu.Lock()
	startCount := stats.startCount
	endCount := stats.endCount
	maxReduction := stats.maxReduction
	totalReduction := stats.totalReduction
	stats.mu.Unlock()

	var failures []string
	if startCount == 0 || endCount == 0 {
		failures = append(failures, "compression events did not fire")
	}
	if maxReduction <= 0 || totalReduction <= 0 {
		failures = append(failures, "no token reduction observed on compression_end")
	}
	if final.toolCalls > 0 {
		failures = append(failures, "final memory-only question still triggered tool calls")
	}
	if !containsAll(final.text, *marker, *namespace, *pod) {
		failures = append(failures, "final response does not recall marker+namespace+pod")
	}

	if len(failures) > 0 {
		for _, f := range failures {
			fmt.Fprintf(os.Stderr, "FAIL: %s\n", f)
		}
		fmt.Fprintf(os.Stderr, "stats: compression_start=%d compression_end=%d max_reduction=%d total_reduction=%d\n", startCount, endCount, maxReduction, totalReduction)
		os.Exit(1)
	}

	fmt.Printf("PASS: compression_start=%d compression_end=%d max_reduction=%d total_reduction=%d\n", startCount, endCount, maxReduction, totalReduction)
	fmt.Printf("PASS: memory recall preserved marker=%s namespace=%s pod=%s\n", *marker, *namespace, *pod)

	if _, err := os.Stat(*auditPath); err != nil {
		must(errors.New("audit file missing: " + *auditPath))
	}
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

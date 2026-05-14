package compress

import (
	"context"
	"encoding/json"
	"os"
	"sort"
	"strings"
	"sync"

	"google.golang.org/genai"

	"google.golang.org/adk/model"
)

// StateLog is a small structured digest of "what we know so far" about the
// current session. It is auto-extracted by an AfterModelCallback every
// StateLogEvery turns (or on demand via compact_now) and persisted to
// disk so the model can be brought back up to speed after aggressive
// compression.
type StateLog struct {
	Goal       string            `json:"goal,omitempty"`
	Decisions  []string          `json:"decisions,omitempty"`
	OpenIssues []string          `json:"open_issues,omitempty"`
	Files      map[string]string `json:"files,omitempty"`
	Tools      map[string]int    `json:"tools,omitempty"`
	// TurnCount is the total number of model responses in this session.
	// Set directly by the compress plugin; never extracted by the LLM.
	TurnCount int `json:"turn_count,omitempty"`
}

// merge folds `other` into the receiver. Slices are de-duplicated; map
// values from `other` overwrite earlier entries; tool counts are summed.
func (s *StateLog) merge(other *StateLog) {
	if other == nil {
		return
	}
	if other.Goal != "" {
		s.Goal = other.Goal
	}
	s.Decisions = mergeStrings(s.Decisions, other.Decisions)
	s.OpenIssues = mergeStrings(s.OpenIssues, other.OpenIssues)
	if len(other.Files) > 0 {
		if s.Files == nil {
			s.Files = map[string]string{}
		}
		for k, v := range other.Files {
			s.Files[k] = v
		}
	}
	if len(other.Tools) > 0 {
		if s.Tools == nil {
			s.Tools = map[string]int{}
		}
		for k, v := range other.Tools {
			s.Tools[k] += v
		}
	}
	// TurnCount is set externally (never extracted by the LLM delta); take the max.
	if other.TurnCount > s.TurnCount {
		s.TurnCount = other.TurnCount
	}
}

func mergeStrings(a, b []string) []string {
	seen := make(map[string]struct{}, len(a)+len(b))
	out := make([]string, 0, len(a)+len(b))
	for _, s := range a {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	for _, s := range b {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

// renderForPrompt produces a Markdown block intended to be prepended to
// the synthetic summary turn during PassSummarizeMiddle.
func (s *StateLog) renderForPrompt() string {
	if s == nil {
		return ""
	}
	if s.Goal == "" && len(s.Decisions) == 0 && len(s.OpenIssues) == 0 && len(s.Files) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## State log (durable facts)\n")
	if s.Goal != "" {
		b.WriteString("- Goal: " + s.Goal + "\n")
	}
	if len(s.Decisions) > 0 {
		b.WriteString("- Decisions:\n")
		for _, d := range s.Decisions {
			b.WriteString("  - " + d + "\n")
		}
	}
	if len(s.OpenIssues) > 0 {
		b.WriteString("- Open issues:\n")
		for _, q := range s.OpenIssues {
			b.WriteString("  - " + q + "\n")
		}
	}
	if len(s.Files) > 0 {
		paths := make([]string, 0, len(s.Files))
		for p := range s.Files {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		b.WriteString("- Files:\n")
		for _, p := range paths {
			b.WriteString("  - " + p + ": " + s.Files[p] + "\n")
		}
	}
	return b.String()
}

// stateLogState is the per-session bookkeeping owned by the manager. It
// tracks the live log, the turn counter, and the index of the last turn
// already fed to the extractor (so we only summarise the delta).
type stateLogState struct {
	mu         sync.Mutex
	log        StateLog
	lastIndex  int // index in req.Contents up to which the log is current
	turnsSince int // turns since last extraction
}

// stateLogExtractorPrompt is the prompt sent to the StateLogLLM. The
// model is asked to emit ONLY a JSON document matching the StateLog
// schema. Anything else is discarded by the parser.
const stateLogExtractorPrompt = `You maintain a structured log of what we know about the current agent session.
Read the conversation delta below and output ONLY a JSON object matching this schema (omit empty fields):

{"goal": "...", "decisions": ["..."], "open_issues": ["..."], "files": {"path": "fact"}, "tools": {"name": count}}

Rules:
- Output JSON only, no prose, no code fences.
- Include only NEW facts learned from the delta; skip what would be a duplicate.
- Keep each string under 200 chars.

Delta:
`

// extractStateLog asks llm to produce a StateLog JSON for the given
// transcript. Returns nil on error or when the response cannot be parsed
// as JSON — the caller treats this as a no-op.
func extractStateLog(ctx context.Context, llm model.LLM, transcript string) *StateLog {
	if llm == nil || strings.TrimSpace(transcript) == "" {
		return nil
	}
	req := &model.LLMRequest{
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: stateLogExtractorPrompt + transcript}}},
		},
	}
	seq := llm.GenerateContent(ctx, req, false)
	var out string
	for resp, err := range seq {
		if err != nil {
			return nil
		}
		if resp == nil || resp.Content == nil {
			continue
		}
		for _, p := range resp.Content.Parts {
			out += p.Text
		}
	}
	out = strings.TrimSpace(out)
	// Tolerate fenced code blocks even though the prompt forbids them.
	out = strings.TrimPrefix(out, "```json")
	out = strings.TrimPrefix(out, "```")
	out = strings.TrimSuffix(out, "```")
	out = strings.TrimSpace(out)
	if !strings.HasPrefix(out, "{") {
		return nil
	}
	var sl StateLog
	if err := json.Unmarshal([]byte(out), &sl); err != nil {
		return nil
	}
	return &sl
}

// persistStateLog writes the JSON form of sl to path. Best-effort: errors
// are returned for the caller to log but do not block the agent loop.
func persistStateLog(path string, sl *StateLog) error {
	if path == "" || sl == nil {
		return nil
	}
	data, err := json.MarshalIndent(sl, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

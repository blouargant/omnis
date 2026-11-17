// memory_review.go — /memory review: one model call proposes clean-up
// operations on a collection's memories (merge duplicates, rewrite or delete a
// memory a more recent one contradicts), and the host adds a "check" for every
// project fact or reference nobody said or changed for 90 days. Nothing is
// written here: the user approves each operation in the web UI and the server
// applies the approved ones (server/collection_memories.go). Same one-off-LLM
// pattern as the goal evaluator: no runner, tools or event bus.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"

	"github.com/blouargant/omnis/internal/collmem"
)

// Review operations.
const (
	ReviewMerge  = "merge"  // names ≥ 2 → into
	ReviewUpdate = "update" // one name → into (same name)
	ReviewDelete = "delete" // names ≥ 1
	ReviewCheck  = "check"  // one old project fact / reference: still true?
)

// MemoryStaleDays is the age (since last said or changed) past which a project
// fact or a reference gets a "check" operation. Same threshold as the web UI.
const MemoryStaleDays = 90

// maxReviewOps bounds the model's proposals (checks are added on top).
const maxReviewOps = 20

// ReviewInto is the memory an operation writes.
type ReviewInto struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description"`
	Content     string `json:"content"`
}

// ReviewOp is one proposed clean-up step. Versions pins the state of each
// memory the operation touches, so applying it after a change is refused.
type ReviewOp struct {
	Op       string            `json:"op"`
	Names    []string          `json:"names"`
	Into     *ReviewInto       `json:"into,omitempty"`
	Reason   string            `json:"reason"`
	Versions map[string]string `json:"versions,omitempty"`
}

// MemoryVersion identifies one state of a memory: a review proposal is only
// applied to the state it was made from.
func MemoryVersion(m collmem.Memory) string {
	return fmt.Sprintf("%s|%s|%d|%s|%d", m.Updated, m.LastConfirmed, m.Confirmations, m.Type, len(m.Body))
}

const memoryReviewPrompt = `You review the memory of one collection: small notes an assistant saved from what the user said, to apply in later conversations. Propose a short list of clean-up operations. The user approves or rejects each one; nothing changes without them.

Operations:
- "merge": two or more memories say the same thing or cover one subject. Give the merged memory in "into": content that combines theirs, nothing lost, nothing added. Its name may be one of theirs.
- "update": a memory is contradicted by a MORE RECENT memory but also holds other things that are still true. Rewrite it in "into" (same name) without the outdated part. When the more recent memory already says everything that remains true, propose "delete" instead: never rewrite a memory into a copy of another.
- "delete": a memory that a more recent one contradicts entirely, that another memory fully covers, or that only concerns a one-off task (one event's date or place, one draft) rather than something later conversations need.

Rules:
- The most recent memory is right when two disagree (saved, updated and restated dates are given).
- Use only the text of the memories. Never add a fact, a rule or a detail. Keep the user's wording.
- Leave alone every memory that is fine. Age alone is not a reason to change anything. Fewer good proposals beat many. No proposals is a valid answer.
- A memory appears in at most one operation.
- "reason": one short sentence, in the language the memories are written in.
- "into.type" is one of feedback, user, project, reference (keep the original type unless the merged memories differ). "into.description" is one line.

Reply with JSON only, no prose:
{"ops":[{"op":"merge","names":["a","b"],"into":{"name":"a","type":"feedback","description":"…","content":"…"},"reason":"…"},{"op":"delete","names":["c"],"reason":"…"}]}`

// buildMemoryReviewRequest renders the memories with their dates.
func buildMemoryReviewRequest(mems []collmem.Memory, today string) *model.LLMRequest {
	var b strings.Builder
	b.WriteString("Today is " + today + ".\n\nMEMORIES:\n")
	for _, m := range mems {
		fmt.Fprintf(&b, "\n## %s (%s) — %s\n", m.Name, m.Type, m.Description)
		fmt.Fprintf(&b, "saved %s, updated %s", m.Created, m.Updated)
		if m.Confirmations > 0 {
			fmt.Fprintf(&b, ", restated %d× (last %s)", m.Confirmations, m.LastConfirmed)
		}
		b.WriteString("\n" + m.Body + "\n")
	}
	return &model.LLMRequest{
		Config: &genai.GenerateContentConfig{
			SystemInstruction: &genai.Content{Parts: []*genai.Part{{Text: memoryReviewPrompt}}},
		},
		Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: b.String()}}}},
	}
}

// parseMemoryReview extracts {"ops":[…]} from the reply (fences and prose
// around it are tolerated).
func parseMemoryReview(text string) ([]ReviewOp, error) {
	start, end := strings.Index(text, "{"), strings.LastIndex(text, "}")
	if start < 0 || end <= start {
		return nil, errors.New("the review returned no JSON")
	}
	var out struct {
		Ops []ReviewOp `json:"ops"`
	}
	if err := json.Unmarshal([]byte(text[start:end+1]), &out); err != nil {
		return nil, fmt.Errorf("the review returned invalid JSON: %w", err)
	}
	return out.Ops, nil
}

// validateReviewOps keeps the operations that name existing memories (each in
// one operation at most) and write a valid memory, pins their versions, and
// appends a "check" for every stale project fact or reference left untouched.
func validateReviewOps(ops []ReviewOp, mems []collmem.Memory, today time.Time) []ReviewOp {
	byName := map[string]collmem.Memory{}
	for _, m := range mems {
		byName[m.Name] = m
	}
	used := map[string]bool{}
	var out []ReviewOp
	for _, op := range ops {
		if len(out) >= maxReviewOps {
			break
		}
		names, ok := reviewNames(op.Names, byName, used)
		if !ok {
			continue
		}
		switch op.Op {
		case ReviewMerge:
			if len(names) < 2 || !validInto(op.Into) {
				continue
			}
			// The merged name may be a new one, but never another memory's.
			if _, exists := byName[op.Into.Name]; exists && !containsName(names, op.Into.Name) {
				continue
			}
		case ReviewUpdate:
			if len(names) != 1 || !validInto(op.Into) || op.Into.Name != names[0] {
				continue
			}
		case ReviewDelete:
			op.Into = nil
		default:
			continue
		}
		op.Names = names
		op.Reason = strings.TrimSpace(op.Reason)
		op.Versions = map[string]string{}
		for _, n := range names {
			used[n] = true
			op.Versions[n] = MemoryVersion(byName[n])
		}
		out = append(out, op)
	}
	for _, m := range mems {
		if !used[m.Name] && MemoryIsStale(m, today) {
			out = append(out, ReviewOp{Op: ReviewCheck, Names: []string{m.Name},
				Versions: map[string]string{m.Name: MemoryVersion(m)}})
		}
	}
	return out
}

// reviewNames normalises names and checks each exists and is not yet used.
func reviewNames(raw []string, byName map[string]collmem.Memory, used map[string]bool) ([]string, bool) {
	seen := map[string]bool{}
	var names []string
	for _, r := range raw {
		n, ok := collmem.NormalizeName(r)
		if !ok || used[n] || seen[n] {
			return nil, false
		}
		if _, exists := byName[n]; !exists {
			return nil, false
		}
		seen[n] = true
		names = append(names, n)
	}
	return names, len(names) > 0
}

func validInto(in *ReviewInto) bool {
	if in == nil || strings.TrimSpace(in.Content) == "" || !collmem.ValidType(strings.TrimSpace(in.Type)) {
		return false
	}
	n, ok := collmem.NormalizeName(in.Name)
	if !ok {
		return false
	}
	in.Name, in.Type = n, strings.TrimSpace(in.Type)
	return len([]rune(in.Content)) <= collmem.MaxBodyChars
}

func containsName(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// MemoryIsStale reports a project fact or reference nobody said or changed for
// more than MemoryStaleDays before today.
func MemoryIsStale(m collmem.Memory, today time.Time) bool {
	if m.Type != collmem.TypeProject && m.Type != collmem.TypeReference {
		return false
	}
	last, err := time.Parse("2006-01-02", m.LastActivity())
	if err != nil {
		return false
	}
	return today.Sub(last) > MemoryStaleDays*24*time.Hour
}

// reviewTimeout bounds the model call: the review is user-initiated and the
// user waits for it, but a whole collection may be long.
const reviewTimeout = 3 * time.Minute

// ReviewMemories asks the current generation's leader model for clean-up
// operations on mems. Checks for stale memories are added even when the model
// call fails or proposes nothing, so err is reported alongside them.
func (m *Manager) ReviewMemories(ctx context.Context, mems []collmem.Memory) ([]ReviewOp, error) {
	today := time.Now()
	var ops []ReviewOp
	var callErr error
	if len(mems) >= 2 {
		ops, callErr = m.proposeReview(ctx, mems, today.Format("2006-01-02"))
	}
	return validateReviewOps(ops, mems, today), callErr
}

func (m *Manager) proposeReview(ctx context.Context, mems []collmem.Memory, today string) ([]ReviewOp, error) {
	if m == nil {
		return nil, errors.New("no agent")
	}
	inst := m.Current()
	if inst == nil {
		return nil, errors.New("no agent generation")
	}
	mdl, err := newModelForAgent(ctx, inst.LeaderCfg)
	if err != nil {
		return nil, err
	}
	cctx, cancel := context.WithTimeout(ctx, reviewTimeout)
	defer cancel()
	var text strings.Builder
	for resp, gerr := range mdl.GenerateContent(cctx, buildMemoryReviewRequest(mems, today), false) {
		if gerr != nil {
			return nil, gerr
		}
		if resp != nil && resp.Content != nil {
			for _, p := range resp.Content.Parts {
				if !p.Thought {
					text.WriteString(p.Text)
				}
			}
		}
	}
	return parseMemoryReview(text.String())
}

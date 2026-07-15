// collection_memory.go — the Phase-2 collection-memory distiller. Given a
// collection's CURRENT memory plus material gathered from its RECENT sessions, a
// single non-streamed completion on the cheap evaluator model (eval_model_ref,
// falling back to the leader model — same resolution as the /goal evaluator)
// produces an UPDATED, reconciled, bounded memory block. This is the same
// one-off-LLM pattern as EvaluateGoal / GenerateTitle: no runner, tools, or event
// bus, so nothing reaches the SSE stream.
//
// Crucially this only RETURNS a proposal — it never writes memory.md. The caller
// (the server distill route) hands the proposal to the user to review and accept,
// so an evolving collection memory can never silently poison every new chat with a
// stale or wrong fact (the "recalled memory that is wrong" hazard).
package agent

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/adk/model"
	"google.golang.org/genai"
)

// collectionMemoryOutputCap bounds the distilled memory — it is injected into the
// system instruction of EVERY chat in the collection, so it must stay small.
const collectionMemoryOutputCap = 6000

// collectionMaterialCap bounds the recent-sessions material fed to the distiller
// (the caller also caps what it gathers; this is defence in depth). The material
// is ordered most-recent-first, so the HEAD is the freshest and most relevant.
const collectionMaterialCap = 24000

const collectionMemorySystemPrompt = "You maintain a concise, durable MEMORY for a collection of related chat " +
	"sessions — a single workstream (e.g. a client, a project, a research topic). You are given the CURRENT MEMORY " +
	"and RECENT SESSIONS from the collection. Produce an UPDATED MEMORY a teammate could read to get up to speed.\n" +
	"Rules:\n" +
	"- MERGE new durable facts from the recent sessions into the current memory: repositories and stacks involved, " +
	"decisions taken, conventions, constraints, named systems/people, environment details.\n" +
	"- SUPERSEDE or REMOVE anything the recent sessions contradict or make obsolete. Do NOT simply append — the memory " +
	"must stay internally consistent and must not grow without bound.\n" +
	"- Keep only STABLE, reusable facts about the workstream. Drop one-off task details, questions, chit-chat, and " +
	"anything transient or already resolved.\n" +
	"- Be concise: a short markdown bullet list (group under short headings only if it genuinely helps). Output the " +
	"memory itself and nothing else — no preamble, no 'here is the updated memory'.\n" +
	"- If nothing in the recent sessions is worth remembering, return the current memory unchanged (or empty if it was empty)."

// DistillCollectionMemory reconciles a collection's current memory with material
// gathered from its recent sessions and returns an updated, bounded memory block
// as a PROPOSAL (the caller decides whether to apply it). It never writes to disk.
func (m *Manager) DistillCollectionMemory(ctx context.Context, currentMemory, material string) (string, error) {
	if m == nil {
		return "", fmt.Errorf("no manager")
	}
	inst := m.Current()
	if inst == nil {
		return "", fmt.Errorf("no agent generation available")
	}
	mdl, err := m.evalModel(ctx, inst)
	if err != nil || mdl == nil {
		return "", fmt.Errorf("no model available for distillation: %w", err)
	}

	if strings.TrimSpace(material) == "" {
		return "", fmt.Errorf("no session material to distill")
	}
	req := buildDistillRequest(currentMemory, material)

	var out strings.Builder
	for resp, gerr := range mdl.GenerateContent(ctx, req, false) {
		if gerr != nil {
			return "", fmt.Errorf("distillation failed: %w", gerr)
		}
		if resp == nil || resp.Content == nil {
			continue
		}
		for _, p := range resp.Content.Parts {
			out.WriteString(p.Text)
		}
	}

	res := strings.TrimSpace(out.String())
	if r := []rune(res); len(r) > collectionMemoryOutputCap {
		res = strings.TrimSpace(string(r[:collectionMemoryOutputCap]))
	}
	return res, nil
}

// buildDistillRequest assembles the one-off LLM request from the current memory
// and the gathered material, applying the material input cap (the material is
// most-recent-first, so it keeps the HEAD). Extracted so the capping + prompt
// shape are unit-testable without a live model.
func buildDistillRequest(currentMemory, material string) *model.LLMRequest {
	material = strings.TrimSpace(material)
	if r := []rune(material); len(r) > collectionMaterialCap {
		material = string(r[:collectionMaterialCap]) + "\n…(older sessions omitted)…"
	}
	currentMemory = strings.TrimSpace(currentMemory)

	var user strings.Builder
	user.WriteString("CURRENT MEMORY:\n")
	if currentMemory == "" {
		user.WriteString("(empty — this collection has no memory yet)\n")
	} else {
		user.WriteString(currentMemory)
		user.WriteString("\n")
	}
	user.WriteString("\nRECENT SESSIONS (most recent first):\n")
	user.WriteString(material)

	return &model.LLMRequest{
		Config: &genai.GenerateContentConfig{
			SystemInstruction: &genai.Content{Parts: []*genai.Part{{Text: collectionMemorySystemPrompt}}},
		},
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: user.String()}}},
		},
	}
}

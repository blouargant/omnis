package agent

import (
	adkagent "google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/model"
	"google.golang.org/adk/plugin"
	"google.golang.org/genai"

	fstools "github.com/blouargant/omnis/core/tools"
	"github.com/blouargant/omnis/internal/agentmd"
)

// agentMDPlugin builds the runner-level plugin that injects AGENT.md project
// memory into the leader/root system instruction on every turn. It resolves
// the AGENT.md hierarchy against the session's working directory (the same cwd
// the file-system tools and Folders panel use) and prepends the rendered block
// to req.Config.SystemInstruction. With no AGENT.md anywhere it is a no-op, so
// behaviour is unchanged for projects that don't use the feature.
func agentMDPlugin(name string) (*plugin.Plugin, error) {
	return plugin.New(plugin.Config{
		Name:                name,
		BeforeModelCallback: llmagent.BeforeModelCallback(injectAgentMD),
	})
}

func injectAgentMD(ctx adkagent.CallbackContext, req *model.LLMRequest) (*model.LLMResponse, error) {
	if req == nil {
		return nil, nil
	}
	cwd := fstools.CwdFor(ctx, ctx.SessionID())
	prependAgentMD(req, agentmd.Resolve(cwd))
	return nil, nil
}

// prependAgentMD prepends block to req's system instruction. An empty block or
// nil request is a no-op.
func prependAgentMD(req *model.LLMRequest, block string) {
	if req == nil || block == "" {
		return
	}
	if req.Config == nil {
		req.Config = &genai.GenerateContentConfig{}
	}
	part := &genai.Part{Text: block + "\n\n"}
	if req.Config.SystemInstruction == nil {
		req.Config.SystemInstruction = &genai.Content{Parts: []*genai.Part{part}}
		return
	}
	req.Config.SystemInstruction.Parts = append(
		[]*genai.Part{part}, req.Config.SystemInstruction.Parts...,
	)
}

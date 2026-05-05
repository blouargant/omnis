// Package softskills exposes a second skill toolset reading from a
// dedicated directory of *learned* procedures (as opposed to the curated
// `skills/` directory of authored procedures).
//
// Layout: `<dir>/<name>/SKILL.md`. The directory is flat (same shape as
// `skills/`); the YAML frontmatter accepts only `name` and `description`
// (the upstream skill loader rejects unknown fields). Human grouping
// lives exclusively in `softskills/INDEX.md`. The lead model
// discovers softskills through `list_softskills` / `load_softskill` and
// treats them as lower-trust hints distilled from past sessions.
package softskills

import (
	"context"
	"fmt"
	"os"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/skilltoolset"
	"google.golang.org/adk/tool/skilltoolset/skill"
	"google.golang.org/genai"
)

// DefaultDir is the root softskills directory used when none is supplied.
const DefaultDir = "softskills"

// Renamed tool surface — lets us mount this toolset alongside the authored
// `skilltoolset.New` (which exposes `list_skills` / `load_skill`) without
// name collisions and signals provenance to the model.
const (
	listToolName     = "list_softskills"
	loadToolName     = "load_softskill"
	resourceToolName = "load_softskill_resource"
)

const instruction = `You also have access to **soft-skills**: learned procedures distilled by a curator agent from past sessions. They live alongside authored skills but are auto-generated, so treat them as helpful hints rather than authoritative documentation.

Tool protocol:

1. Call ` + "`" + listToolName + "`" + ` once at the start of any non-trivial task to discover relevant learned procedures (cheap — only frontmatter is returned).
2. If a soft-skill looks relevant, call ` + "`" + loadToolName + "`" + ` with ` + "`name=\"<SOFTSKILL_NAME>\"`" + ` (the parameter is literally ` + "`name`" + `, not ` + "`skill_name`" + `) before planning.
3. Use ` + "`" + resourceToolName + "`" + ` to read files inside a soft-skill directory (` + "`references/*`, `assets/*`, `scripts/*`" + `).
4. If a soft-skill conflicts with an authored skill or a tool's own documentation, prefer the authored source and mention the conflict in your reply.

IMPORTANT — do NOT use ` + "`load_skill`" + ` to open names returned by ` + "`" + listToolName + "`" + `. ` + "`load_skill`" + ` reads the authored ` + "`skills/`" + ` directory and will return "skill not found" for soft-skills, which live in ` + "`softskills/`" + ` and are only reachable through ` + "`" + loadToolName + "`" + `.
`

// Toolset returns an ADK tool.Toolset reading softskills from `dir`.
// `dir` is created if missing.
func Toolset(ctx context.Context, dir string) (tool.Toolset, error) {
	if dir == "" {
		dir = DefaultDir
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("softskills dir: %w", err)
	}
	src := skill.NewFileSystemSource(os.DirFS(dir))

	inner, err := skilltoolset.New(ctx, skilltoolset.Config{
		Source:            src,
		Name:              "softskills",
		SystemInstruction: instruction,
	})
	if err != nil {
		return nil, fmt.Errorf("softskills toolset: %w", err)
	}
	return &renamedToolset{SkillToolset: inner}, nil
}

// renamedToolset wraps the upstream skilltoolset so the three tools it
// produces are exposed under softskill-specific names. The embedded
// *skilltoolset.SkillToolset still satisfies ProcessRequest (which injects
// the system instruction + frontmatter XML into the LLM request) — we only
// override Tools() to re-wrap each tool with a new name.
type renamedToolset struct {
	*skilltoolset.SkillToolset
}

// Tools overrides the embedded method to rename each underlying tool.
func (r *renamedToolset) Tools(ctx agent.ReadonlyContext) ([]tool.Tool, error) {
	inner, err := r.SkillToolset.Tools(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]tool.Tool, 0, len(inner))
	for _, t := range inner {
		newName, ok := renameMap[t.Name()]
		if !ok {
			out = append(out, t)
			continue
		}
		rt, err := wrap(t, newName)
		if err != nil {
			return nil, err
		}
		out = append(out, rt)
	}
	return out, nil
}

var renameMap = map[string]string{
	"list_skills":         listToolName,
	"load_skill":          loadToolName,
	"load_skill_resource": resourceToolName,
}

// runnableTool mirrors the unexported interface ADK type-asserts on when
// dispatching tool calls. We must implement Declaration(), Run() and
// ProcessRequest() so the framework treats the wrapper as a callable tool
// (the LLM flow's toolPreprocess step rejects any tool that doesn't
// implement ProcessRequest).
type runnableTool interface {
	tool.Tool
	Declaration() *genai.FunctionDeclaration
	Run(ctx tool.Context, args any) (map[string]any, error)
	ProcessRequest(ctx tool.Context, req *model.LLMRequest) error
}

type renamedTool struct {
	runnableTool
	name string
	decl *genai.FunctionDeclaration
}

func (rt *renamedTool) Name() string                            { return rt.name }
func (rt *renamedTool) Declaration() *genai.FunctionDeclaration { return rt.decl }

// ProcessRequest registers the *renamed* declaration with the LLM
// request. The embedded runnableTool.ProcessRequest would otherwise pack
// the underlying tool's original name (the upstream functionTool calls
// PackTool with itself as receiver), defeating the rename. We replicate
// PackTool's behaviour here against the wrapper. Run() dispatch keys off
// req.Tools[name], so storing the wrapper under the new name routes
// invocations back through us — and we forward Run() to the embedded
// tool transparently.
func (rt *renamedTool) ProcessRequest(_ tool.Context, req *model.LLMRequest) error {
	if req.Tools == nil {
		req.Tools = make(map[string]any)
	}
	if _, ok := req.Tools[rt.name]; ok {
		return fmt.Errorf("duplicate tool: %q", rt.name)
	}
	req.Tools[rt.name] = rt
	if req.Config == nil {
		req.Config = &genai.GenerateContentConfig{}
	}
	var funcTool *genai.Tool
	for _, t := range req.Config.Tools {
		if t != nil && t.FunctionDeclarations != nil {
			funcTool = t
			break
		}
	}
	if funcTool == nil {
		req.Config.Tools = append(req.Config.Tools, &genai.Tool{
			FunctionDeclarations: []*genai.FunctionDeclaration{rt.decl},
		})
	} else {
		funcTool.FunctionDeclarations = append(funcTool.FunctionDeclarations, rt.decl)
	}
	return nil
}

func wrap(t tool.Tool, newName string) (tool.Tool, error) {
	rt, ok := t.(runnableTool)
	if !ok {
		return nil, fmt.Errorf("softskills: tool %q is not runnable", t.Name())
	}
	origDecl := rt.Declaration()
	if origDecl == nil {
		return nil, fmt.Errorf("softskills: tool %q has no declaration", t.Name())
	}
	cloned := *origDecl
	cloned.Name = newName
	return &renamedTool{runnableTool: rt, name: newName, decl: &cloned}, nil
}

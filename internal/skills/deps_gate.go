package skills

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/skilltoolset"
	"google.golang.org/genai"

	"github.com/blouargant/yoke/internal/deps"
	"github.com/blouargant/yoke/internal/paths"
)

// DepGate is invoked after a skill is loaded via load_skill. It receives the
// tool context and the loaded skill's name, and returns a human-readable notice
// to attach to the load_skill result when one of the skill's declared
// dependencies is unavailable (empty = nothing to report). This is the seam
// where the host enforces dependency installation (check → ask → install →
// recheck); the implementation lives in the agent package, which owns the
// ask-user registry and the Bash installer.
type DepGate func(ctx tool.Context, skillName string) string

// The gate is process-wide: the ask-user registry and Bash installer live on
// the (singleton, hot-reload-surviving) Infrastructure, so a single setter keeps
// skills.Toolset's signature — and all its callers — unchanged. Set once at
// startup; read on every Toolset build.
var (
	depGateMu sync.RWMutex
	depGateFn DepGate
)

// SetDepGate installs the process-wide dependency gate. Passing nil disables
// gating (skills load exactly as before — the byte-identical no-gate path).
func SetDepGate(g DepGate) {
	depGateMu.Lock()
	depGateFn = g
	depGateMu.Unlock()
}

func depGate() DepGate {
	depGateMu.RLock()
	defer depGateMu.RUnlock()
	return depGateFn
}

// RequiresFor returns the runtime dependencies declared by the named skill,
// resolved across the skills search chain (first layer whose SKILL.md exists
// wins, matching the loader's precedence). Returns nil when the skill or the
// declaration is absent.
//
// Dependencies live in a `requires.json` sidecar next to SKILL.md — NOT in the
// SKILL.md frontmatter — because ADK's skill loader parses that frontmatter
// with KnownFields(true) and rejects any field it doesn't know (name,
// description, license, compatibility, metadata, allowed-tools). The sidecar
// mirrors the per-skill permissions.json pattern. Shape:
//
//	{ "requires": [ { "command": "lit", "label": "LiteParse",
//	                  "install": "pip install liteparse" } ] }
//
// `install` accepts a string or a per-OS object ({linux:…, darwin:…}).
func RequiresFor(name string) ([]deps.Requirement, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, nil
	}
	for _, dir := range paths.SkillsAllSearchDirs() {
		skillDir := filepath.Join(dir, name)
		if _, err := os.Stat(filepath.Join(skillDir, "SKILL.md")); err != nil {
			continue // this layer doesn't own the skill; keep first-match precedence
		}
		b, err := os.ReadFile(filepath.Join(skillDir, "requires.json"))
		if err != nil {
			return nil, nil // skill exists here but declares no dependencies
		}
		return parseRequiresJSON(b)
	}
	return nil, nil
}

func parseRequiresJSON(b []byte) ([]deps.Requirement, error) {
	var doc struct {
		Requires []deps.Requirement `json:"requires"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, err
	}
	out := make([]deps.Requirement, 0, len(doc.Requires))
	for _, r := range doc.Requires {
		if strings.TrimSpace(r.Command) != "" {
			out = append(out, r)
		}
	}
	return out, nil
}

// gatedSkillToolset wraps the upstream skilltoolset so the load_skill tool runs
// the dependency gate after the skill body is fetched. It embeds the concrete
// *SkillToolset so Name()/ProcessRequest (system-instruction + frontmatter
// injection) are inherited unchanged; only Tools() is overridden to decorate
// the one tool.
type gatedSkillToolset struct {
	*skilltoolset.SkillToolset
	gate DepGate
}

func (g *gatedSkillToolset) Tools(ctx agent.ReadonlyContext) ([]tool.Tool, error) {
	inner, err := g.SkillToolset.Tools(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]tool.Tool, 0, len(inner))
	for _, t := range inner {
		if t.Name() == "load_skill" {
			if wt, werr := newGatedLoadTool(t, g.gate); werr == nil {
				out = append(out, wt)
				continue
			}
		}
		out = append(out, t)
	}
	return out, nil
}

type runnableSkillTool interface {
	tool.Tool
	Declaration() *genai.FunctionDeclaration
	Run(ctx tool.Context, args any) (map[string]any, error)
	ProcessRequest(ctx tool.Context, req *model.LLMRequest) error
}

// gatedLoadTool keeps load_skill's name and declaration but runs the dependency
// gate after the inner tool returns, attaching any "dependency_status" notice
// to the result.
type gatedLoadTool struct {
	runnableSkillTool
	gate DepGate
	decl *genai.FunctionDeclaration
}

func newGatedLoadTool(t tool.Tool, gate DepGate) (tool.Tool, error) {
	rt, ok := t.(runnableSkillTool)
	if !ok {
		return nil, fmt.Errorf("skills: load_skill is not a runnable tool")
	}
	decl := rt.Declaration()
	if decl == nil {
		return nil, fmt.Errorf("skills: load_skill has no declaration")
	}
	return &gatedLoadTool{runnableSkillTool: rt, gate: gate, decl: decl}, nil
}

func (g *gatedLoadTool) Run(ctx tool.Context, args any) (map[string]any, error) {
	out, err := g.runnableSkillTool.Run(ctx, args)
	if err != nil || g.gate == nil {
		return out, err
	}
	name := skillNameFromArgs(args)
	if name == "" {
		return out, nil
	}
	if notice := g.gate(ctx, name); notice != "" {
		if out == nil {
			out = map[string]any{}
		}
		out["dependency_status"] = notice
	}
	return out, nil
}

// ProcessRequest packs THIS wrapper under the load_skill name so the runner
// dispatches calls through Run (which runs the gate). Packing the embedded tool
// instead — what the inherited ProcessRequest would do — keys req.Tools to the
// inner tool and bypasses the gate. Mirrors softskills' renamedTool.
func (g *gatedLoadTool) ProcessRequest(_ tool.Context, req *model.LLMRequest) error {
	if req.Tools == nil {
		req.Tools = make(map[string]any)
	}
	name := g.Name()
	if _, ok := req.Tools[name]; ok {
		return fmt.Errorf("duplicate tool: %q", name)
	}
	req.Tools[name] = g
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
			FunctionDeclarations: []*genai.FunctionDeclaration{g.decl},
		})
	} else {
		funcTool.FunctionDeclarations = append(funcTool.FunctionDeclarations, g.decl)
	}
	return nil
}

func skillNameFromArgs(args any) string {
	if m, ok := args.(map[string]any); ok {
		if s, ok := m["name"].(string); ok {
			return strings.TrimSpace(s)
		}
	}
	if b, err := json.Marshal(args); err == nil {
		var m struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(b, &m) == nil {
			return strings.TrimSpace(m.Name)
		}
	}
	return ""
}

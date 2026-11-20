package agent

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unsafe"

	adkagent "google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"

	"github.com/blouargant/omnis/internal/lessons"
)

// buildLessonFleet builds a real generation: a "default" squad led by leader
// with one member "worker". routerSquad is written into agents.json as is.
func buildLessonFleet(t *testing.T, routerSquad string) (*Instance, string) {
	t.Helper()
	layer := t.TempDir()
	home := t.TempDir()
	t.Setenv("OMNIS_HOME", home)
	t.Setenv("OMNIS_SYSTEM_CONFIG_DIR", layer)
	t.Setenv("OMNIS_CONFIG_DIRS", layer)
	t.Setenv("OMNIS_AGENTSKILLS_DIR", "")
	t.Setenv("OMNIS_LESSONS", "")
	SetHiddenSessionResolver(nil)
	SetCollectionRootResolver(nil)

	writeAgentDef(t, layer, "leader", `{"name":"leader","enabled":true,"leader":true,"description":"Coordinator.","tools":[]}`)
	writeAgentDef(t, layer, "worker", `{"name":"worker","enabled":true,"description":"Does the work.","tools":[]}`)
	agentsJSON := `{"agents":["leader","worker"],"router_squad":` + routerSquad + `,
		"squads":[{"name":"default","leader":"leader","members":["worker"]}]}`
	if err := os.WriteFile(filepath.Join(layer, "agents.json"), []byte(agentsJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	opts := Options{DeferModelErrors: true}
	infra, err := BuildInfrastructure(ctx, opts)
	if err != nil {
		t.Fatalf("build infra: %v", err)
	}
	t.Cleanup(func() { infra.Close() })
	inst, err := BuildInstance(ctx, infra, opts, 1)
	if err != nil {
		t.Fatalf("build instance: %v", err)
	}
	return inst, home
}

// subAgentAfterTools reads a built agent's after-tool callbacks. ADK keeps them
// in an unexported field; the assertion that matters is that the BUILT agent
// carries the lesson callback after the shaper, so read them there.
func subAgentAfterTools(t *testing.T, a adkagent.Agent) []llmagent.AfterToolCallback {
	t.Helper()
	v := reflect.ValueOf(a)
	for v.Kind() == reflect.Ptr || v.Kind() == reflect.Interface {
		v = v.Elem()
	}
	f := v.FieldByName("afterToolCallbacks")
	if !f.IsValid() {
		t.Fatalf("built agent has no afterToolCallbacks field (ADK layout changed?): %T", a)
	}
	f = reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem()
	cbType := reflect.TypeOf(llmagent.AfterToolCallback(nil))
	out := make([]llmagent.AfterToolCallback, 0, f.Len())
	for i := 0; i < f.Len(); i++ {
		out = append(out, f.Index(i).Convert(cbType).Interface().(llmagent.AfterToolCallback))
	}
	return out
}

func hugeKnownError(t *testing.T, root, agent string) (map[string]any, map[string]any) {
	t.Helper()
	text := lessonTestErr + " " + strings.Repeat("x", 100_000)
	sig := lessons.Signature(lessons.ToolKey("Bash", lessonTestArgs), text)
	if err := lessons.In(root).Save(lessons.Lesson{
		Agent: agent, Tool: "Bash", Signature: sig, Cue: "when listing every namespace",
		Text: "use -A", Worked: `{"command":"kubectl get pods -A"}`, State: lessons.StateHint,
	}); err != nil {
		t.Fatal(err)
	}
	return lessonTestArgs, map[string]any{"output": text}
}

func assertShapedWithHint(t *testing.T, out map[string]any) {
	t.Helper()
	if out == nil {
		t.Fatal("no rewritten result")
	}
	if s, _ := out["output"].(string); len(s) >= 100_000 {
		t.Fatalf("result not shaped: %d chars", len(s))
	}
	if h, _ := out[lessonHintKey].(string); !strings.Contains(h, "use -A") || !strings.Contains(h, `kubectl get pods -A`) {
		t.Fatalf("lesson missing or cut: %q", out[lessonHintKey])
	}
}

func TestSubAgentsCarryTheLessonCallbackAfterTheShaper(t *testing.T) {
	inst, home := buildLessonFleet(t, `"none"`)
	sq := inst.Squad("default")
	if sq == nil || sq.SubAgents["worker"] == nil {
		t.Fatal("worker not built")
	}
	cbs := subAgentAfterTools(t, sq.SubAgents["worker"])
	args, result := hugeKnownError(t, home, "worker")
	out, err := cbs[len(cbs)-1](newLessonCtx("s1", "inv1"), namedTool{name: "Bash"}, args, result, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertShapedWithHint(t, out)
}

func codingPluginAfterTool(sq *SquadInstance) llmagent.AfterToolCallback {
	for _, p := range sq.Plugins {
		if p.Name() == "coding_efficiency" {
			return p.AfterToolCallback()
		}
	}
	return nil
}

func TestRootCarriesTheLessonCallback(t *testing.T) {
	inst, home := buildLessonFleet(t, `"none"`)
	sq := inst.Squad("default")
	cb := codingPluginAfterTool(sq)
	if cb == nil {
		t.Fatal("no coding_efficiency plugin on the root")
	}
	args, result := hugeKnownError(t, home, "leader")
	out, err := cb(newLessonCtx("s1", "inv1"), namedTool{name: "Bash"}, args, result, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertShapedWithHint(t, out)
}

func TestRouterHasNoLessonCallback(t *testing.T) {
	inst, _ := buildLessonFleet(t, `"omnis"`)
	if inst.RouterName == "" {
		t.Fatal("router not built")
	}
	sq := inst.Squad(inst.RouterName)
	// The hint only ever rides inside the coding-efficiency plugin (and the
	// prompt block in the lessons plugin): neither may be on the router.
	for _, p := range sq.Plugins {
		if p.Name() == "coding_efficiency" || p.Name() == "lessons" {
			t.Fatalf("router mounts the %q plugin", p.Name())
		}
	}
	// And the answering squad of the same generation does carry it.
	if codingPluginAfterTool(inst.Squad("default")) == nil {
		t.Fatal("the answering root lost its coding_efficiency plugin")
	}
}

package agent

import (
	"bytes"
	"log"
	"reflect"
	"strings"
	"testing"

	"github.com/blouargant/omnis/internal/claudeplugins"
)

const claudePluginsFixtureRoot = "../internal/claudeplugins/testdata/agentskills/claude"

// captureLog redirects the standard logger into a buffer for the test, so the
// log lines the conversion emits can be asserted on and never reach the test
// output.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(prev) })
	return &buf
}

func testClaudeCatalog() map[string]RuntimeModelConfig {
	return map[string]RuntimeModelConfig{"hosted": {}, "high": {}, "premium": {}}
}

// discoverDemoPlugin loads the demo plugin of the shared fixture. The fixture's
// marketplace also lists entries that discovery must refuse, so its warnings
// are expected and not asserted on here.
func discoverDemoPlugin(t *testing.T) claudeplugins.Plugin {
	t.Helper()
	plugins, _ := claudeplugins.Discover(claudePluginsFixtureRoot)
	for _, p := range plugins {
		if p.Name == "demo" {
			return p
		}
	}
	t.Fatalf("the fixture must yield the demo plugin, got %+v", plugins)
	return claudeplugins.Plugin{}
}

func agentByName(t *testing.T, agents []pluginAgent, name string) pluginAgent {
	t.Helper()
	for _, a := range agents {
		if a.Entry.Name == name {
			return a
		}
	}
	t.Fatalf("no converted agent named %q in %v", name, agentNames(agents))
	return pluginAgent{}
}

func agentNames(agents []pluginAgent) []string {
	var out []string
	for _, a := range agents {
		out = append(out, a.Entry.Name)
	}
	return out
}

func isTrue(b *bool) bool { return b != nil && *b }

func TestPluginAgentName(t *testing.T) {
	cases := []struct{ plugin, agent, want string }{
		{"iaparc", "prod-support", "iaparc_prod_support"},
		{"Demo", "Lead", "demo_lead"},
		{"my-plugin", "x", "my_plugin_x"},
	}
	for _, c := range cases {
		if got := pluginAgentName(c.plugin, c.agent); got != c.want {
			t.Errorf("pluginAgentName(%q, %q) = %q, want %q", c.plugin, c.agent, got, c.want)
		}
	}
}

func TestMapClaudeTools(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"worker", []string{"Bash", "Read", "Write", "Edit", "MultiEdit", "NotebookEdit"},
			[]string{"Bash", "Read", "Write", "Edit", "MultiEdit", "Skill"}},
		{"lead", []string{"Bash", "Read", "Agent", "AskUserQuestion", "SendMessage"},
			[]string{"Bash", "Read", "Skill"}},
		{"nil inherits all", nil,
			[]string{"Bash", "Read", "Grep", "Glob", "Write", "Edit", "MultiEdit", "Skill"}},
		{"empty inherits all", []string{},
			[]string{"Bash", "Read", "Grep", "Glob", "Write", "Edit", "MultiEdit", "Skill"}},
		{"input order kept, Skill last", []string{"Edit", "Skill", "Bash"},
			[]string{"Edit", "Bash", "Skill"}},
		{"repeats collapse", []string{"Bash", "Bash", "Read"},
			[]string{"Bash", "Read", "Skill"}},
		{"nothing mappable still gets Skill", []string{"Agent", "WebFetch"},
			[]string{"Skill"}},
	}
	for _, c := range cases {
		if got := mapClaudeTools(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: mapClaudeTools(%v) = %v, want %v", c.name, c.in, got, c.want)
		}
	}
}

func TestResolveClaudeModelRef(t *testing.T) {
	cat := testClaudeCatalog()
	cases := []struct {
		name  string
		alias string
		m     map[string]string
		want  string
		logs  bool
	}{
		{"alias in the map", "haiku", defaultClaudeModelMap, "hosted", false},
		{"sonnet", "sonnet", defaultClaudeModelMap, "high", false},
		{"opus", "opus", defaultClaudeModelMap, "premium", false},
		{"inherit", "inherit", defaultClaudeModelMap, "", false},
		{"absent", "", defaultClaudeModelMap, "", false},
		{"catalogue ref used as is", "high", defaultClaudeModelMap, "high", false},
		{"unknown value", "gpt-9", defaultClaudeModelMap, "", true},
		{"map points outside the catalogue", "haiku", map[string]string{"haiku": "nope"}, "", true},
		{"nil map still accepts a catalogue ref", "premium", nil, "premium", false},
	}
	for _, c := range cases {
		buf := captureLog(t)
		if got := resolveClaudeModelRef(c.alias, c.m, cat); got != c.want {
			t.Errorf("%s: resolveClaudeModelRef(%q) = %q, want %q", c.name, c.alias, got, c.want)
		}
		if logged := buf.Len() > 0; logged != c.logs {
			t.Errorf("%s: logged = %v, want %v (log: %q)", c.name, logged, c.logs, buf.String())
		}
	}
}

func TestConvertDemoPlugin(t *testing.T) {
	captureLog(t)
	p := discoverDemoPlugin(t)
	agents, squad := convertPlugin(p, defaultClaudeModelMap, testClaudeCatalog())

	if len(agents) != 3 {
		t.Fatalf("want 3 agents, got %v", agentNames(agents))
	}
	if squad == nil {
		t.Fatal("want a squad, got nil")
	}
	lead := agentByName(t, agents, "demo_lead")
	reader := agentByName(t, agents, "demo_reader")
	worker := agentByName(t, agents, "demo_worker")

	if squad.Name != "demo" || squad.Leader != "demo_lead" {
		t.Errorf("squad = %+v, want name demo and leader demo_lead", squad)
	}
	if want := []string{"demo_reader", "demo_worker"}; !reflect.DeepEqual(squad.Members, want) {
		t.Errorf("squad members = %v, want %v", squad.Members, want)
	}
	if squad.Description != lead.Entry.Description || squad.Description == "" {
		t.Errorf("squad description %q must be the leader's %q", squad.Description, lead.Entry.Description)
	}
	if squad.Hidden {
		t.Error("a plugin squad must be routable, not hidden")
	}

	if !isTrue(lead.Entry.Leader) || isTrue(reader.Entry.Leader) || isTrue(worker.Entry.Leader) {
		t.Error("only demo_lead must be marked leader")
	}
	for _, a := range agents {
		if !isTrue(a.Entry.Enabled) {
			t.Errorf("%s must be enabled", a.Entry.Name)
		}
		if isTrue(a.Entry.BuiltIn) {
			t.Errorf("%s must not be built in", a.Entry.Name)
		}
		if a.Plugin != "demo" {
			t.Errorf("%s: Plugin = %q, want demo", a.Entry.Name, a.Plugin)
		}
	}

	if lead.Entry.ModelRef != "high" || worker.Entry.ModelRef != "hosted" || reader.Entry.ModelRef != "premium" {
		t.Errorf("model refs lead=%q worker=%q reader=%q, want high/hosted/premium",
			lead.Entry.ModelRef, worker.Entry.ModelRef, reader.Entry.ModelRef)
	}
	if want := []string{"demo-skill"}; !reflect.DeepEqual(reader.Entry.Skills, want) {
		t.Errorf("reader skills = %v, want its own list %v", reader.Entry.Skills, want)
	}
	if want := []string{"demo-skill"}; !reflect.DeepEqual(worker.Entry.Skills, want) {
		t.Errorf("worker skills = %v, want every plugin skill %v", worker.Entry.Skills, want)
	}
	if worker.Type != "demo:worker" || lead.Type != "demo:lead" {
		t.Errorf("types = %q, %q, want demo:worker and demo:lead", worker.Type, lead.Type)
	}
	if want := []string{"Bash", "Read", "Skill"}; !reflect.DeepEqual(lead.Entry.Tools, want) {
		t.Errorf("lead tools = %v, want %v", lead.Entry.Tools, want)
	}
	if want := []string{"Bash", "Read", "Grep", "Glob", "Write", "Edit", "MultiEdit", "Skill"}; !reflect.DeepEqual(reader.Entry.Tools, want) {
		t.Errorf("reader tools = %v, want the inherited default %v", reader.Entry.Tools, want)
	}
	if worker.Entry.Description != "Makes the changes the lead hands over." {
		t.Errorf("description must be verbatim, got %q", worker.Entry.Description)
	}

	in := lead.Entry.Instruction
	if !strings.HasPrefix(in, "You are the lead of the demo squad.") {
		t.Errorf("instruction must start with the agent body, got %q", in)
	}
	if strings.Contains(in, "${CLAUDE_PLUGIN_ROOT}") {
		t.Error("instruction still holds ${CLAUDE_PLUGIN_ROOT}")
	}
	for _, want := range []string{
		p.Dir + "/skills/demo-skill/",
		"## Running inside omnis",
		"`demo:worker` is the tool `demo_worker`",
		"`demo:reader` is the tool `demo_reader`",
		"resume_session",
		"other:automation",
	} {
		if !strings.Contains(in, want) {
			t.Errorf("lead instruction lacks %q:\n%s", want, in)
		}
	}
	// Every agent of the plugin carries the note, not only the leader.
	if !strings.Contains(worker.Entry.Instruction, "## Running inside omnis") {
		t.Error("worker instruction lacks the translation note")
	}
}

func TestPluginTranslationNote(t *testing.T) {
	p := claudeplugins.Plugin{Name: "demo", Agents: []claudeplugins.Agent{
		{Name: "lead", Body: "Delegate to demo:worker, or to zeta:b and alpha:a. See zeta:b again."},
		{Name: "worker", Body: "Visit https://example.com:8080/x at 10:30, or file:///tmp/f."},
	}}
	note := pluginTranslationNote(p)

	if !strings.HasPrefix(note, "## Running inside omnis") {
		t.Errorf("note must open with its heading, got %q", note)
	}
	for _, want := range []string{
		"`demo:lead` is the tool `demo_lead`",
		"`demo:worker` is the tool `demo_worker`",
		"calling an agent with the Agent tool means calling its tool",
		"SendMessage to an agent means calling the same agent again with `resume_session` set to the `session` handle its previous call returned",
		"are not available here: tell the user instead of calling them",
	} {
		if !strings.Contains(note, want) {
			t.Errorf("note lacks %q:\n%s", want, note)
		}
	}
	// Foreign names are the <x>:<y> mentions with x != the plugin: sorted, once
	// each, and a URL, a clock time or the plugin's own agent is none of them.
	if !strings.Contains(note, "(such as `alpha:a`, `zeta:b`)") {
		t.Errorf("foreign agents must read (such as `alpha:a`, `zeta:b`):\n%s", note)
	}

	// With nothing foreign to cite the note gives no examples.
	q := claudeplugins.Plugin{Name: "demo", Agents: []claudeplugins.Agent{{Name: "solo", Body: "No one else."}}}
	if got := pluginTranslationNote(q); strings.Contains(got, "such as") {
		t.Errorf("a plugin mentioning no foreign agent must cite none:\n%s", got)
	}
}

func TestConvertPluginWithoutAgentToolHasNoSquad(t *testing.T) {
	p := claudeplugins.Plugin{Name: "demo", Dir: "/x", Agents: []claudeplugins.Agent{
		{Name: "reader", Tools: []string{"Read"}},
		{Name: "worker", Tools: []string{"Bash"}},
	}}
	agents, squad := convertPlugin(p, defaultClaudeModelMap, testClaudeCatalog())
	if squad != nil {
		t.Errorf("a plugin with no Agent-tool holder must get no squad, got %+v", squad)
	}
	if len(agents) != 2 {
		t.Fatalf("its agents must still convert, got %v", agentNames(agents))
	}
	for _, a := range agents {
		if isTrue(a.Entry.Leader) {
			t.Errorf("%s must not be a leader", a.Entry.Name)
		}
	}
}

func TestConvertPluginTwoAgentHoldersPrefersLead(t *testing.T) {
	holder := func(name string) claudeplugins.Agent {
		return claudeplugins.Agent{Name: name, Tools: []string{"Agent"}}
	}

	t.Run("lead wins over an earlier name", func(t *testing.T) {
		buf := captureLog(t)
		p := claudeplugins.Plugin{Name: "demo", Agents: []claudeplugins.Agent{holder("alpha"), holder("lead"), {Name: "zed"}}}
		agents, squad := convertPlugin(p, nil, nil)
		if squad == nil || squad.Leader != "demo_lead" {
			t.Fatalf("squad = %+v, want leader demo_lead", squad)
		}
		if want := []string{"demo_alpha", "demo_zed"}; !reflect.DeepEqual(squad.Members, want) {
			t.Errorf("members = %v, want %v", squad.Members, want)
		}
		if !isTrue(agentByName(t, agents, "demo_lead").Entry.Leader) || isTrue(agentByName(t, agents, "demo_alpha").Entry.Leader) {
			t.Error("only demo_lead must carry leader: true")
		}
		if !strings.Contains(buf.String(), "leader") {
			t.Errorf("choosing among several holders must be logged, got %q", buf.String())
		}
	})

	t.Run("no lead, the first by name", func(t *testing.T) {
		captureLog(t)
		p := claudeplugins.Plugin{Name: "demo", Agents: []claudeplugins.Agent{holder("zed"), holder("alpha")}}
		_, squad := convertPlugin(p, nil, nil)
		if squad == nil || squad.Leader != "demo_alpha" {
			t.Fatalf("squad = %+v, want leader demo_alpha", squad)
		}
		if want := []string{"demo_zed"}; !reflect.DeepEqual(squad.Members, want) {
			t.Errorf("members = %v, want %v", squad.Members, want)
		}
	})
}

func TestConvertPluginDuplicateNamesKeepsFirst(t *testing.T) {
	buf := captureLog(t)
	p := claudeplugins.Plugin{Name: "demo", Agents: []claudeplugins.Agent{
		{Name: "foo-bar", Description: "first"},
		{Name: "foo_bar", Description: "second"},
	}}
	agents, squad := convertPlugin(p, nil, nil)
	if len(agents) != 1 || agents[0].Entry.Name != "demo_foo_bar" {
		t.Fatalf("want the single agent demo_foo_bar, got %v", agentNames(agents))
	}
	if agents[0].Entry.Description != "first" || agents[0].Type != "demo:foo-bar" {
		t.Errorf("must keep the first by name, got %q / %q", agents[0].Entry.Description, agents[0].Type)
	}
	if squad != nil {
		t.Errorf("no Agent-tool holder, want no squad, got %+v", squad)
	}
	if !strings.Contains(buf.String(), "demo_foo_bar") {
		t.Errorf("the dropped duplicate must be logged, got %q", buf.String())
	}
	// The note names only what survived: the duplicate is not a second tool.
	if n := strings.Count(agents[0].Entry.Instruction, "is the tool `demo_foo_bar`"); n != 1 {
		t.Errorf("note lists demo_foo_bar %d times, want once:\n%s", n, agents[0].Entry.Instruction)
	}
}

func TestConvertPluginSkipsAgentsWithUnusableNames(t *testing.T) {
	buf := captureLog(t)
	p := claudeplugins.Plugin{Name: "demo", Agents: []claudeplugins.Agent{
		{Name: "a/b"},
		{Name: "x:y"},
		{Name: "my agent"},
		{Name: ""},
		{Name: "ok", Description: "fine"},
	}}
	agents, _ := convertPlugin(p, nil, nil)
	if len(agents) != 1 || agents[0].Entry.Name != "demo_ok" {
		t.Fatalf("only demo_ok must survive, got %v", agentNames(agents))
	}
	for _, bad := range []string{"a/b", "x:y", "my agent"} {
		if !strings.Contains(buf.String(), bad) {
			t.Errorf("skipping %q must be logged, got %q", bad, buf.String())
		}
	}
	if strings.Contains(agents[0].Entry.Instruction, "a/b") || strings.Contains(agents[0].Entry.Instruction, "x:y") {
		t.Errorf("the note must not name a skipped agent:\n%s", agents[0].Entry.Instruction)
	}
}

func TestConvertPluginLeaderViaRestrictedAgentTool(t *testing.T) {
	captureLog(t)
	p := claudeplugins.Plugin{Name: "demo", Agents: []claudeplugins.Agent{
		{Name: "boss", Tools: []string{"Read", "Agent(worker, reader)"}},
		{Name: "worker"},
	}}
	_, squad := convertPlugin(p, nil, nil)
	if squad == nil || squad.Leader != "demo_boss" {
		t.Fatalf("Agent(...) must make its holder the leader, got %+v", squad)
	}
}

func TestConvertPluginLogsUnmappedToolsOncePerAgent(t *testing.T) {
	buf := captureLog(t)
	p := claudeplugins.Plugin{Name: "demo", Agents: []claudeplugins.Agent{
		{Name: "a", Tools: []string{"Read", "Agent", "AskUserQuestion", "SendMessage", "NotebookEdit", "WebFetch"}},
		{Name: "b", Tools: []string{"Bash", "SendMessage"}},
	}}
	convertPlugin(p, nil, nil)
	out := buf.String()
	if strings.Count(out, "NotebookEdit") != 1 || !strings.Contains(out, "WebFetch") {
		t.Errorf("unmapped tools must be logged once for the agent, got %q", out)
	}
	for _, expected := range []string{"AskUserQuestion", "SendMessage", `"Agent"`} {
		if strings.Contains(out, expected) {
			t.Errorf("the expected drop %s must not be logged: %q", expected, out)
		}
	}
	if strings.Count(out, "\n") != 1 {
		t.Errorf("agent b has nothing unmapped and must log nothing: %q", out)
	}
}

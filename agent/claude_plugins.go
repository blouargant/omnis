package agent

import (
	"fmt"
	"log"
	"regexp"
	"sort"
	"strings"

	"github.com/blouargant/omnis/internal/claudeplugins"
)

// Claude Code plugins, converted. A plugin discovered by internal/claudeplugins
// holds Claude-format agents; this file turns them into the AgentEntry /
// SquadEntry values the rest of omnis already understands, so a plugin agent is
// an ordinary agent from then on (see docs/superpowers/specs/
// 2026-10-28-claude-code-plugins-design.md, §2–§5). Everything here is a pure
// function of its inputs: nothing reads or writes the disk and nothing is wired
// into the build.

// defaultClaudeModelMap is the built-in alias table: which catalogue model a
// Claude Code `model:` alias stands for. models.json's claude_model_map
// overrides it entry by entry.
var defaultClaudeModelMap = map[string]string{"haiku": "hosted", "sonnet": "high", "opus": "premium"}

// claudeNativeTools are the Claude Code tool names that have an omnis tool of
// the same name; every other name is dropped (Agent, AskUserQuestion and
// SendMessage are handled by delegation, the always-on ask tool and resumable
// sessions respectively — see the translation note).
var claudeNativeTools = map[string]bool{
	"Bash": true, "Read": true, "Grep": true, "Glob": true,
	"Write": true, "Edit": true, "MultiEdit": true,
}

// pluginOmnisNamePattern is what a converted agent name must look like: it
// becomes a tool name and a registry directory, so anything else is skipped
// rather than sanitised.
var pluginOmnisNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_]{0,63}$`)

// pluginAgentMention finds `<plugin>:<agent>` references in an agent body. The
// leading class keeps a URL host, a path or an identifier from being read as a
// plugin name; the names themselves start with a letter, so a clock time or a
// port is not one either.
var pluginAgentMention = regexp.MustCompile(`(?:^|[^A-Za-z0-9_:/.-])([a-z][a-z0-9-]*):([a-z][a-z0-9-]*)`)

// pluginAgent is one converted plugin agent.
type pluginAgent struct {
	Plugin string
	Type   string // the qualified Claude name, "iaparc:prod-support"
	Entry  AgentEntry
}

// pluginAgentName is the omnis name of a plugin's agent: `<plugin>_<agent>`,
// lower-cased, with `-` turned into `_` ("iaparc", "prod-support" →
// "iaparc_prod_support").
func pluginAgentName(plugin, agent string) string {
	return strings.ReplaceAll(strings.ToLower(plugin+"_"+agent), "-", "_")
}

// mapClaudeTools turns a Claude agent's `tools` list into omnis tool keys: the
// names omnis has an equivalent for, in the order given, then `Skill`, which an
// agent always gets. No `tools` field at all means Claude Code's "inherit
// everything", which here is every file tool.
func mapClaudeTools(tools []string) []string {
	if len(tools) == 0 {
		return []string{"Bash", "Read", "Grep", "Glob", "Write", "Edit", "MultiEdit", "Skill"}
	}
	out := make([]string, 0, len(tools)+1)
	seen := map[string]bool{}
	for _, t := range tools {
		if claudeNativeTools[t] && !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return append(out, "Skill")
}

// resolveClaudeModelRef maps an agent's `model:` value to a catalogue ref: an
// absent value or `inherit` is "" (the agent inherits its leader's model); an
// alias in modelMap is its ref; a value that is itself a catalogue ref is used
// as is. Anything else — or a ref the catalogue does not hold — is "" with a
// log line, because a dangling model_ref would fail the whole config.
func resolveClaudeModelRef(alias string, modelMap map[string]string, catalog map[string]RuntimeModelConfig) string {
	alias = strings.TrimSpace(alias)
	key := strings.ToLower(alias)
	if key == "" || key == "inherit" {
		return ""
	}
	if ref, ok := modelMap[key]; ok {
		if _, ok := catalog[ref]; ok {
			return ref
		}
		log.Printf("claude plugins: model alias %q maps to %q, which is not in the model catalogue; the agent inherits its leader's model", alias, ref)
		return ""
	}
	if _, ok := catalog[alias]; ok {
		return alias
	}
	log.Printf("claude plugins: model %q is neither a known alias nor a catalogue model; the agent inherits its leader's model", alias)
	return ""
}

// convertibleAgents is the plugin's agents that can become omnis agents, by
// name: one whose omnis name is unusable is skipped, and when two map to the
// same omnis name the first is kept. Each skip is logged.
func convertibleAgents(p claudeplugins.Plugin) []claudeplugins.Agent {
	sorted := append([]claudeplugins.Agent(nil), p.Agents...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })

	owner := map[string]string{} // omnis name → the Claude name that holds it
	var out []claudeplugins.Agent
	for _, a := range sorted {
		name := pluginAgentName(p.Name, a.Name)
		if a.Name == "" || !pluginOmnisNamePattern.MatchString(name) {
			log.Printf("claude plugin %q: agent %q cannot be an omnis agent name (want %s); skipped", p.Name, a.Name, pluginOmnisNamePattern)
			continue
		}
		if first, taken := owner[name]; taken {
			log.Printf("claude plugin %q: agent %q maps to %q, already taken by %q; skipped", p.Name, a.Name, name, first)
			continue
		}
		owner[name] = a.Name
		out = append(out, a)
	}
	return out
}

// pluginLeaderIndex picks the plugin's leader among its agents: the one that
// holds the Agent tool — `lead` when several do, else the first by name — or -1
// when none does.
func pluginLeaderIndex(plugin string, agents []claudeplugins.Agent) int {
	var holders []int
	for i, a := range agents {
		for _, t := range a.Tools {
			if t == "Agent" {
				holders = append(holders, i)
				break
			}
		}
	}
	switch len(holders) {
	case 0:
		return -1
	case 1:
		return holders[0]
	}
	pick := holders[0]
	for _, i := range holders {
		if strings.EqualFold(agents[i].Name, "lead") {
			pick = i
			break
		}
	}
	log.Printf("claude plugin %q: %d agents hold the Agent tool; %q is the leader", plugin, len(holders), agents[pick].Name)
	return pick
}

// convertPlugin turns a plugin's agents into omnis agents and, when one of them
// can delegate, the plugin's squad. Every agent carries the plugin's skills
// unless its own frontmatter names some, and an instruction that ends with the
// translation note. The squad is nil when no agent holds the Agent tool.
func convertPlugin(p claudeplugins.Plugin, modelMap map[string]string, catalog map[string]RuntimeModelConfig) ([]pluginAgent, *SquadEntry) {
	kept := convertibleAgents(p)
	if len(kept) == 0 {
		return nil, nil
	}
	p.Agents = kept // p is this call's copy: the note must name only what survived
	note := pluginTranslationNote(p)
	leader := pluginLeaderIndex(p.Name, kept)

	agents := make([]pluginAgent, 0, len(kept))
	var members []string
	for i, a := range kept {
		skills := a.Skills
		if len(skills) == 0 {
			skills = p.SkillNames
		}
		isLeader, isOn, isBuiltIn := i == leader, true, false
		name := pluginAgentName(p.Name, a.Name)
		agents = append(agents, pluginAgent{
			Plugin: p.Name,
			Type:   p.Name + ":" + a.Name,
			Entry: AgentEntry{
				Name:        name,
				ModelRef:    resolveClaudeModelRef(a.ModelAlias, modelMap, catalog),
				Description: a.Description,
				Instruction: strings.ReplaceAll(a.Body, "${CLAUDE_PLUGIN_ROOT}", p.Dir) + "\n\n" + note,
				Enabled:     &isOn,
				Leader:      &isLeader,
				BuiltIn:     &isBuiltIn,
				Tools:       mapClaudeTools(a.Tools),
				Skills:      append([]string(nil), skills...),
			},
		})
		if !isLeader {
			members = append(members, name)
		}
	}
	if leader < 0 {
		return agents, nil
	}
	sort.Strings(members)
	return agents, &SquadEntry{
		Name:        p.Name,
		Description: agents[leader].Entry.Description,
		Leader:      agents[leader].Entry.Name,
		Members:     members,
	}
}

// pluginTranslationNote is the English block appended to every plugin agent's
// instruction. A Claude agent is written against Claude Code's tools; the note
// says what each of them means here, so the leader does not call tools that
// do not exist.
func pluginTranslationNote(p claudeplugins.Plugin) string {
	var b strings.Builder
	b.WriteString("## Running inside omnis\n\n")
	b.WriteString("This agent was written for Claude Code and runs inside omnis, where a few things work differently.\n\n")
	b.WriteString("- The agents of this plugin are tools here, named after the plugin and the agent:\n")
	for _, a := range p.Agents {
		fmt.Fprintf(&b, "  - `%s:%s` is the tool `%s`\n", p.Name, a.Name, pluginAgentName(p.Name, a.Name))
	}
	b.WriteString("- In omnis, calling an agent with the Agent tool means calling its tool.\n")
	b.WriteString("- In omnis, SendMessage to an agent means calling the same agent again with `resume_session` set to the `session` handle its previous call returned. " +
		"Use it to send a message to an agent you already called: it must reach that same agent session, so keep the handle.\n")
	if foreign := foreignPluginAgents(p); len(foreign) > 0 {
		fmt.Fprintf(&b, "- Agents of other plugins (such as %s) are not available here: tell the user instead of calling them.\n", strings.Join(foreign, ", "))
	} else {
		b.WriteString("- Agents of other plugins are not available here: tell the user instead of calling them.\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// foreignPluginAgents lists, backquoted, sorted and once each, the
// `<plugin>:<agent>` names the plugin's agents mention that belong to another
// plugin.
func foreignPluginAgents(p claudeplugins.Plugin) []string {
	seen := map[string]bool{}
	var out []string
	for _, a := range p.Agents {
		for _, m := range pluginAgentMention.FindAllStringSubmatch(a.Body, -1) {
			if m[1] == p.Name {
				continue
			}
			name := m[1] + ":" + strings.TrimRight(m[2], "-")
			if !seen[name] {
				seen[name] = true
				out = append(out, name)
			}
		}
	}
	sort.Strings(out)
	for i, n := range out {
		out[i] = "`" + n + "`"
	}
	return out
}

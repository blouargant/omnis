package registries

import (
	"fmt"

	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
)

// Deps wires the tools to filesystem locations and agent-resolver callbacks.
//
// Path-bearing fields are functions so they re-resolve on every tool call —
// this matters when the user has just added a remote registry or installed a
// skill via the Web UI: subsequent agent calls see the change without a
// process restart.
type Deps struct {
	// RegistryDir returns the absolute path to the installed-skills root
	// (typically $OMNIS_HOME/registry/skills).
	RegistryDir func() string
	// ConfigPath returns the absolute path of remote_registries.json that
	// reads should consume (the 3-layer search chain's top hit).
	ConfigPath func() string
	// ListAgentSkills returns name → explicit skills list for every configured
	// agent. Used to annotate installed skills with the agents that list them.
	ListAgentSkills func() map[string][]string
	// AddSkillToAgent appends skillName to the named agent's skills list in
	// its agent.json. Idempotent: no error if already present.
	AddSkillToAgent func(agentName, skillName string) error

	// AgentsRegistryDir returns the local agents registry directory used to
	// check which remote agents are already installed (Installed flag in browse).
	AgentsRegistryDir func() string
	// InstalledMCPNames returns the set of MCP server names currently present
	// in mcp_config.json. Used to annotate Installed flag when browsing.
	InstalledMCPNames func() map[string]bool
	// InstalledSquadNames returns the set of squad names currently listed in
	// agents.json. Used to annotate Installed flag when browsing.
	InstalledSquadNames func() map[string]bool
	// InstalledA2ANames returns the set of A2A agent names currently in
	// a2a_config.json. Used to annotate Installed flag when browsing.
	InstalledA2ANames func() map[string]bool
	// InstalledCommandNames returns the set of user slash-command names
	// (normalised) currently in user_commands.json. Used to annotate the
	// Installed flag when browsing command registries.
	InstalledCommandNames func() map[string]bool
	// InstalledPermissionPatterns returns the set of rule patterns currently in
	// the user's permissions.json. Used to annotate the Installed flag when
	// browsing a permissions registry (a rule-set whose patterns are all present
	// shows as installed).
	InstalledPermissionPatterns func() map[string]bool

	// InstallMCP downloads and merges an MCP server into mcp_config.json.
	// Returns the server name, whether it was newly added, and any error.
	// When nil, the install tool returns an error in this surface.
	InstallMCP func(ref RepoRef, token, dirPath string) (name string, added bool, err error)

	// InstallAgent downloads and installs a remote agent. When enable is true
	// the agent name is appended to agents.json so the next reload wires it in.
	// When nil, the install tool returns an error in this surface.
	InstallAgent func(ref RepoRef, token, dirPath string, enable bool) (name string, enabled bool, err error)

	// InstallSquad downloads and merges a remote squad into agents.json.
	// Returns the squad name, whether it was newly added, and any error.
	// When nil, the install tool returns an error in this surface.
	InstallSquad func(ref RepoRef, token, dirPath string) (name string, added bool, err error)

	// InstallA2A downloads and merges a remote A2A agent into a2a_config.json.
	// Returns the agent name, whether it was newly added, and any error.
	// When nil, the install tool returns an error in this surface.
	InstallA2A func(ref RepoRef, token, dirPath string) (name string, added bool, err error)

	// InstallCommand downloads a remote slash command and merges it into the
	// per-user user_commands.json. Returns the command name, whether it was
	// newly added (vs. replaced), and any error. When nil, the install tool
	// returns an error in this surface.
	InstallCommand func(ref RepoRef, token, dirPath string) (name string, added bool, err error)

	// InstallPermission downloads a remote permission rule-set and merges its
	// rules into the user's permissions.json (deduped by pattern). Returns the
	// rule-set name, whether any new rule was added, and any error. When nil,
	// the install tool returns an error in this surface.
	InstallPermission func(ref RepoRef, token, dirPath string) (name string, added bool, err error)

	// RequestReload, when set, triggers a hot-reload of the agent generation so
	// a just-installed item (agent, MCP server, squad, A2A peer) is wired into
	// the running fleet without waiting for the next manual reload. It returns
	// whether a reload actually fired. The server wires this to Manager.Reload;
	// CLI/TUI leave it nil (config edits there apply on the next start).
	RequestReload func() bool
}

// LoaderProtocol is prepended to the instruction of any agent that mounts the
// "registries" tool group. It nudges the model to discover registries before
// invoking specialised actions.
const LoaderProtocol = `
REGISTRY ACCESS — when looking for an existing skill, agent, MCP server, squad, A2A agent, or slash command:
- For skills ALREADY ON DISK: call 'list_installed_skills' to see everything in the local registry and which agents each skill is linked to; use 'get_installed_skill' to read a SKILL.md.
- For remote items: 'list_registries' enumerates configured remote sources (each has a 'kind': skills, agents, mcp, squads, a2a, commands, or both); 'browse_registry' lists items of the appropriate kind; 'get_remote_item' fetches raw content for inspection. When you don't know which registry holds a capability, 'search_registries' (when available) ranks items across ALL registries by meaning.
- Writing actions: 'install_remote_skill' downloads a remote skill into the local registry. 'link_skill_to_agent' grants an agent access to a locally installed skill. 'install_remote_item' installs any remote item (skill, agent, MCP server, squad, A2A agent, or slash command).
- These tools are for stewarding registry items on behalf of OTHER agents — never to load or execute skill content yourself.
`

// ── Input / output types ───────────────────────────────────────────────────

type listRegistriesIn struct{}

type registrySummary struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	URL      string `json:"url"`
	Provider string `json:"provider,omitempty"`
	Kind     string `json:"kind"`
}

type listRegistriesOut struct {
	Registries []registrySummary `json:"registries"`
}

type browseRegistryIn struct {
	RegistryID string `json:"registry_id"`
}

// browseRegistryOut is a union type: only the fields matching the registry's
// Kind are populated. Check the Kind field to know which slice to read.
type browseRegistryOut struct {
	Kind        string           `json:"kind"`
	Skills      []SkillInfo      `json:"skills,omitempty"`
	Agents      []AgentInfo      `json:"agents,omitempty"`
	MCP         []MCPToolInfo    `json:"mcp,omitempty"`
	Squads      []SquadInfo      `json:"squads,omitempty"`
	A2A         []A2AAgentInfo   `json:"a2a,omitempty"`
	Commands    []CommandInfo    `json:"commands,omitempty"`
	Permissions []PermissionInfo `json:"permissions,omitempty"`
}

type getRemoteSkillIn struct {
	RegistryID string `json:"registry_id"`
	DirPath    string `json:"dir_path"`
}

type getRemoteSkillOut struct {
	Content string `json:"content"`
}

type getRemoteItemIn struct {
	RegistryID string `json:"registry_id"`
	DirPath    string `json:"dir_path"`
}

type getRemoteItemOut struct {
	Kind    string `json:"kind"`
	Content string `json:"content"`
}

type installRemoteSkillIn struct {
	RegistryID string `json:"registry_id"`
	DirPath    string `json:"dir_path"`
}

type installRemoteSkillOut struct {
	Name string `json:"name"`
	// InstalledDeps lists commands/permission-sets auto-installed alongside the
	// skill (prefixed "command:" / "permission:"), declared in its SKILL.md.
	InstalledDeps []string `json:"installed_deps,omitempty"`
	// Warnings carries best-effort dependency-resolution failures.
	Warnings []string `json:"warnings,omitempty"`
	// Reloaded reports whether a hot-reload was triggered.
	Reloaded bool `json:"reloaded,omitempty"`
}

type installRemoteItemIn struct {
	RegistryID string `json:"registry_id"`
	DirPath    string `json:"dir_path"`
	// Enable is only relevant for agent installs: when true the installed agent
	// name is appended to agents.json so the next hot-reload wires it in.
	Enable bool `json:"enable,omitempty"`
}

type installRemoteItemOut struct {
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Added   bool   `json:"added,omitempty"`
	Enabled bool   `json:"enabled,omitempty"` // agents only
	// InstalledDeps lists dependencies auto-installed alongside an agent
	// (prefixed "skill:" / "mcp:"). Agents only.
	InstalledDeps []string `json:"installed_deps,omitempty"`
	// Warnings carries best-effort failures (e.g. a declared dependency not
	// found in any configured registry) that did not abort the install.
	Warnings []string `json:"warnings,omitempty"`
	// Reloaded reports whether a hot-reload was triggered so the item is live
	// in the running fleet without a manual reload.
	Reloaded bool `json:"reloaded,omitempty"`
}

type linkSkillToAgentIn struct {
	AgentName string `json:"agent_name"`
	SkillName string `json:"skill_name"`
}

type linkSkillToAgentOut struct {
	Linked        bool   `json:"linked"`         // true when a new symlink was created
	AlreadyLinked bool   `json:"already_linked"` // true when an equivalent symlink already existed
	SkillName     string `json:"skill_name"`
	AgentName     string `json:"agent_name"`
	Reloaded      bool   `json:"reloaded,omitempty"` // true when a hot-reload was triggered
}

type listInstalledIn struct{}

type listInstalledOut struct {
	Skills []InstalledSkill `json:"skills"`
}

type getInstalledIn struct {
	SkillName string `json:"skill_name"`
}

type getInstalledOut struct {
	Content  string   `json:"content"`
	LinkedIn []string `json:"linked_in"`
}

// ── Tool factory ───────────────────────────────────────────────────────────

// NewTools returns the registries tool set: list_installed_skills,
// get_installed_skill, list_registries, browse_registry, get_remote_skill,
// get_remote_item, install_remote_skill, install_remote_item, link_skill_to_agent.
func NewTools(deps Deps) []tool.Tool {
	return []tool.Tool{
		mustTool("list_installed_skills",
			"List every skill currently installed in the local skills registry. Each "+
				"result includes the skill `name`, description, author, tags, and the list "+
				"of agents that explicitly reference it (`linked_in`). Use this first when "+
				"asked to find or recommend a skill — locally installed skills are preferred "+
				"over remote ones. No arguments.",
			func(_ tool.Context, _ listInstalledIn) (listInstalledOut, error) {
				var agentsMap map[string][]string
				if deps.ListAgentSkills != nil {
					agentsMap = deps.ListAgentSkills()
				}
				list, err := ListInstalled(deps.RegistryDir(), agentsMap)
				if err != nil {
					return listInstalledOut{}, err
				}
				return listInstalledOut{Skills: list}, nil
			}),

		mustTool("get_installed_skill",
			"Fetch the raw SKILL.md content of a skill already installed in the local "+
				"registry, plus the list of agents that reference it. Use this to inspect a "+
				"skill on disk before recommending it or adding it to an agent. "+
				"Arguments: `skill_name` (string, required) — from list_installed_skills.",
			func(_ tool.Context, in getInstalledIn) (getInstalledOut, error) {
				if in.SkillName == "" {
					return getInstalledOut{}, fmt.Errorf("skill_name is required")
				}
				data, err := ReadInstalled(deps.RegistryDir(), in.SkillName)
				if err != nil {
					return getInstalledOut{}, err
				}
				linkedIn := []string{}
				if deps.ListAgentSkills != nil {
					for agentName, skillList := range deps.ListAgentSkills() {
						for _, s := range skillList {
							if s == in.SkillName {
								linkedIn = append(linkedIn, agentName)
								break
							}
						}
					}
				}
				return getInstalledOut{Content: string(data), LinkedIn: linkedIn}, nil
			}),

		mustTool("list_registries",
			"List the remote registries configured for this installation. Each entry "+
				"includes an `id` you can pass to browse_registry and a `kind` field "+
				"(skills, agents, mcp, squads, a2a, or both) that tells you what the "+
				"registry contains. No arguments.",
			func(_ tool.Context, _ listRegistriesIn) (listRegistriesOut, error) {
				list, err := LoadRegistries(deps.ConfigPath())
				if err != nil {
					return listRegistriesOut{}, err
				}
				out := make([]registrySummary, 0, len(list))
				for _, r := range list {
					out = append(out, registrySummary{
						ID:       r.ID,
						Name:     r.Name,
						URL:      r.URL,
						Provider: r.Provider,
						Kind:     r.NormalizedKind(),
					})
				}
				return listRegistriesOut{Registries: out}, nil
			}),

		mustTool("browse_registry",
			"List every item available in a remote registry. The response includes a "+
				"`kind` field and one populated slice: `skills` (kind=skills/both), "+
				"`agents` (kind=agents/both), `mcp` (kind=mcp), `squads` (kind=squads), "+
				"`a2a` (kind=a2a), or `commands` (kind=commands). Each item includes its `dir_path` for use with "+
				"get_remote_item or install_remote_item, a description, and an `installed` "+
				"flag. For agents, a `format` field of `\"claude\"` means the agent uses "+
				"Claude Code markdown format (a single .md file) — this is fully supported "+
				"and installable, not an error. Agents without a format field use native "+
				"omnis format (agent.json + optional instruction.md). "+
				"Arguments: `registry_id` (string, required) — from list_registries.",
			func(_ tool.Context, in browseRegistryIn) (browseRegistryOut, error) {
				ref, reg, token, err := resolveRef(deps, in.RegistryID)
				if err != nil {
					return browseRegistryOut{}, err
				}
				kind := reg.NormalizedKind()
				out := browseRegistryOut{Kind: kind}

				switch kind {
				case KindBoth:
					skillsDir := ""
					if deps.RegistryDir != nil {
						skillsDir = deps.RegistryDir()
					}
					skills, err := BrowseSkills(ref, token, skillsDir)
					if err != nil {
						return browseRegistryOut{}, err
					}
					out.Skills = skills
					agentsDir := ""
					if deps.AgentsRegistryDir != nil {
						agentsDir = deps.AgentsRegistryDir()
					}
					agents, err := BrowseAgents(ref, token, agentsDir)
					if err != nil {
						return browseRegistryOut{}, err
					}
					out.Agents = agents

				case KindSkills:
					skillsDir := ""
					if deps.RegistryDir != nil {
						skillsDir = deps.RegistryDir()
					}
					skills, err := BrowseSkills(ref, token, skillsDir)
					if err != nil {
						return browseRegistryOut{}, err
					}
					out.Skills = skills

				case KindAgents:
					agentsDir := ""
					if deps.AgentsRegistryDir != nil {
						agentsDir = deps.AgentsRegistryDir()
					}
					agents, err := BrowseAgents(ref, token, agentsDir)
					if err != nil {
						return browseRegistryOut{}, err
					}
					out.Agents = agents

				case KindMCP:
					installed := map[string]bool{}
					if deps.InstalledMCPNames != nil {
						installed = deps.InstalledMCPNames()
					}
					tools, err := BrowseMCPTools(ref, token, installed)
					if err != nil {
						return browseRegistryOut{}, err
					}
					out.MCP = tools

				case KindSquads:
					installed := map[string]bool{}
					if deps.InstalledSquadNames != nil {
						installed = deps.InstalledSquadNames()
					}
					squads, err := BrowseSquads(ref, token, installed)
					if err != nil {
						return browseRegistryOut{}, err
					}
					out.Squads = squads

				case KindA2A:
					installed := map[string]bool{}
					if deps.InstalledA2ANames != nil {
						installed = deps.InstalledA2ANames()
					}
					agents, err := BrowseA2AAgents(ref, token, installed)
					if err != nil {
						return browseRegistryOut{}, err
					}
					out.A2A = agents

				case KindCommands:
					installed := map[string]bool{}
					if deps.InstalledCommandNames != nil {
						installed = deps.InstalledCommandNames()
					}
					commands, err := BrowseCommands(ref, token, installed)
					if err != nil {
						return browseRegistryOut{}, err
					}
					out.Commands = commands

				case KindPermissions:
					var installedPatterns map[string]bool
					if deps.InstalledPermissionPatterns != nil {
						installedPatterns = deps.InstalledPermissionPatterns()
					}
					perms, err := BrowsePermissions(ref, token, installedPatterns)
					if err != nil {
						return browseRegistryOut{}, err
					}
					out.Permissions = perms

				default:
					return browseRegistryOut{}, fmt.Errorf("unsupported registry kind %q", kind)
				}
				return out, nil
			}),

		mustTool("get_remote_skill",
			"Fetch the raw SKILL.md content of a skill in a remote registry without installing it. "+
				"Use this to inspect what a skill does before recommending it. "+
				"Arguments: `registry_id` (string, required), `dir_path` (string, required) — from browse_registry.",
			func(_ tool.Context, in getRemoteSkillIn) (getRemoteSkillOut, error) {
				ref, _, token, err := resolveRef(deps, in.RegistryID)
				if err != nil {
					return getRemoteSkillOut{}, err
				}
				if in.DirPath == "" {
					return getRemoteSkillOut{}, fmt.Errorf("dir_path is required")
				}
				body, err := FetchSkillMD(ref, token, in.DirPath)
				if err != nil {
					return getRemoteSkillOut{}, err
				}
				return getRemoteSkillOut{Content: string(body)}, nil
			}),

		mustTool("get_remote_item",
			"Fetch the raw content of any remote registry item (skill, agent, MCP server, "+
				"squad, A2A agent, or slash command) without installing it. Use this to inspect an item "+
				"before recommending or installing it. The response includes the `kind` and "+
				"the raw `content` (SKILL.md, agent.json or agent .md for Claude Code format, "+
				"mcp.json/mcp.md, squad.json, a2a.json, or the command .md). For agents with `format: \"claude\"` "+
				"the content is the raw Claude Code markdown definition. "+
				"Arguments: `registry_id` (string, required), `dir_path` (string, required) — from browse_registry.",
			func(_ tool.Context, in getRemoteItemIn) (getRemoteItemOut, error) {
				ref, reg, token, err := resolveRef(deps, in.RegistryID)
				if err != nil {
					return getRemoteItemOut{}, err
				}
				if in.DirPath == "" {
					return getRemoteItemOut{}, fmt.Errorf("dir_path is required")
				}
				kind := reg.NormalizedKind()
				var body []byte
				switch kind {
				case KindSkills, KindBoth:
					body, err = FetchSkillMD(ref, token, in.DirPath)
				case KindAgents:
					body, err = FetchAgentJSON(ref, token, in.DirPath)
				case KindMCP:
					body, err = FetchMCPManifest(ref, token, in.DirPath)
				case KindSquads:
					body, err = FetchSquadJSON(ref, token, in.DirPath)
				case KindA2A:
					body, err = FetchA2AAgentJSON(ref, token, in.DirPath)
				case KindCommands:
					body, err = FetchCommandMD(ref, token, in.DirPath)
				case KindPermissions:
					body, err = FetchPermissionJSON(ref, token, in.DirPath)
				default:
					return getRemoteItemOut{}, fmt.Errorf("unsupported registry kind %q", kind)
				}
				if err != nil {
					return getRemoteItemOut{}, err
				}
				return getRemoteItemOut{Kind: kind, Content: string(body)}, nil
			}),

		mustTool("install_remote_skill",
			"Download a skill from a remote registry and install it into the local skills registry. "+
				"The skill becomes available system-wide; use link_skill_to_agent afterwards to grant a specific agent access. "+
				"Arguments: `registry_id` (string, required), `dir_path` (string, required) — from browse_registry.",
			func(_ tool.Context, in installRemoteSkillIn) (installRemoteSkillOut, error) {
				ref, _, token, err := resolveRef(deps, in.RegistryID)
				if err != nil {
					return installRemoteSkillOut{}, err
				}
				if in.DirPath == "" {
					return installRemoteSkillOut{}, fmt.Errorf("dir_path is required")
				}
				name, err := InstallSkill(ref, token, in.DirPath, deps.RegistryDir())
				if err != nil {
					return installRemoteSkillOut{}, err
				}
				// Cascade the skill's declared commands + permission rule-sets so
				// it arrives with everything it needs, then reload so bundled
				// permission overlays apply live.
				installedDeps, warnings := deps.cascadeSkillDeps(ref, token, in.DirPath)
				reloaded := false
				if len(installedDeps) > 0 {
					reloaded = deps.requestReload()
				}
				return installRemoteSkillOut{Name: name, InstalledDeps: installedDeps, Warnings: warnings, Reloaded: reloaded}, nil
			}),

		mustTool("install_remote_item",
			"Install any remote registry item: skill, agent, MCP server, squad, A2A agent, slash command, or permission rule-set. "+
				"Dispatches based on the registry's kind. Installing a skill also cascades any commands/permissions it declares in its SKILL.md. For skills, use link_skill_to_agent "+
				"afterwards to grant a specific agent access. For agents, set `enable: true` to "+
				"append the agent to agents.json so the next hot-reload wires it in. "+
				"Arguments: `registry_id` (string, required), `dir_path` (string, required) "+
				"— from browse_registry; `enable` (bool, optional, agents only).",
			func(_ tool.Context, in installRemoteItemIn) (installRemoteItemOut, error) {
				ref, reg, token, err := resolveRef(deps, in.RegistryID)
				if err != nil {
					return installRemoteItemOut{}, err
				}
				if in.DirPath == "" {
					return installRemoteItemOut{}, fmt.Errorf("dir_path is required")
				}
				kind := reg.NormalizedKind()

				switch kind {
				case KindSkills, KindBoth:
					name, err := InstallSkill(ref, token, in.DirPath, deps.RegistryDir())
					if err != nil {
						return installRemoteItemOut{}, err
					}
					// Cascade the skill's declared commands + permission rule-sets.
					installedDeps, warnings := deps.cascadeSkillDeps(ref, token, in.DirPath)
					reloaded := false
					if len(installedDeps) > 0 {
						reloaded = deps.requestReload()
					}
					return installRemoteItemOut{
						Name: name, Kind: KindSkills, Added: true,
						InstalledDeps: installedDeps, Warnings: warnings, Reloaded: reloaded,
					}, nil

				case KindAgents:
					if deps.InstallAgent == nil {
						return installRemoteItemOut{}, fmt.Errorf("agent install is not available in this surface")
					}
					name, enabled, err := deps.InstallAgent(ref, token, in.DirPath, in.Enable)
					if err != nil {
						return installRemoteItemOut{}, err
					}
					// Resolve the skills and MCP servers the agent declares so it
					// is actually usable, mirroring the web-UI install route. The
					// remote manifest is the source of truth (no disk-layer guess).
					var installedDeps, warnings []string
					if raw, ferr := FetchAgentJSON(ref, token, in.DirPath); ferr == nil {
						skills, mcpServers := parseAgentDeps(raw)
						installedDeps, warnings = deps.resolveAgentDeps(skills, mcpServers)
					}
					reloaded := deps.requestReload()
					return installRemoteItemOut{
						Name: name, Kind: KindAgents, Added: true, Enabled: enabled,
						InstalledDeps: installedDeps, Warnings: warnings, Reloaded: reloaded,
					}, nil

				case KindMCP:
					if deps.InstallMCP == nil {
						return installRemoteItemOut{}, fmt.Errorf("MCP install is not available in this surface")
					}
					name, added, err := deps.InstallMCP(ref, token, in.DirPath)
					if err != nil {
						return installRemoteItemOut{}, err
					}
					reloaded := added && deps.requestReload()
					return installRemoteItemOut{Name: name, Kind: KindMCP, Added: added, Reloaded: reloaded}, nil

				case KindSquads:
					if deps.InstallSquad == nil {
						return installRemoteItemOut{}, fmt.Errorf("squad install is not available in this surface")
					}
					name, added, err := deps.InstallSquad(ref, token, in.DirPath)
					if err != nil {
						return installRemoteItemOut{}, err
					}
					reloaded := added && deps.requestReload()
					return installRemoteItemOut{Name: name, Kind: KindSquads, Added: added, Reloaded: reloaded}, nil

				case KindA2A:
					if deps.InstallA2A == nil {
						return installRemoteItemOut{}, fmt.Errorf("A2A install is not available in this surface")
					}
					name, added, err := deps.InstallA2A(ref, token, in.DirPath)
					if err != nil {
						return installRemoteItemOut{}, err
					}
					reloaded := added && deps.requestReload()
					return installRemoteItemOut{Name: name, Kind: KindA2A, Added: added, Reloaded: reloaded}, nil

				case KindCommands:
					if deps.InstallCommand == nil {
						return installRemoteItemOut{}, fmt.Errorf("command install is not available in this surface")
					}
					name, added, err := deps.InstallCommand(ref, token, in.DirPath)
					if err != nil {
						return installRemoteItemOut{}, err
					}
					return installRemoteItemOut{Name: name, Kind: KindCommands, Added: added}, nil

				case KindPermissions:
					if deps.InstallPermission == nil {
						return installRemoteItemOut{}, fmt.Errorf("permission install is not available in this surface")
					}
					name, added, err := deps.InstallPermission(ref, token, in.DirPath)
					if err != nil {
						return installRemoteItemOut{}, err
					}
					reloaded := added && deps.requestReload()
					return installRemoteItemOut{Name: name, Kind: KindPermissions, Added: added, Reloaded: reloaded}, nil

				default:
					return installRemoteItemOut{}, fmt.Errorf("unsupported registry kind %q", kind)
				}
			}),

		mustTool("link_skill_to_agent",
			"Add an installed skill to an agent's skills list so the agent can load it. "+
				"The skill must already be installed locally (see install_remote_skill or install_remote_item). "+
				"Arguments: `agent_name` (string, required), `skill_name` (string, required).",
			func(_ tool.Context, in linkSkillToAgentIn) (linkSkillToAgentOut, error) {
				if in.AgentName == "" {
					return linkSkillToAgentOut{}, fmt.Errorf("agent_name is required")
				}
				if in.SkillName == "" {
					return linkSkillToAgentOut{}, fmt.Errorf("skill_name is required")
				}
				if deps.AddSkillToAgent == nil {
					return linkSkillToAgentOut{}, fmt.Errorf("agent linking is not available in this surface")
				}
				if err := deps.AddSkillToAgent(in.AgentName, in.SkillName); err != nil {
					return linkSkillToAgentOut{}, err
				}
				return linkSkillToAgentOut{
					Linked:        true,
					AlreadyLinked: false,
					SkillName:     in.SkillName,
					AgentName:     in.AgentName,
					Reloaded:      deps.requestReload(),
				}, nil
			}),
	}
}

// resolveRef looks up a registry by ID and parses its repo reference.
func resolveRef(deps Deps, id string) (RepoRef, *Registry, string, error) {
	if id == "" {
		return nil, nil, "", fmt.Errorf("registry_id is required")
	}
	list, err := LoadRegistries(deps.ConfigPath())
	if err != nil {
		return nil, nil, "", err
	}
	reg := FindByID(list, id)
	if reg == nil {
		return nil, nil, "", fmt.Errorf("registry %q not found — call list_registries to see configured registries", id)
	}
	ref, err := ParseRepoRef(reg.URL, reg.Provider)
	if err != nil {
		return nil, nil, "", fmt.Errorf("registry %q has an invalid URL: %w", id, err)
	}
	return ref, reg, reg.Token, nil
}

func mustTool[A, R any](name, desc string, h functiontool.Func[A, R]) tool.Tool {
	t, err := functiontool.New(functiontool.Config{Name: name, Description: desc}, h)
	if err != nil {
		panic(fmt.Errorf("build tool %s: %w", name, err))
	}
	return t
}

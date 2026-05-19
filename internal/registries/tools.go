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
	// (typically $YOKE_HOME/registry/skills).
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
}

// LoaderProtocol is prepended to the instruction of any agent that mounts the
// "registries" tool group. It nudges the model to discover registries before
// invoking specialised actions.
const LoaderProtocol = `
SKILL REGISTRY ACCESS — when looking for an existing skill or extending an agent's capabilities:
- For skills ALREADY ON DISK: call 'list_installed_skills' to see everything in the local registry and which agents each skill is linked to; use 'get_installed_skill' to read a SKILL.md.
- For skills NOT YET ON DISK: 'list_registries' enumerates configured remote sources; 'browse_registry' lists each one; 'get_remote_skill' fetches a SKILL.md for inspection.
- Writing actions: 'install_remote_skill' downloads a remote skill into the local registry. 'link_skill_to_agent' grants an agent access to a locally installed skill.
- These tools are for stewarding skills on behalf of OTHER agents — never to load or execute skill content yourself.
`

// ── Input / output types ───────────────────────────────────────────────────

type listRegistriesIn struct{}

type registrySummary struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	URL      string `json:"url"`
	Provider string `json:"provider,omitempty"`
}

type listRegistriesOut struct {
	Registries []registrySummary `json:"registries"`
}

type browseRegistryIn struct {
	RegistryID string `json:"registry_id"`
}

type browseRegistryOut struct {
	Skills []SkillInfo `json:"skills"`
}

type getRemoteSkillIn struct {
	RegistryID string `json:"registry_id"`
	DirPath    string `json:"dir_path"`
}

type getRemoteSkillOut struct {
	Content string `json:"content"`
}

type installRemoteSkillIn struct {
	RegistryID string `json:"registry_id"`
	DirPath    string `json:"dir_path"`
}

type installRemoteSkillOut struct {
	Name string `json:"name"`
}

type linkSkillToAgentIn struct {
	AgentName string `json:"agent_name"`
	SkillName string `json:"skill_name"`
}

type linkSkillToAgentOut struct {
	Linked        bool   `json:"linked"`           // true when a new symlink was created
	AlreadyLinked bool   `json:"already_linked"`   // true when an equivalent symlink already existed
	SkillName     string `json:"skill_name"`
	AgentName     string `json:"agent_name"`
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
// install_remote_skill, link_skill_to_agent.
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
			"List the remote skill registries configured for this installation. "+
				"Each entry includes an `id` you can pass to browse_registry. No arguments.",
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
					})
				}
				return listRegistriesOut{Registries: out}, nil
			}),

		mustTool("browse_registry",
			"List every skill available in a remote registry. Each result includes its "+
				"`dir_path` (pass it to get_remote_skill or install_remote_skill), description, "+
				"author, tags, and whether the skill is already installed locally. "+
				"Arguments: `registry_id` (string, required) — from list_registries.",
			func(_ tool.Context, in browseRegistryIn) (browseRegistryOut, error) {
				ref, _, token, err := resolveRef(deps, in.RegistryID)
				if err != nil {
					return browseRegistryOut{}, err
				}
				skills, err := BrowseSkills(ref, token, deps.RegistryDir())
				if err != nil {
					return browseRegistryOut{}, err
				}
				return browseRegistryOut{Skills: skills}, nil
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
				return installRemoteSkillOut{Name: name}, nil
			}),

		mustTool("link_skill_to_agent",
			"Add an installed skill to an agent's skills list so the agent can load it. "+
				"The skill must already be installed locally (see install_remote_skill). "+
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

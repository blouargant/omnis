package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/blouargant/omnis/internal/budget"
	"github.com/blouargant/omnis/internal/configedit"
	"github.com/blouargant/omnis/internal/paths"
)

// SquadEntry describes one named group of agents in the JSON runtime config.
// A squad picks a leader and a set of member sub-agents from the top-level
// `agents:` array; squads don't redefine agents. Selecting a squad per
// chat session controls which leader and which sub-agents the session uses.
type SquadEntry struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Leader      string   `json:"leader"`
	Members     []string `json:"members"`
}

// AgentEntry describes one agent in the JSON runtime config. Model selection
// is owned exclusively by models.json — an agent picks a model via ModelRef
// and inherits provider/base_url/api_key/context_length/prices from there.
// Older agent.json files may still carry provider/model/base_url/api_key
// fields; Go's JSON decoder silently drops them.
type AgentEntry struct {
	Name                  string   `json:"name"`
	ModelRef              string   `json:"model_ref"`
	Description           string   `json:"description"`
	Instruction           string   `json:"instruction"`
	Enabled               *bool    `json:"enabled"`
	Leader                *bool    `json:"leader"`
	BuiltIn               *bool    `json:"builtin"`
	AllowFileAttachments  *bool    `json:"allow_file_attachments"`
	Tools                 []string `json:"tools"`
	Skills                []string `json:"skills"`
	SoftSkillsDir         string   `json:"softskills_dir"`
	MCPConfigPath         string   `json:"mcp_config_path"`
	MCPServers            []string `json:"mcp_servers"`
	A2AAgents             []string `json:"a2a_agents,omitempty"`
	PermissionsConfigPath string   `json:"permissions_config_path"`
	// MaxInstances caps how many invocations of this sub-agent the leader may
	// run in parallel from a single tool call. <= 1 (the default) keeps the
	// classic one-at-a-time tool; > 1 exposes a batch/fan-out tool.
	MaxInstances int `json:"max_instances,omitempty"`
	// MaxToolCalls caps how many tool calls this agent may make in ONE user turn.
	// 0 (the default) = uncapped.
	//
	// This is a design limit, not a spend ceiling (that is agents.json
	// `turn_budget`): it bounds how much work the agent does, and when it trips
	// the agent is told to conclude with what it has — the user is never asked.
	// It matters most for a sub-agent, whose cost is QUADRATIC in its tool calls:
	// it runs its own flow loop and re-sends its entire accumulated context —
	// every fetched page included — on each model call. research_critic reached
	// 9.1M prompt tokens from ~20 fetches across 2 invocations, so capping N is
	// worth far more than capping tokens.
	MaxToolCalls int `json:"max_tool_calls,omitempty"`
	// ResumableSessions controls durable, re-attachable sessions for this
	// sub-agent: each call returns a `session` handle the leader can pass back as
	// `resume_session` to continue that exact conversation (keeping its prior
	// context) instead of starting fresh. It is a tri-state pointer with an
	// **opt-out** default: nil (absent) ⇒ ENABLED; set it to false to disable and
	// revert the sub-agent to a stateless pure function. Composes with
	// MaxInstances (each parallel task gets its own handle).
	ResumableSessions *bool `json:"resumable_sessions,omitempty"`
	// SubAgents is this agent's OWN team: agents it may delegate to, mounted as
	// agenttool wrappers on its tool list exactly as a squad root mounts its
	// members. It is how an expensive specialist pushes bulk retrieval into a
	// cheap gatherer's context instead of accumulating it in its own (see
	// nested_subagents.go for why that matters and the contract that makes it
	// safe). Names must be enabled agents; the graph must be acyclic.
	//
	// Distinct from a squad's `members`, which is what the LEADER may delegate to.
	// An agent reachable only through `subagents` is built but never handed to the
	// leader, so a pure gatherer can serve one specialist without cluttering the
	// coordinator's tool list.
	SubAgents []string `json:"subagents,omitempty"`
}

// ProviderEntry describes one reusable provider profile in models.json.
// A provider groups credentials and an endpoint so multiple models can share
// them via `provider_ref` on a ModelEntry.
type ProviderEntry struct {
	Kind    string `json:"kind"`
	BaseURL string `json:"base_url"`
	APIKey  string `json:"api_key"`
}

// ModelEntry describes one reusable model profile in models.json.
// Most fields are inherited from the referenced provider when set; explicit
// fields override the provider's defaults.
type ModelEntry struct {
	ProviderRef                string  `json:"provider_ref"`
	Provider                   string  `json:"provider"`
	Model                      string  `json:"model"`
	BaseURL                    string  `json:"base_url"`
	APIKey                     string  `json:"api_key"`
	ContextLength              int     `json:"context_length"`
	InputTokenPricePerMillion  float64 `json:"input_token_price_per_million"`
	OutputTokenPricePerMillion float64 `json:"output_token_price_per_million"`
	// CachedInputTokenPricePerMillion is the price for prompt tokens served
	// from the provider's prompt cache (Anthropic cache_read,
	// OpenAI prompt_tokens_details.cached_tokens). Defaults to
	// InputTokenPricePerMillion when unset (i.e. no cache discount).
	CachedInputTokenPricePerMillion float64 `json:"cached_input_token_price_per_million"`
	// CacheCreationTokenPricePerMillion is the price for prompt tokens that
	// populate the provider's prompt cache for the first time (Anthropic
	// cache_creation_input_tokens). Defaults to InputTokenPricePerMillion
	// when unset.
	CacheCreationTokenPricePerMillion float64 `json:"cache_creation_token_price_per_million"`
	// Embedding marks this model entry as an embeddings model (not a chat
	// model). The Web UI lists only Embedding:true models in the internal
	// embedding-model selector, and agents never pick one via model_ref.
	Embedding bool `json:"embedding,omitempty"`
	// Dim is the output dimension of an embedding model (e.g. 1536 for
	// text-embedding-3-small, 768 for nomic-embed-text). Ignored for chat
	// models. Zero means "learn from the first response".
	Dim int `json:"dim,omitempty"`
	// DisableStreaming forces agents using this model to call the
	// non-streaming endpoint even when the surface (web UI) requests SSE.
	// Set it for backends whose streamed output misbehaves (e.g. a quantised
	// model behind vLLM/LiteLLM that runs away only when streamed); the
	// non-streaming path delivers the full reply in one turn.
	DisableStreaming bool `json:"disable_streaming,omitempty"`
	// PromptCache enables Anthropic-style prompt caching for agents using this
	// model: the OpenAI-compat adapter adds `cache_control: {"type": "ephemeral"}`
	// breakpoints to the long-lived prefix (system instruction + tool catalogue)
	// and the latest turn so an upstream LiteLLM proxy fronting an Anthropic model
	// caches them.
	//
	// Tri-state (opt-out): nil/absent defaults to ON for `openai_compat`
	// providers — LiteLLM/gateway backends, where a client-side breakpoint is
	// what makes Anthropic caching engage at all, and which we verified silently
	// ignore the annotation when the backing model doesn't cache (Scaleway,
	// Llama/Mistral/GLM behind LiteLLM: HTTP 200, field ignored, no error) — and
	// OFF for a plain `openai` provider (it caches automatically server-side and
	// may reject the unrecognised field). An explicit `false` forces it off, a
	// `true` forces it on for any provider. Resolved by promptCacheEnabled at the
	// ModelEntry→RuntimeModelConfig boundary, so the runtime side stays a plain
	// bool.
	PromptCache *bool `json:"prompt_cache,omitempty"`
}

// modelsConfigFile is the on-disk shape of models.json.
type modelsConfigFile struct {
	Providers map[string]ProviderEntry `json:"providers"`
	Models    map[string]ModelEntry    `json:"models"`
	// EmbedModelRef names the model used as the internal semantic embedder.
	// Lives here so the Web UI Models panel can manage the whole embedding
	// config (the embedding model entries + which one is active) in one place.
	// An agents.json `embed_model_ref` or OMNIS_EMBED_MODEL_REF env override it.
	EmbedModelRef string `json:"embed_model_ref,omitempty"`
	// EvalModelRef names the model used for internal one-off evaluator calls —
	// currently the /goal completion judge (condition + transcript → yes/no).
	// It is a cheap "small fast model" role, distinct from any agent's model_ref;
	// when empty the evaluator falls back to the session's leader model so /goal
	// works out-of-the-box. An agents.json `eval_model_ref` or the
	// OMNIS_GOAL_MODEL_REF env override it. Mirrors EmbedModelRef precisely.
	EvalModelRef string `json:"eval_model_ref,omitempty"`
	// OverrideModelRef names a single model that, when OverrideModelEnabled is
	// true, is forced onto EVERY agent — a temporary "run the whole fleet on one
	// model" switch (see applyModelOverride). The chosen ref is kept even while
	// disabled so toggling the flag flips between the single model and the
	// per-agent multi-model config without re-picking. OMNIS_OVERRIDE_MODEL_REF /
	// OMNIS_OVERRIDE_MODEL_ENABLED override these. The internal embedder and the
	// /goal evaluator models are deliberately unaffected (a chat model can't
	// embed, and the evaluator is an internal, non-agent role).
	OverrideModelRef     string `json:"override_model_ref,omitempty"`
	OverrideModelEnabled bool   `json:"override_model_enabled,omitempty"`
}

type runtimeConfigFile struct {
	SoftSkillsDir         string `json:"softskills_dir"`
	AppName               string `json:"app_name"`
	TokenOptimization     bool   `json:"token_optimization"`
	BashOutputFiltersDir  string `json:"bash_output_filters_dir"`
	BashTimeoutSeconds    int    `json:"bash_timeout_seconds"`
	MCPConfigPath         string `json:"mcp_config_path"`
	PermissionsConfigPath string `json:"permissions_config_path"`
	HooksConfigPath       string `json:"hooks_config_path"`
	SerpAPIKey            string `json:"serpapi_key"`
	// EmbedModelRef names the model in models.json used for internal semantic
	// embedding (softskill/precedent/codebase recall). It must reference a
	// model entry flagged `"embedding": true`. Empty disables semantic recall
	// unless the OMNIS_EMBED_* environment provides an embedder instead.
	EmbedModelRef string `json:"embed_model_ref,omitempty"`
	// EvalModelRef overrides models.json `eval_model_ref` (the /goal evaluator
	// model). OMNIS_GOAL_MODEL_REF overrides this in turn.
	EvalModelRef string       `json:"eval_model_ref,omitempty"`
	Agents       []string     `json:"agents"`
	Squads       []SquadEntry `json:"squads"`
	// RouterSquad names the squad that acts as the Omnis router — the default
	// agent for new chats, which routes each request to the best-suited squad.
	// A pointer so we can distinguish absent (nil → default to "omnis") from an
	// explicit opt-out ("none"/""). Overridable by OMNIS_ROUTER_SQUAD.
	RouterSquad *string `json:"router_squad,omitempty"`
	// TurnBudget caps what one turn may spend before the user is asked whether
	// to continue. Absent → the defaults; either field set to 0 removes that
	// axis; both 0 makes turns unbounded (the pre-budget behaviour).
	// Overridable by OMNIS_TURN_MAX_TOOL_CALLS / OMNIS_TURN_MAX_TOKENS.
	TurnBudget *turnBudgetFile `json:"turn_budget,omitempty"`
	// Models is no longer a supported field in agents.json. It is detected
	// here only to produce a clear migration error. Move the block to
	// models.json (see RuntimeSettings.ModelsConfigPath).
	LegacyModels json.RawMessage `json:"models,omitempty"`
}

// turnBudgetFile is the agents.json shape of the per-turn spend ceiling.
// Pointers so an explicit 0 ("no ceiling on this axis") is distinguishable from
// an absent field ("use the default").
type turnBudgetFile struct {
	MaxToolCalls *int   `json:"max_tool_calls,omitempty"`
	MaxTokens    *int64 `json:"max_tokens,omitempty"`
}

// RuntimeProviderConfig is one normalized provider profile.
type RuntimeProviderConfig struct {
	Name    string
	Kind    string
	BaseURL string
	APIKey  string
}

// RuntimeModelConfig is one normalized model profile.
type RuntimeModelConfig struct {
	Name                              string
	Provider                          string
	Model                             string
	BaseURL                           string
	APIKey                            string
	ContextLength                     int
	InputTokenPricePerMillion         float64
	OutputTokenPricePerMillion        float64
	CachedInputTokenPricePerMillion   float64
	CacheCreationTokenPricePerMillion float64
	Embedding                         bool
	Dim                               int
	DisableStreaming                  bool
	PromptCache                       bool
}

// RuntimeAgentConfig is one fully-resolved agent configuration entry.
type RuntimeAgentConfig struct {
	Name                              string
	ModelRef                          string
	Provider                          string
	Model                             string
	BaseURL                           string
	APIKey                            string
	ContextLength                     int
	InputTokenPricePerMillion         float64
	OutputTokenPricePerMillion        float64
	CachedInputTokenPricePerMillion   float64
	CacheCreationTokenPricePerMillion float64
	DisableStreaming                  bool
	PromptCache                       bool
	Description                       string
	Instruction                       string
	Enabled                           bool
	Leader                            bool
	BuiltIn                           bool
	AllowFileAttachments              bool
	Tools                             []string
	// Skills is the explicit list of skill names this agent can access from
	// the shared registry. Nil/empty means all installed skills are visible.
	Skills        []string
	SoftSkillsDir string
	MCPConfigPath string
	// MCPServers is the per-agent whitelist of MCP server names (matching
	// `name` fields in the resolved mcp_config.json). An empty / unset list
	// means the agent gets NO MCP servers — opt-in is explicit.
	MCPServers            []string
	PermissionsConfigPath string
	// A2AAgents is the per-agent list of A2A agent names this agent can reach.
	A2AAgents []string
	// MaxInstances is the resolved parallel-invocation cap (always >= 1).
	MaxInstances int
	// MaxToolCalls is this agent's per-turn tool-call cap (0 = uncapped).
	MaxToolCalls int
	// ResumableSessions is the resolved (opt-out default applied) flag for durable,
	// re-attachable sub-agent sessions: true unless the agent explicitly opted out.
	// The leader can resume a prior call via its returned handle. See AgentEntry.
	ResumableSessions bool
	// SubAgents is this agent's own delegable team (see AgentEntry.SubAgents).
	// Validated as an acyclic graph over enabled agents by validateSubAgentGraph.
	SubAgents []string
}

// RuntimeSquadConfig is one normalized squad: a named group composed of an
// existing leader agent plus a set of member sub-agents. Members are
// references by name into RuntimeSettings.Agents; the squad itself does not
// own agent definitions, skills, tools or MCP — those live on the agents.
type RuntimeSquadConfig struct {
	Name        string
	Description string
	Leader      string
	Members     []string
}

// DefaultSquadName is the name of the squad used when a session does not
// specify one. Always present in RuntimeSettings.Squads after resolution
// (synthesised when the config file does not declare one).
const DefaultSquadName = "default"

// RuntimeSettings is the merged runtime configuration after precedence
// resolution: defaults -> JSON -> ENV -> Options.
type RuntimeSettings struct {
	ConfigPath              string
	ModelsConfigPath        string
	Providers               map[string]RuntimeProviderConfig
	SoftSkillsDir           string
	AppName                 string
	BashOutputFilterEnabled bool
	BashOutputFiltersDir    string
	BashTimeoutSeconds      int
	MCPConfigPath           string
	PermissionsConfigPath   string
	// HooksConfigPath is the resolved path to hooks.json, defining Claude
	// Code-style lifecycle hooks (shell commands fired before/after tools, on
	// prompt submit, on stop, etc.). Empty/missing means no hooks.
	HooksConfigPath string
	// A2AConfigPath is the resolved path to a2a_config.json, defining remote
	// A2A agent endpoints that any agent's `a2a_agents` list can reference.
	A2AConfigPath string
	SerpAPIKey    string
	// EmbedModelRef names the model in Models used as the internal embedder for
	// semantic recall. Empty means no config-selected embedder (the OMNIS_EMBED_*
	// environment may still provide one).
	EmbedModelRef string
	// EvalModelRef names the model in Models used for internal one-off evaluator
	// calls (the /goal completion judge). Empty means the evaluator falls back to
	// the session's leader model. See modelsConfigFile.EvalModelRef.
	EvalModelRef string
	// OverrideModelRef / OverrideModelEnabled implement the "single model for all
	// agents" switch: when enabled and the ref resolves in Models, every agent's
	// model connection + pricing fields are overwritten with that model
	// (applyModelOverride). Disabled restores the per-agent config. Sourced from
	// models.json + OMNIS_OVERRIDE_MODEL_REF / OMNIS_OVERRIDE_MODEL_ENABLED.
	OverrideModelRef     string
	OverrideModelEnabled bool
	Models               map[string]RuntimeModelConfig
	Agents               []RuntimeAgentConfig
	// Squads is the normalised list of named agent groups. Always contains
	// at least one entry named DefaultSquadName.
	Squads []RuntimeSquadConfig
	// RouterSquad is the resolved name of the Omnis router squad (the default
	// agent for new chats). Empty means routing is disabled (opt-out). The
	// router agent + leaderless squad are injected by ensureRouterSquad in the
	// build path when missing.
	RouterSquad string
	// Curator gate thresholds (OMNIS_CURATOR_MIN_TURNS / OMNIS_CURATOR_MIN_SUB_AGENT_CALLS).
	// Zero values fall back to the defaults in CuratorGateConfig.
	CuratorMinTurns         int
	CuratorMinSubAgentCalls int
	// CuratorIdleTimeout is the idle-session duration after which the Web UI
	// server fires an automatic curation run (OMNIS_CURATOR_IDLE_TIMEOUT).
	// Zero means disabled.
	CuratorIdleTimeout time.Duration
	// TurnBudget is the resolved per-turn spend ceiling. When a turn crosses it
	// the user is asked whether to continue; an unlimited value (both axes zero)
	// disables the gate entirely. See internal/budget and agent/budget_plugin.go.
	TurnBudget budget.Limits
}

// AgentConfig returns the effective config for one agent name.
func (s RuntimeSettings) AgentConfig(name string) (RuntimeAgentConfig, bool) {
	needle := strings.ToLower(strings.TrimSpace(name))
	if needle == "" {
		return RuntimeAgentConfig{}, false
	}
	for _, cfg := range s.Agents {
		if strings.ToLower(strings.TrimSpace(cfg.Name)) == needle {
			return cfg, true
		}
	}
	return RuntimeAgentConfig{}, false
}

// LeaderConfig returns the mandatory leader agent configuration.
func (s RuntimeSettings) LeaderConfig() (RuntimeAgentConfig, bool) {
	return s.AgentConfig("leader")
}

// Squad returns the squad with the given name (case-insensitive).
func (s RuntimeSettings) Squad(name string) (RuntimeSquadConfig, bool) {
	needle := strings.ToLower(strings.TrimSpace(name))
	if needle == "" {
		return RuntimeSquadConfig{}, false
	}
	for _, sq := range s.Squads {
		if sq.Name == needle {
			return sq, true
		}
	}
	return RuntimeSquadConfig{}, false
}

// DefaultSquad returns the squad named DefaultSquadName. Callers can rely on
// it being present after ResolveRuntimeSettings.
func (s RuntimeSettings) DefaultSquad() (RuntimeSquadConfig, bool) {
	return s.Squad(DefaultSquadName)
}

// normalizeNames lower-cases, trims and de-dups a list of names while
// preserving order. Returns nil for an empty input so the field round-trips
// cleanly through JSON.
func normalizeNames(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, raw := range in {
		t := strings.ToLower(strings.TrimSpace(raw))
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func normalizeTools(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, raw := range in {
		t := strings.TrimSpace(raw)
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}

func defaultAgents() []RuntimeAgentConfig {
	return []RuntimeAgentConfig{
		{
			Name:    "leader",
			Enabled: true,
			Leader:  true,
		},
		{
			Name:    "investigator",
			Enabled: true,
			Tools:   []string{"fs", "mcp"},
		},
		{
			Name:    "summariser",
			Enabled: true,
			Tools:   []string{},
		},
		{
			Name:    "curator",
			Enabled: true,
		},
	}
}

func normalizeProviderCatalog(providers map[string]ProviderEntry) map[string]RuntimeProviderConfig {
	if len(providers) == 0 {
		return map[string]RuntimeProviderConfig{}
	}
	out := make(map[string]RuntimeProviderConfig, len(providers))
	for rawName, p := range providers {
		name := strings.ToLower(strings.TrimSpace(rawName))
		if name == "" {
			continue
		}
		out[name] = RuntimeProviderConfig{
			Name:    name,
			Kind:    strings.TrimSpace(p.Kind),
			BaseURL: resolveBaseURLReference(strings.TrimSpace(p.BaseURL)),
			APIKey:  resolveAPIKeyReference(strings.TrimSpace(p.APIKey)),
		}
	}
	return out
}

func normalizeModelCatalog(models map[string]ModelEntry, providers map[string]RuntimeProviderConfig) (map[string]RuntimeModelConfig, error) {
	if len(models) == 0 {
		return map[string]RuntimeModelConfig{}, nil
	}
	out := make(map[string]RuntimeModelConfig, len(models))
	for rawName, m := range models {
		name := strings.ToLower(strings.TrimSpace(rawName))
		if name == "" {
			continue
		}
		providerRef := strings.ToLower(strings.TrimSpace(m.ProviderRef))
		var refProvider RuntimeProviderConfig
		if providerRef != "" {
			p, ok := providers[providerRef]
			if !ok {
				return nil, fmt.Errorf("models config: model %q references unknown provider_ref %q", name, providerRef)
			}
			refProvider = p
		}
		provider := firstNonEmpty(strings.TrimSpace(m.Provider), refProvider.Kind)
		out[name] = RuntimeModelConfig{
			Name:                              name,
			Provider:                          provider,
			Model:                             strings.TrimSpace(m.Model),
			BaseURL:                           resolveBaseURLReference(firstNonEmpty(strings.TrimSpace(m.BaseURL), refProvider.BaseURL)),
			APIKey:                            resolveAPIKeyReference(firstNonEmpty(strings.TrimSpace(m.APIKey), refProvider.APIKey)),
			ContextLength:                     m.ContextLength,
			InputTokenPricePerMillion:         m.InputTokenPricePerMillion,
			OutputTokenPricePerMillion:        m.OutputTokenPricePerMillion,
			CachedInputTokenPricePerMillion:   m.CachedInputTokenPricePerMillion,
			CacheCreationTokenPricePerMillion: m.CacheCreationTokenPricePerMillion,
			Embedding:                         m.Embedding,
			Dim:                               m.Dim,
			DisableStreaming:                  m.DisableStreaming,
			PromptCache:                       promptCacheEnabled(m.PromptCache, provider),
		}
	}
	return out, nil
}

func resolveAgentEntries(entries []AgentEntry, modelCatalog map[string]RuntimeModelConfig) ([]RuntimeAgentConfig, error) {
	out := make([]RuntimeAgentConfig, 0, len(entries))
	for _, e := range entries {
		name := strings.ToLower(strings.TrimSpace(e.Name))
		if name == "" {
			continue
		}
		modelRef := strings.ToLower(strings.TrimSpace(e.ModelRef))
		refModel := RuntimeModelConfig{}
		if modelRef != "" {
			m, ok := modelCatalog[modelRef]
			if !ok {
				return nil, fmt.Errorf("runtime config: agent %q references unknown model_ref %q", name, modelRef)
			}
			refModel = m
		}
		enabled := true
		if e.Enabled != nil {
			enabled = *e.Enabled
		}
		// Leader flag controls squad-leader eligibility and teammate-tool
		// wiring. The agent literally named "leader" is the canonical default
		// and is always leader-eligible (and always enabled). Any other agent
		// can be marked leader explicitly.
		leader := false
		if e.Leader != nil {
			leader = *e.Leader
		}
		if name == "leader" {
			enabled = true
			leader = true
		}
		allowFileAttachments := false
		if e.AllowFileAttachments != nil {
			allowFileAttachments = *e.AllowFileAttachments
		}
		builtIn := false
		if e.BuiltIn != nil {
			builtIn = *e.BuiltIn
		}
		maxInstances := e.MaxInstances
		if maxInstances < 1 {
			maxInstances = 1
		}
		out = append(out, RuntimeAgentConfig{
			Name:                              name,
			ModelRef:                          modelRef,
			Provider:                          refModel.Provider,
			Model:                             refModel.Model,
			BaseURL:                           refModel.BaseURL,
			APIKey:                            refModel.APIKey,
			ContextLength:                     refModel.ContextLength,
			InputTokenPricePerMillion:         refModel.InputTokenPricePerMillion,
			OutputTokenPricePerMillion:        refModel.OutputTokenPricePerMillion,
			CachedInputTokenPricePerMillion:   refModel.CachedInputTokenPricePerMillion,
			CacheCreationTokenPricePerMillion: refModel.CacheCreationTokenPricePerMillion,
			DisableStreaming:                  refModel.DisableStreaming,
			PromptCache:                       refModel.PromptCache,
			Description:                       strings.TrimSpace(e.Description),
			Instruction:                       strings.TrimSpace(e.Instruction),
			Enabled:                           enabled,
			Leader:                            leader,
			BuiltIn:                           builtIn,
			AllowFileAttachments:              allowFileAttachments,
			Tools:                             normalizeTools(e.Tools),
			Skills:                            normalizeNames(e.Skills),
			SoftSkillsDir:                     strings.TrimSpace(e.SoftSkillsDir),
			MCPConfigPath:                     strings.TrimSpace(e.MCPConfigPath),
			MCPServers:                        normalizeNames(e.MCPServers),
			PermissionsConfigPath:             strings.TrimSpace(e.PermissionsConfigPath),
			A2AAgents:                         normalizeNames(e.A2AAgents),
			MaxInstances:                      maxInstances,
			MaxToolCalls:                      maxInt(e.MaxToolCalls, 0),
			ResumableSessions:                 resumableEnabled(e.ResumableSessions),
			SubAgents:                         normalizeNames(e.SubAgents),
		})
	}
	return out, nil
}

func inheritAgentModelFromLeader(in RuntimeAgentConfig, leader RuntimeAgentConfig) RuntimeAgentConfig {
	out := in
	if strings.TrimSpace(out.Provider) == "" {
		out.Provider = leader.Provider
	}
	if strings.TrimSpace(out.Model) == "" {
		out.Model = leader.Model
	}
	if strings.TrimSpace(out.BaseURL) == "" {
		out.BaseURL = leader.BaseURL
	}
	if strings.TrimSpace(out.APIKey) == "" {
		out.APIKey = leader.APIKey
	}
	if out.ContextLength == 0 {
		out.ContextLength = leader.ContextLength
	}
	if out.InputTokenPricePerMillion == 0 {
		out.InputTokenPricePerMillion = leader.InputTokenPricePerMillion
	}
	if out.OutputTokenPricePerMillion == 0 {
		out.OutputTokenPricePerMillion = leader.OutputTokenPricePerMillion
	}
	if out.CachedInputTokenPricePerMillion == 0 {
		out.CachedInputTokenPricePerMillion = leader.CachedInputTokenPricePerMillion
	}
	if out.CacheCreationTokenPricePerMillion == 0 {
		out.CacheCreationTokenPricePerMillion = leader.CacheCreationTokenPricePerMillion
	}
	return out
}

func withInheritedModels(agents []RuntimeAgentConfig) ([]RuntimeAgentConfig, error) {
	var leader RuntimeAgentConfig
	foundLeader := false
	for _, a := range agents {
		if a.Name == "leader" {
			leader = a
			foundLeader = true
			break
		}
	}
	if !foundLeader {
		return nil, fmt.Errorf("runtime config: missing mandatory agents entry with name=leader")
	}
	out := make([]RuntimeAgentConfig, 0, len(agents))
	for _, a := range agents {
		if a.Name == "leader" {
			out = append(out, a)
			continue
		}
		out = append(out, inheritAgentModelFromLeader(a, leader))
	}
	return out, nil
}

func applyCuratorEnabledOverride(agents []RuntimeAgentConfig, enabled bool) []RuntimeAgentConfig {
	for i := range agents {
		if agents[i].Name == "curator" {
			agents[i].Enabled = enabled
			return agents
		}
	}
	agents = append(agents, RuntimeAgentConfig{Name: "curator", Enabled: enabled})
	return agents
}

func mapAgentEntries(entries []RuntimeAgentConfig, fn func(RuntimeAgentConfig) RuntimeAgentConfig) []RuntimeAgentConfig {
	out := make([]RuntimeAgentConfig, 0, len(entries))
	for _, e := range entries {
		out = append(out, fn(e))
	}
	return out
}

func normalizedAgentConfig(in RuntimeAgentConfig) RuntimeAgentConfig {
	return RuntimeAgentConfig{
		Name:                              strings.ToLower(strings.TrimSpace(in.Name)),
		ModelRef:                          strings.ToLower(strings.TrimSpace(in.ModelRef)),
		Provider:                          strings.TrimSpace(in.Provider),
		Model:                             strings.TrimSpace(in.Model),
		BaseURL:                           strings.TrimSpace(in.BaseURL),
		APIKey:                            strings.TrimSpace(in.APIKey),
		ContextLength:                     in.ContextLength,
		InputTokenPricePerMillion:         in.InputTokenPricePerMillion,
		OutputTokenPricePerMillion:        in.OutputTokenPricePerMillion,
		CachedInputTokenPricePerMillion:   in.CachedInputTokenPricePerMillion,
		CacheCreationTokenPricePerMillion: in.CacheCreationTokenPricePerMillion,
		DisableStreaming:                  in.DisableStreaming,
		PromptCache:                       in.PromptCache,
		Description:                       strings.TrimSpace(in.Description),
		Instruction:                       strings.TrimSpace(in.Instruction),
		Enabled:                           in.Enabled,
		Leader:                            in.Leader,
		BuiltIn:                           in.BuiltIn,
		AllowFileAttachments:              in.AllowFileAttachments,
		Tools:                             normalizeTools(in.Tools),
		Skills:                            normalizeNames(in.Skills),
		SoftSkillsDir:                     strings.TrimSpace(in.SoftSkillsDir),
		MCPConfigPath:                     strings.TrimSpace(in.MCPConfigPath),
		MCPServers:                        normalizeNames(in.MCPServers),
		PermissionsConfigPath:             strings.TrimSpace(in.PermissionsConfigPath),
		A2AAgents:                         normalizeNames(in.A2AAgents),
		MaxInstances:                      maxInt(in.MaxInstances, 1),
		MaxToolCalls:                      maxInt(in.MaxToolCalls, 0),
		ResumableSessions:                 in.ResumableSessions,
		SubAgents:                         normalizeNames(in.SubAgents),
	}
}

// resumableEnabled resolves the per-agent resumable-sessions flag. Durable,
// re-attachable sub-agent sessions are ON by default (opt-out): an absent flag
// (nil) means enabled; only an explicit false disables them.
func resumableEnabled(p *bool) bool {
	return p == nil || *p
}

// promptCacheEnabled resolves the per-model prompt_cache flag (opt-out for
// gateways). An explicit value always wins; when absent (nil) the default is ON
// for `openai_compat` providers and OFF otherwise. Rationale: client-side
// `cache_control` breakpoints are what make Anthropic prompt caching engage
// behind a LiteLLM/OpenAI-compat gateway (an un-annotated request caches 0%),
// and non-caching openai_compat backends (e.g. Scaleway-hosted Llama/Mistral/GLM
// behind LiteLLM) silently ignore the annotation — verified to return HTTP 200
// with the field dropped, never an error. A plain `openai` endpoint caches
// automatically server-side and may reject the unrecognised field, so it stays
// off unless opted in. Only the openai/openai_compat adapters honour the flag
// (see applyModelPrefs); gemini/native-anthropic ignore it regardless.
func promptCacheEnabled(p *bool, provider string) bool {
	if p != nil {
		return *p
	}
	return strings.EqualFold(strings.TrimSpace(provider), "openai_compat")
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// resolveSquadEntries normalises raw JSON squad entries against the agent
// catalogue. It enforces:
//   - non-empty squad name; names lower-cased and unique
//   - leader and members reference existing, enabled agents
//   - the squad's leader is an agent marked `leader: true`
//   - curator is not a member (it is process-wide)
//   - members are de-duplicated; the leader is never listed as a member
//
// Returns the resolved squads and an error describing the first violation.
func resolveSquadEntries(entries []SquadEntry, agents []RuntimeAgentConfig) ([]RuntimeSquadConfig, error) {
	enabled := map[string]RuntimeAgentConfig{}
	for _, a := range agents {
		if a.Enabled {
			enabled[a.Name] = a
		}
	}
	seenName := map[string]bool{}
	out := make([]RuntimeSquadConfig, 0, len(entries))
	for _, e := range entries {
		name := strings.ToLower(strings.TrimSpace(e.Name))
		if name == "" {
			return nil, fmt.Errorf("runtime config: squad has empty name")
		}
		if seenName[name] {
			return nil, fmt.Errorf("runtime config: duplicate squad name %q", name)
		}
		seenName[name] = true
		leader := strings.ToLower(strings.TrimSpace(e.Leader))
		// A leaderless squad (leader "" or "none") runs a single member agent
		// directly as the runner root — no coordinator. It must declare exactly
		// one member, which need not be marked leader:true.
		leaderless := leader == "" || leader == "none"
		seenMember := map[string]bool{}
		if !leaderless {
			leaderCfg, ok := enabled[leader]
			if !ok {
				return nil, fmt.Errorf("runtime config: squad %q leader %q is not an enabled agent", name, leader)
			}
			if !leaderCfg.Leader {
				return nil, fmt.Errorf("runtime config: squad %q leader %q is not marked as leader: true", name, leader)
			}
			seenMember[leader] = true
		}
		members := make([]string, 0, len(e.Members))
		for _, raw := range e.Members {
			m := strings.ToLower(strings.TrimSpace(raw))
			if m == "" || seenMember[m] {
				continue
			}
			seenMember[m] = true
			if _, ok := enabled[m]; !ok {
				return nil, fmt.Errorf("runtime config: squad %q member %q is not an enabled agent", name, m)
			}
			if m == "curator" {
				return nil, fmt.Errorf("runtime config: squad %q cannot include the curator agent (curator is process-wide)", name)
			}
			members = append(members, m)
		}
		if leaderless {
			// Normalise the leaderless marker to "" internally; buildSquadInstance
			// keys on an empty Leader.
			leader = ""
			if len(members) != 1 {
				return nil, fmt.Errorf("runtime config: leaderless squad %q must have exactly one member (got %d); set a leader to coordinate multiple agents", name, len(members))
			}
		}
		out = append(out, RuntimeSquadConfig{
			Name:        name,
			Description: strings.TrimSpace(e.Description),
			Leader:      leader,
			Members:     members,
		})
	}
	return out, nil
}

// synthesizeDefaultSquad builds a `default` squad from the enabled agents
// when no `squads:` block is present. The leader is the agent named
// "leader" (mandatory); members are every other enabled agent except
// "curator" (which is process-wide).
func synthesizeDefaultSquad(agents []RuntimeAgentConfig) RuntimeSquadConfig {
	sq := RuntimeSquadConfig{Name: DefaultSquadName, Leader: "leader"}
	for _, a := range agents {
		if !a.Enabled || a.Name == "leader" || a.Name == "curator" {
			continue
		}
		sq.Members = append(sq.Members, a.Name)
	}
	return sq
}

// ensureDefaultSquad guarantees the squad list contains an entry named
// DefaultSquadName. When the caller provided squads but none is named
// "default", a synthesised default is prepended so the resolved list
// always has a fallback for sessions that don't specify a squad. This
// keeps the editor UX friendly: a user who creates a single non-default
// squad doesn't have to manually re-declare the default one alongside it.
func ensureDefaultSquad(squads []RuntimeSquadConfig, agents []RuntimeAgentConfig) ([]RuntimeSquadConfig, error) {
	for _, sq := range squads {
		if sq.Name == DefaultSquadName {
			return squads, nil
		}
	}
	synth := synthesizeDefaultSquad(agents)
	if len(squads) == 0 {
		return []RuntimeSquadConfig{synth}, nil
	}
	return append([]RuntimeSquadConfig{synth}, squads...), nil
}

// loadAgentFromRegistry loads an agent definition from the registry.
// Path is {registryDir}/{name}/agent.json. If the agent's name field is
// empty, it is inferred from the directory name.
// loadAgentFromRegistry searches registryDirs in order and returns the first
// agent.json found. This mirrors the config 3-layer lookup so that a
// $OMNIS_HOME/registry/agents/<name>/agent.json override takes precedence over
// ./registry/agents/<name>/agent.json without hiding agents that only exist in
// one of the layers.
// loadAgentFromRegistry resolves one agent by name across the registry search
// chain. Unlike a first-existing-wins lookup, it DEEP-MERGES the agent.json from
// every layer (low→high, configedit.MergeGeneric) so a per-user overlay that
// changes one field of a package-shipped agent keeps evolving with package
// updates to the agent's other fields, instead of shadowing the whole entry.
// instruction.md is taken from the highest-precedence layer that has one, and
// its frontmatter overlays the merged agent.json (the Claude Code–style
// markdown-agent contract). registryDirs is high→low precedence.
func loadAgentFromRegistry(name string, registryDirs []string) (AgentEntry, error) {
	// Collect agent.json maps low→high so higher layers overlay lower ones.
	var layers []map[string]any
	found := false
	for i := len(registryDirs) - 1; i >= 0; i-- {
		p := filepath.Join(registryDirs[i], name, "agent.json")
		jsonBytes, err := os.ReadFile(p)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return AgentEntry{}, fmt.Errorf("agent registry %q: %w", p, err)
		}
		var m map[string]any
		if err := json.Unmarshal(jsonBytes, &m); err != nil {
			return AgentEntry{}, fmt.Errorf("agent registry %q: decode json: %w", p, err)
		}
		layers = append(layers, m)
		found = true
	}

	// instruction.md from the highest-precedence layer (high→low, first wins).
	var instrBytes []byte
	instrFound := false
	for _, dir := range registryDirs {
		if ib, err := os.ReadFile(filepath.Join(dir, name, "instruction.md")); err == nil {
			instrBytes = ib
			instrFound = true
			found = true
			break
		}
	}

	if !found {
		return AgentEntry{}, fmt.Errorf("agent %q not found in any registry directory", name)
	}

	var e AgentEntry
	if len(layers) > 0 {
		merged := configedit.MergeGeneric(layers)
		mb, err := json.Marshal(merged)
		if err != nil {
			return AgentEntry{}, fmt.Errorf("agent registry %q: merge: %w", name, err)
		}
		if err := json.Unmarshal(mb, &e); err != nil {
			return AgentEntry{}, fmt.Errorf("agent registry %q: decode merged json: %w", name, err)
		}
	}
	if e.Name == "" {
		e.Name = name
	}
	if instrFound {
		if fm, _ := ParseInstructionMarkdown(instrBytes); fm.HasAny() {
			applyInstructionFrontmatter(&e, fm)
		}
	}
	return e, nil
}

// applyInstructionFrontmatter overlays frontmatter values onto an AgentEntry.
// The model field is intentionally treated as a recommendation only — the
// frontmatter never silently rewires which provider/model the runtime targets.
// The Web UI surfaces unresolved recommendations via a separate channel.
func applyInstructionFrontmatter(e *AgentEntry, fm InstructionFrontmatter) {
	if fm.Name != "" {
		e.Name = fm.Name
	}
	if fm.Description != "" {
		e.Description = fm.Description
	}
	if len(fm.Tools) > 0 {
		e.Tools = fm.Tools
	}
	if len(fm.Skills) > 0 {
		e.Skills = fm.Skills
	}
	if len(fm.MCPServers) > 0 {
		e.MCPServers = fm.MCPServers
	}
}

// ResolveRuntimeSettings loads and merges runtime settings using precedence:
// defaults -> JSON -> ENV -> Options.
func ResolveRuntimeSettings(opts Options) (RuntimeSettings, error) {
	out := RuntimeSettings{
		ConfigPath:              paths.FindConfig("agents.json"),
		ModelsConfigPath:        paths.FindConfig("models.json"),
		SoftSkillsDir:           paths.SoftSkillsDir(),
		AppName:                 "omnis",
		BashOutputFilterEnabled: false,
		BashOutputFiltersDir:    paths.FindConfigDir("filters"),
		BashTimeoutSeconds:      120,
		MCPConfigPath:           paths.FindConfig("mcp_config.json"),
		PermissionsConfigPath:   paths.FindConfig("permissions.json"),
		HooksConfigPath:         paths.FindConfig("hooks.json"),
		A2AConfigPath:           paths.FindConfig("a2a_config.json"),
		Providers:               map[string]RuntimeProviderConfig{},
		Models:                  map[string]RuntimeModelConfig{},
		Agents:                  defaultAgents(),
		TurnBudget: budget.Limits{
			MaxToolCalls: budget.DefaultMaxToolCalls,
			MaxTokens:    budget.DefaultMaxTokens,
		},
	}

	explicitConfig := strings.TrimSpace(opts.ConfigPath) != ""
	if explicitConfig {
		out.ConfigPath = strings.TrimSpace(opts.ConfigPath)
	}

	cfg, err := loadRuntimeConfig(out.ConfigPath, explicitConfig)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) && !opts.ConfigPathStrict {
			cfg = runtimeConfigFile{}
		} else {
			return RuntimeSettings{}, err
		}
	}

	// Hard break on the legacy in-line models block. Direct the user to
	// the new models.json file rather than silently honouring stale config.
	if len(cfg.LegacyModels) > 0 && string(cfg.LegacyModels) != "null" {
		return RuntimeSettings{}, fmt.Errorf("runtime config %q: \"models\" must be defined in models.json (move the block to %s and remove it from agents.json)", out.ConfigPath, out.ModelsConfigPath)
	}

	// Load models.json (providers + models). Missing file is always fine:
	// the catalogue is auto-discovered, and agents without a resolvable
	// model_ref simply fall back to inline or leader-inherited fields.
	modelsCfg := modelsConfigFile{}
	if loaded, mErr := loadModelsConfig(out.ModelsConfigPath); mErr == nil {
		modelsCfg = loaded
	} else if !errors.Is(mErr, os.ErrNotExist) {
		return RuntimeSettings{}, mErr
	}
	out.Providers = normalizeProviderCatalog(modelsCfg.Providers)
	out.Models, err = normalizeModelCatalog(modelsCfg.Models, out.Providers)
	if err != nil {
		return RuntimeSettings{}, err
	}
	if strings.TrimSpace(modelsCfg.EmbedModelRef) != "" {
		out.EmbedModelRef = strings.ToLower(strings.TrimSpace(modelsCfg.EmbedModelRef))
	}
	if strings.TrimSpace(modelsCfg.EvalModelRef) != "" {
		out.EvalModelRef = strings.ToLower(strings.TrimSpace(modelsCfg.EvalModelRef))
	}
	// Single-model override ("run the whole fleet on one model"). The ref is kept
	// even while disabled, so the toggle flips cleanly between the single model
	// and the per-agent config; applyModelOverride (below) is what enforces it.
	out.OverrideModelRef = strings.ToLower(strings.TrimSpace(modelsCfg.OverrideModelRef))
	out.OverrideModelEnabled = modelsCfg.OverrideModelEnabled

	// File
	if strings.TrimSpace(cfg.SoftSkillsDir) != "" {
		out.SoftSkillsDir = strings.TrimSpace(cfg.SoftSkillsDir)
	}
	if strings.TrimSpace(cfg.AppName) != "" {
		out.AppName = strings.TrimSpace(cfg.AppName)
	}
	out.BashOutputFilterEnabled = cfg.TokenOptimization
	if strings.TrimSpace(cfg.BashOutputFiltersDir) != "" {
		out.BashOutputFiltersDir = strings.TrimSpace(cfg.BashOutputFiltersDir)
	}
	if cfg.BashTimeoutSeconds > 0 {
		out.BashTimeoutSeconds = cfg.BashTimeoutSeconds
	}
	// Per-turn spend ceiling. Each axis is set independently so a config can
	// cap tokens but not tool calls (or vice-versa) by pinning the other to 0.
	if tb := cfg.TurnBudget; tb != nil {
		if tb.MaxToolCalls != nil {
			out.TurnBudget.MaxToolCalls = *tb.MaxToolCalls
		}
		if tb.MaxTokens != nil {
			out.TurnBudget.MaxTokens = *tb.MaxTokens
		}
	}
	if strings.TrimSpace(cfg.MCPConfigPath) != "" {
		out.MCPConfigPath = strings.TrimSpace(cfg.MCPConfigPath)
	}
	if strings.TrimSpace(cfg.PermissionsConfigPath) != "" {
		out.PermissionsConfigPath = strings.TrimSpace(cfg.PermissionsConfigPath)
	}
	if strings.TrimSpace(cfg.HooksConfigPath) != "" {
		out.HooksConfigPath = strings.TrimSpace(cfg.HooksConfigPath)
	}
	if strings.TrimSpace(cfg.SerpAPIKey) != "" {
		out.SerpAPIKey = resolveAPIKeyReference(strings.TrimSpace(cfg.SerpAPIKey))
	}
	if strings.TrimSpace(cfg.EmbedModelRef) != "" {
		out.EmbedModelRef = strings.ToLower(strings.TrimSpace(cfg.EmbedModelRef))
	}
	if strings.TrimSpace(cfg.EvalModelRef) != "" {
		out.EvalModelRef = strings.ToLower(strings.TrimSpace(cfg.EvalModelRef))
	}
	if len(cfg.Agents) > 0 {
		agentsRegistryDirs := paths.AgentsRegistrySearchDirs()
		entries := make([]AgentEntry, 0, len(cfg.Agents))
		for _, name := range cfg.Agents {
			e, err := loadAgentFromRegistry(strings.ToLower(strings.TrimSpace(name)), agentsRegistryDirs)
			if err != nil {
				return RuntimeSettings{}, err
			}
			entries = append(entries, e)
		}
		out.Agents, err = resolveAgentEntries(entries, out.Models)
		if err != nil {
			return RuntimeSettings{}, err
		}
	}

	// ENV
	if v, ok := parseBoolEnv("OMNIS_CURATOR_ENABLED"); ok {
		out.Agents = applyCuratorEnabledOverride(out.Agents, v)
	}
	// Per-turn spend ceiling. `>= 0` (not `> 0`) so an explicit 0 is honoured as
	// "no ceiling on this axis" — setting both to 0 restores unbounded turns.
	if v, err := strconv.Atoi(strings.TrimSpace(os.Getenv("OMNIS_TURN_MAX_TOOL_CALLS"))); err == nil && v >= 0 {
		out.TurnBudget.MaxToolCalls = v
	}
	if v, err := strconv.ParseInt(strings.TrimSpace(os.Getenv("OMNIS_TURN_MAX_TOKENS")), 10, 64); err == nil && v >= 0 {
		out.TurnBudget.MaxTokens = v
	}
	if v, err := strconv.Atoi(strings.TrimSpace(os.Getenv("OMNIS_CURATOR_MIN_TURNS"))); err == nil && v > 0 {
		out.CuratorMinTurns = v
	}
	if v, err := strconv.Atoi(strings.TrimSpace(os.Getenv("OMNIS_CURATOR_MIN_SUB_AGENT_CALLS"))); err == nil && v > 0 {
		out.CuratorMinSubAgentCalls = v
	}
	if raw := strings.TrimSpace(os.Getenv("OMNIS_CURATOR_IDLE_TIMEOUT")); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil && d > 0 {
			out.CuratorIdleTimeout = d
		}
	}
	if raw := strings.TrimSpace(os.Getenv("OMNIS_EMBED_MODEL_REF")); raw != "" {
		out.EmbedModelRef = strings.ToLower(raw)
	}
	if raw := strings.TrimSpace(os.Getenv("OMNIS_GOAL_MODEL_REF")); raw != "" {
		out.EvalModelRef = strings.ToLower(raw)
	}
	// OMNIS_OVERRIDE_MODEL_REF sets the single-model-override ref and, when
	// non-empty, enables the override; OMNIS_OVERRIDE_MODEL_ENABLED then has the
	// final say on the flag (so the env can force it on/off independently).
	if raw := strings.TrimSpace(os.Getenv("OMNIS_OVERRIDE_MODEL_REF")); raw != "" {
		out.OverrideModelRef = strings.ToLower(raw)
		out.OverrideModelEnabled = true
	}
	if v, ok := parseBoolEnv("OMNIS_OVERRIDE_MODEL_ENABLED"); ok {
		out.OverrideModelEnabled = v
	}

	// Options (highest precedence)
	if strings.TrimSpace(opts.SoftSkillsDir) != "" {
		out.SoftSkillsDir = strings.TrimSpace(opts.SoftSkillsDir)
	}
	if strings.TrimSpace(opts.AppName) != "" {
		out.AppName = strings.TrimSpace(opts.AppName)
	}
	if strings.TrimSpace(opts.MCPSConfigPath) != "" {
		out.MCPConfigPath = strings.TrimSpace(opts.MCPSConfigPath)
	}
	if strings.TrimSpace(opts.PermissionsConfigPath) != "" {
		out.PermissionsConfigPath = strings.TrimSpace(opts.PermissionsConfigPath)
	}
	if opts.CuratorEnabled != nil {
		out.Agents = applyCuratorEnabledOverride(out.Agents, *opts.CuratorEnabled)
	} else if opts.DisableAutoCurate {
		// Backward-compatible alias for explicitly disabling the hook.
		out.Agents = applyCuratorEnabledOverride(out.Agents, false)
	}

	out.Agents = mapAgentEntries(out.Agents, normalizedAgentConfig)
	// Single-model override: when enabled in models.json, force every agent onto
	// one model. Applied before inheritance so it also short-circuits the
	// leader-model inheritance step (and its "no model" error) — a clean global
	// escape hatch. A no-op when disabled / unset / unresolvable.
	applyModelOverride(&out)
	out.Agents, err = withInheritedModels(out.Agents)
	if err != nil {
		return RuntimeSettings{}, err
	}

	// Nested delegation (`subagents`) is validated against the final agent
	// catalogue: an unknown/disabled target or a cycle can never be built, so it
	// must fail here — loudly, on reload — rather than mid-turn.
	if err := validateSubAgentGraph(out.Agents); err != nil {
		return RuntimeSettings{}, err
	}

	// Squads compose existing agents. Validated against the resolved agent
	// catalogue (post-inheritance) so leader/member references must be
	// enabled, real agents. When the JSON has no squads, synthesize a
	// `default` squad from the enabled agents so callers always have one.
	out.Squads, err = resolveSquadEntries(cfg.Squads, out.Agents)
	if err != nil {
		return RuntimeSettings{}, err
	}
	out.Squads, err = ensureDefaultSquad(out.Squads, out.Agents)
	if err != nil {
		return RuntimeSettings{}, err
	}

	// Resolve the router squad name (config → OMNIS_ROUTER_SQUAD env; absent
	// defaults to "omnis", "none" disables). The router agent + leaderless
	// squad are injected by ensureRouterSquad in the build path, not here, so
	// config-only tests see an unmodified squad list.
	out.RouterSquad = resolveRouterSquadName(cfg.RouterSquad)

	// Fold each agent's own `max_tool_calls` into the turn budget, so the per-turn
	// gate enforces both the shared spend ceiling and the per-agent design limits
	// from one place. Derived from the resolved agents (after every override), so
	// a hot-reload picks up a changed cap. No caps ⇒ the map stays nil and
	// Limits.Unlimited() is unaffected.
	for _, a := range out.Agents {
		if a.MaxToolCalls > 0 {
			if out.TurnBudget.PerAgent == nil {
				out.TurnBudget.PerAgent = map[string]int{}
			}
			out.TurnBudget.PerAgent[a.Name] = a.MaxToolCalls
		}
	}

	out.ConfigPath = filepath.Clean(out.ConfigPath)
	out.ModelsConfigPath = filepath.Clean(out.ModelsConfigPath)
	return out, nil
}

// applyModelOverride forces every agent onto a single model when the models.json
// "single model for all agents" override is enabled. It overwrites each agent's
// resolved model connection + pricing fields with the chosen catalogue model, so
// the whole fleet runs on one model regardless of each agent's own model_ref.
// Disabled, an empty/unresolvable ref, or a ref that points at an embedding model
// is a no-op — the per-agent multi-model config stands. The internal embedder and
// the /goal evaluator models are deliberately untouched (a chat model can't embed,
// and the evaluator is an internal, non-agent role). It runs before
// withInheritedModels, so it also settles agents that carry no model_ref of their
// own (they'd otherwise inherit the leader's).
func applyModelOverride(rs *RuntimeSettings) {
	if rs == nil || !rs.OverrideModelEnabled {
		return
	}
	ref := strings.ToLower(strings.TrimSpace(rs.OverrideModelRef))
	if ref == "" {
		return
	}
	m, ok := rs.Models[ref]
	if !ok || m.Embedding {
		return
	}
	for i := range rs.Agents {
		rs.Agents[i].ModelRef = ref
		rs.Agents[i].Provider = m.Provider
		rs.Agents[i].Model = m.Model
		rs.Agents[i].BaseURL = m.BaseURL
		rs.Agents[i].APIKey = m.APIKey
		rs.Agents[i].ContextLength = m.ContextLength
		rs.Agents[i].InputTokenPricePerMillion = m.InputTokenPricePerMillion
		rs.Agents[i].OutputTokenPricePerMillion = m.OutputTokenPricePerMillion
		rs.Agents[i].CachedInputTokenPricePerMillion = m.CachedInputTokenPricePerMillion
		rs.Agents[i].CacheCreationTokenPricePerMillion = m.CacheCreationTokenPricePerMillion
		rs.Agents[i].DisableStreaming = m.DisableStreaming
		rs.Agents[i].PromptCache = m.PromptCache
	}
}

// resolveAPIKeyReference interprets api_key as either a literal key or an
// environment variable name. If an env var with that exact name exists and is
// non-empty, the env value is used.
func resolveAPIKeyReference(v string) string {
	if v == "" {
		return ""
	}
	if resolved := os.Getenv(v); resolved != "" {
		return resolved
	}
	return v
}

func resolveBaseURLReference(v string) string {
	if v == "" {
		return ""
	}
	// If the value looks like a URL already, use it directly.
	// Env var names never contain "://" so this safely distinguishes literals
	// from references — avoiding the trap of returning the raw name as a URL
	// when the env var is unset (which would produce "OPENAI_BASE_URL/chat/completions").
	if strings.Contains(v, "://") {
		return v
	}
	return os.Getenv(v)
}

func parseBoolEnv(name string) (bool, bool) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return false, false
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return false, false
	}
	return v, true
}

// loadRuntimeConfig loads agents.json. When explicit is true the single named
// file is read verbatim (the OMNIS_CONFIG_PATH bypass); otherwise every layer of
// the search chain is deep-merged (configedit.MergedBytes) so a per-user overlay
// evolves with package updates instead of shadowing them. A missing file (no
// layer has it, or the explicit path is absent) returns os.ErrNotExist so the
// caller's "missing is fine unless strict" branch still applies.
func loadRuntimeConfig(path string, explicit bool) (runtimeConfigFile, error) {
	var b []byte
	if explicit {
		var err error
		b, err = os.ReadFile(path)
		if err != nil {
			return runtimeConfigFile{}, fmt.Errorf("runtime config %q: %w", path, err)
		}
	} else {
		merged, err := configedit.MergedBytes("agents.json")
		if err != nil {
			return runtimeConfigFile{}, fmt.Errorf("runtime config %q: %w", path, err)
		}
		if merged == nil {
			return runtimeConfigFile{}, fmt.Errorf("runtime config %q: %w", path, os.ErrNotExist)
		}
		b = merged
	}
	var cfg runtimeConfigFile
	if err := json.Unmarshal(b, &cfg); err != nil {
		return runtimeConfigFile{}, fmt.Errorf("runtime config %q: decode json: %w", path, err)
	}
	return cfg, nil
}

// loadModelsConfig deep-merges models.json across every layer of the search
// chain. A missing file (no layer has it) returns os.ErrNotExist so the caller's
// graceful "missing is fine" branch applies.
func loadModelsConfig(path string) (modelsConfigFile, error) {
	merged, err := configedit.MergedBytes("models.json")
	if err != nil {
		return modelsConfigFile{}, fmt.Errorf("models config %q: %w", path, err)
	}
	if merged == nil {
		return modelsConfigFile{}, fmt.Errorf("models config %q: %w", path, os.ErrNotExist)
	}
	var cfg modelsConfigFile
	if err := json.Unmarshal(merged, &cfg); err != nil {
		return modelsConfigFile{}, fmt.Errorf("models config %q: decode json: %w", path, err)
	}
	return cfg, nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

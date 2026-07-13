package settings

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
	"gopkg.in/yaml.v3"

	"github.com/blouargant/omnis/internal/configedit"
	"github.com/blouargant/omnis/internal/paths"
)

// ---- shared types --------------------------------------------------------

type sectionInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type getSettingsIn struct {
	Section string `json:"section,omitempty"`
}

type getSettingsOut struct {
	Section  string        `json:"section,omitempty"`
	Layer    string        `json:"layer,omitempty"`
	Path     string        `json:"path,omitempty"`
	Data     any           `json:"data,omitempty"`
	Sections []sectionInfo `json:"sections,omitempty"`
	Note     string        `json:"note,omitempty"`
}

// writeResult is the common return shape of every mutating tool.
type writeResult struct {
	OK              bool   `json:"ok"`
	Section         string `json:"section,omitempty"`
	WrittenPath     string `json:"written_path,omitempty"`
	Layer           string `json:"layer,omitempty"`
	Reloaded        bool   `json:"reloaded"`
	RestartRequired bool   `json:"restart_required,omitempty"`
	Note            string `json:"note,omitempty"`
}

type agentSummary struct {
	Name     string   `json:"name"`
	ModelRef string   `json:"model_ref,omitempty"`
	Model    string   `json:"model,omitempty"`
	Enabled  *bool    `json:"enabled,omitempty"`
	Leader   bool     `json:"leader,omitempty"`
	Builtin  bool     `json:"builtin,omitempty"`
	Tools    []string `json:"tools,omitempty"`
	Skills   []string `json:"skills,omitempty"`
	Layer    string   `json:"layer,omitempty"`
}

// NewTools builds the settings tool group: get_settings, set_preference,
// set_agent, set_model, update_config, remove_config.
func NewTools(deps Deps) []tool.Tool {
	return []tool.Tool{
		mustTool[getSettingsIn, getSettingsOut]("get_settings",
			"Show the current value of an omnis settings section and which config layer it lives in. "+
				"Sections: agents, squads, models, permissions, mcp, a2a, hooks, preferences, server. "+
				"Credentials (api_key/token/secret/password) are redacted. Call with NO arguments to list "+
				"the sections. Pair with the documentation tools: docs explain what a setting means, this "+
				"shows its current value. Argument: `section` (string, optional).",
			getSettings),

		mustTool[setPreferenceIn, writeResult]("set_preference",
			"Change a web-UI preference. `key` ∈ {theme, locale, notifications}. `value` is the new value as "+
				"a string: theme = a theme id (e.g. \"github-dark\"), locale ∈ {en,fr,es,de}, notifications = "+
				"\"true\"/\"false\". To revert a preference to its default, pass an EMPTY string (or \"default\") "+
				"as `value` — e.g. an empty theme resets to VS Code Light. Applies on the next page load; not a "+
				"hot-reload. Not security-sensitive.",
			setPreference),

		mustTool("set_agent",
			"Change one agent's definition (registry/agents/<name>/agent.json) and hot-reload. Argument `name` "+
				"is required; provide only the fields to change: `model_ref` (a key from models.json), `model` "+
				"(a raw model id), `enabled`, `description`, `tools` (string list), `skills` (string list), "+
				"`max_instances`, `max_tool_calls`, `resumable_sessions`, `subagents` (string list — the agents "+
				"THIS agent may delegate to; every name must be an enabled agent and the graph must stay acyclic, "+
				"or the config will fail to reload). The agent must already exist (use the registry tools to "+
				"install a new one). Covers \"switch agent X to model Y\".",
			setAgent(deps)),

		mustTool("set_model",
			"Add or change a models.json catalogue entry and hot-reload. `model_ref` is the catalogue key. "+
				"Provide only the fields to change: `model` (raw id), `provider_ref`, `context_length`, "+
				"`disable_streaming`, `prompt_cache`, `dim`, `embedding`, and the connection fields `provider`, "+
				"`base_url`, `api_key`. Changing a connection field is security-sensitive and is confirmed with "+
				"the user. Changing the active embedding model's identity requires a server restart (reported as "+
				"restart_required).",
			setModel(deps)),

		mustTool("update_config",
			"Generic editor for the long tail of settings: set a value at a JSON pointer within a section and "+
				"hot-reload. `section` ∈ {models, permissions, mcp, a2a, hooks, squads, agents}. `pointer` is an "+
				"RFC-6901 JSON pointer (e.g. \"/permissions/allow/-\" appends, \"/mcp_servers/foo\" sets a key); "+
				"a leading-slash-less or dotted path is also accepted. `value_json` is the new value encoded as "+
				"JSON (e.g. \"\\\"Bash(kubectl get *)\\\"\" or \"{\\\"command\\\":\\\"...\\\"}\"). Editing permissions/"+
				"hooks or any credential field is confirmed with the user. Use set_agent for agent fields and "+
				"set_preference for theme/locale/notifications.",
			updateConfig(deps)),

		mustTool("remove_config",
			"Generic deletion: remove the value at a JSON pointer within a section and hot-reload. Same `section` "+
				"and `pointer` rules as update_config (e.g. remove a permission rule at \"/permissions/allow/2\", "+
				"or an MCP server at \"/mcp_servers/foo\"). Editing permissions/hooks or a credential field is "+
				"confirmed with the user.",
			removeConfig(deps)),

		mustTool("rollback_settings",
			"Undo recent settings changes — the ones made via these tools or the Settings panel — restoring the "+
				"previous on-disk config and hot-reloading. By default undoes the LAST change; pass `steps` to undo "+
				"that many recent changes, or `all: true` to revert EVERY recorded change back to the initial state. "+
				"Use this whenever the user says things like \"revert that\", \"undo your changes\", or \"go back to "+
				"how it was\". Pair with settings_history to show what can be undone. (Reverts config-file edits only, "+
				"not downloaded registry files; a rollback itself cannot be redone.)",
			rollbackSettings(deps)),

		mustTool("settings_history",
			"List recent settings changes that can be undone (newest first), each with a label, timestamp and the "+
				"files it touched. Use it to answer \"what did you change?\" or before calling rollback_settings. "+
				"Optional `limit` caps how many are returned.",
			settingsHistory),
	}
}

// ---- get_settings --------------------------------------------------------

func getSettings(_ tool.Context, in getSettingsIn) (getSettingsOut, error) {
	if strings.TrimSpace(in.Section) == "" {
		out := getSettingsOut{Note: "Pass one of these as `section` to see its current value."}
		for _, s := range sectionCatalogue {
			out.Sections = append(out.Sections, sectionInfo{Name: s.Name, Description: s.Desc})
		}
		return out, nil
	}
	section := normalizeSection(in.Section)
	switch section {
	case "preferences":
		return getSettingsOut{Section: section, Layer: "user", Path: configedit.PreferencesPath(), Data: configedit.ReadPreferences()}, nil

	case "agents":
		cfg, readPath, layer, err := configedit.ReadAgentsConfig()
		if err != nil {
			return getSettingsOut{}, err
		}
		var names []string
		if list, ok := cfg["agents"].([]any); ok {
			for _, v := range list {
				if s, ok := v.(string); ok {
					names = append(names, s)
				}
			}
		}
		var summaries []agentSummary
		for _, name := range names {
			summaries = append(summaries, summarizeAgent(name))
		}
		return getSettingsOut{Section: section, Layer: layer, Path: readPath, Data: summaries}, nil

	case "squads":
		cfg, readPath, layer, err := configedit.ReadAgentsConfig()
		if err != nil {
			return getSettingsOut{}, err
		}
		return getSettingsOut{Section: section, Layer: layer, Path: readPath, Data: cfg["squads"]}, nil

	case "server":
		p := paths.FindConfig("server.yaml")
		out := getSettingsOut{Section: section, Path: p, Note: "server.yaml is read-only via chat; edit it directly to change listen address / token."}
		if p != "" {
			if data, err := os.ReadFile(p); err == nil {
				var parsed any
				if yaml.Unmarshal(data, &parsed) == nil {
					out.Data = redact(normalizeYAML(parsed))
				}
			}
			out.Layer = paths.Layer(p)
		}
		return out, nil

	case "models", "permissions", "mcp", "a2a", "hooks":
		parsed, readPath, layer, _, err := configedit.ReadSection(section)
		if err != nil {
			return getSettingsOut{}, err
		}
		return getSettingsOut{Section: section, Layer: layer, Path: readPath, Data: redact(parsed)}, nil

	default:
		return getSettingsOut{}, fmt.Errorf("unknown section %q; valid sections: agents, squads, models, permissions, mcp, a2a, hooks, preferences, server", in.Section)
	}
}

func summarizeAgent(name string) agentSummary {
	s := agentSummary{Name: name}
	entry, layer, _, err := configedit.ReadAgentEntry(name)
	if err != nil {
		return s
	}
	s.Layer = layer
	s.ModelRef, _ = entry["model_ref"].(string)
	s.Model, _ = entry["model"].(string)
	if b, ok := entry["enabled"].(bool); ok {
		s.Enabled = &b
	}
	s.Leader, _ = entry["leader"].(bool)
	s.Builtin, _ = entry["builtin"].(bool)
	s.Tools = toStringSlice(entry["tools"])
	s.Skills = toStringSlice(entry["skills"])
	return s
}

// ---- set_preference ------------------------------------------------------

type setPreferenceIn struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

func setPreference(_ tool.Context, in setPreferenceIn) (writeResult, error) {
	key := strings.ToLower(strings.TrimSpace(in.Key))
	val := strings.TrimSpace(in.Value)
	// An empty value (or a "default"/"none"/"clear"/"reset" alias) means "revert
	// to the default" — a valid request for every preference.
	reset := val == "" || isDefaultAlias(val)
	prefResult := func(note string) writeResult {
		return writeResult{OK: true, Section: "preferences", WrittenPath: configedit.PreferencesPath(), Layer: "user", Note: note}
	}
	switch key {
	case "theme":
		if reset {
			// "" is the documented default sentinel: the web UI normalises an empty
			// theme to VS Code Light AND reconciles it across open tabs (it only
			// reconciles when prefs.theme is a string), so write "" rather than
			// deleting the key.
			if err := configedit.SetPreference("theme", ""); err != nil {
				return writeResult{}, err
			}
			return prefResult("Theme reset to the default (VS Code Light)."), nil
		}
		if themes := availableThemes(); len(themes) > 0 && !contains(themes, val) {
			return writeResult{}, fmt.Errorf("unknown theme %q; available: %s (or leave empty to reset to the default, VS Code Light)", val, strings.Join(themes, ", "))
		}
		if err := configedit.SetPreference("theme", val); err != nil {
			return writeResult{}, err
		}
	case "locale", "language":
		if reset {
			// No recorded choice ⇒ the web UI falls back to the browser language,
			// then English. Delete the key to represent "unset".
			if err := configedit.SetPreference("locale", nil); err != nil {
				return writeResult{}, err
			}
			return prefResult("Language reset to the default (browser language, then English)."), nil
		}
		if !validLocales[val] {
			return writeResult{}, fmt.Errorf("unknown locale %q; available: en, fr, es, de (or leave empty to reset to the default)", val)
		}
		if err := configedit.SetPreference("locale", val); err != nil {
			return writeResult{}, err
		}
	case "notifications":
		if reset {
			// Unset ⇒ first-run state (the web UI asks again).
			if err := configedit.SetPreference("notifications", nil); err != nil {
				return writeResult{}, err
			}
			return prefResult("Notifications preference reset (the web UI will ask again on next load)."), nil
		}
		b, err := parseBool(val)
		if err != nil {
			return writeResult{}, fmt.Errorf("notifications must be true or false (or empty to reset)")
		}
		if err := configedit.SetPreference("notifications", b); err != nil {
			return writeResult{}, err
		}
	default:
		return writeResult{}, fmt.Errorf("unknown preference %q; valid keys: theme, locale, notifications", in.Key)
	}
	return prefResult("Saved. The web UI applies it on the next page load (no hot-reload needed)."), nil
}

// ---- set_agent -----------------------------------------------------------

type setAgentIn struct {
	Name              string    `json:"name"`
	ModelRef          *string   `json:"model_ref,omitempty"`
	Model             *string   `json:"model,omitempty"`
	Enabled           *bool     `json:"enabled,omitempty"`
	Description       *string   `json:"description,omitempty"`
	Tools             *[]string `json:"tools,omitempty"`
	Skills            *[]string `json:"skills,omitempty"`
	MaxInstances      *int      `json:"max_instances,omitempty"`
	ResumableSessions *bool     `json:"resumable_sessions,omitempty"`
	MaxToolCalls      *int      `json:"max_tool_calls,omitempty"`
	// SubAgents is the agent's own delegable team. Every name must be an enabled
	// agent and the graph must stay acyclic — a dangling or circular edge makes the
	// runtime config fail to resolve, so a bad value here breaks the next reload
	// rather than merely degrading one agent.
	SubAgents *[]string `json:"subagents,omitempty"`
}

func setAgent(deps Deps) functiontool.Func[setAgentIn, writeResult] {
	return func(_ tool.Context, in setAgentIn) (writeResult, error) {
		name := strings.TrimSpace(in.Name)
		if name == "" {
			return writeResult{}, fmt.Errorf("name is required")
		}
		entry, _, _, err := configedit.ReadAgentEntry(name)
		if err != nil {
			return writeResult{}, fmt.Errorf("agent %q has no definition to edit (use the registry tools to install a new agent): %w", name, err)
		}
		var note string
		if in.ModelRef != nil {
			entry["model_ref"] = *in.ModelRef
			if !modelRefExists(*in.ModelRef) {
				note = fmt.Sprintf("warning: model_ref %q is not in the models catalogue (set_model can add it)", *in.ModelRef)
			}
		}
		if in.Model != nil {
			entry["model"] = *in.Model
		}
		if in.Enabled != nil {
			entry["enabled"] = *in.Enabled
		}
		if in.Description != nil {
			entry["description"] = *in.Description
		}
		if in.Tools != nil {
			entry["tools"] = *in.Tools
		}
		if in.Skills != nil {
			entry["skills"] = *in.Skills
		}
		if in.MaxInstances != nil {
			if *in.MaxInstances > 1 {
				entry["max_instances"] = *in.MaxInstances
			} else {
				delete(entry, "max_instances")
			}
		}
		if in.ResumableSessions != nil {
			// Resumable sessions are ON by default; only persist an explicit false.
			if *in.ResumableSessions {
				delete(entry, "resumable_sessions")
			} else {
				entry["resumable_sessions"] = false
			}
		}
		if in.MaxToolCalls != nil {
			if *in.MaxToolCalls > 0 {
				entry["max_tool_calls"] = *in.MaxToolCalls
			} else {
				delete(entry, "max_tool_calls")
			}
		}
		if in.SubAgents != nil {
			// Keep the key absent when empty so agent.json stays clean for the
			// overwhelmingly common no-team case.
			if len(*in.SubAgents) > 0 {
				entry["subagents"] = *in.SubAgents
			} else {
				delete(entry, "subagents")
			}
		}
		path, layer, werr := configedit.WriteAgentEntry(name, entry)
		if werr != nil {
			return writeResult{}, werr
		}
		return writeResult{OK: true, Section: "agents", WrittenPath: path, Layer: layer, Reloaded: deps.reload(), Note: note}, nil
	}
}

// ---- set_model -----------------------------------------------------------

type setModelIn struct {
	ModelRef         string  `json:"model_ref"`
	Model            *string `json:"model,omitempty"`
	ProviderRef      *string `json:"provider_ref,omitempty"`
	ContextLength    *int    `json:"context_length,omitempty"`
	DisableStreaming *bool   `json:"disable_streaming,omitempty"`
	PromptCache      *bool   `json:"prompt_cache,omitempty"`
	Dim              *int    `json:"dim,omitempty"`
	Embedding        *bool   `json:"embedding,omitempty"`
	Provider         *string `json:"provider,omitempty"`
	BaseURL          *string `json:"base_url,omitempty"`
	APIKey           *string `json:"api_key,omitempty"`
}

func setModel(deps Deps) functiontool.Func[setModelIn, writeResult] {
	return func(tc tool.Context, in setModelIn) (writeResult, error) {
		ref := strings.TrimSpace(in.ModelRef)
		if ref == "" {
			return writeResult{}, fmt.Errorf("model_ref is required")
		}
		parsed, _, _, _, err := configedit.ReadSection("models")
		if err != nil {
			return writeResult{}, err
		}
		root, _ := parsed.(map[string]any)
		if root == nil {
			root = map[string]any{}
		}
		models, _ := root["models"].(map[string]any)
		if models == nil {
			models = map[string]any{}
			root["models"] = models
		}
		// Locate the entry case-insensitively; create it when absent.
		key := ref
		var entry map[string]any
		for k, v := range models {
			if strings.EqualFold(k, ref) {
				key = k
				entry, _ = v.(map[string]any)
				break
			}
		}
		created := false
		if entry == nil {
			if in.Model == nil {
				return writeResult{}, fmt.Errorf("model %q does not exist; provide `model` (and usually `provider_ref`) to create it", ref)
			}
			entry = map[string]any{}
			models[key] = entry
			created = true
		}

		before := configedit.EmbedderFingerprint(root)

		if in.Model != nil {
			entry["model"] = *in.Model
		}
		if in.ProviderRef != nil {
			entry["provider_ref"] = *in.ProviderRef
		}
		if in.ContextLength != nil {
			entry["context_length"] = *in.ContextLength
		}
		if in.DisableStreaming != nil {
			entry["disable_streaming"] = *in.DisableStreaming
		}
		if in.PromptCache != nil {
			entry["prompt_cache"] = *in.PromptCache
		}
		if in.Dim != nil {
			entry["dim"] = *in.Dim
		}
		if in.Embedding != nil {
			entry["embedding"] = *in.Embedding
		}
		if in.Provider != nil {
			entry["provider"] = *in.Provider
		}
		if in.BaseURL != nil {
			entry["base_url"] = *in.BaseURL
		}
		if in.APIKey != nil {
			entry["api_key"] = *in.APIKey
		}

		// Connection fields are security-sensitive.
		if in.Provider != nil || in.BaseURL != nil || in.APIKey != nil {
			verb := "Update"
			if created {
				verb = "Create"
			}
			summary := fmt.Sprintf("%s model %q connection (provider/base_url/api_key) in models.json?", verb, ref)
			if ok, reason := confirmSensitive(tc, summary); !ok {
				return writeResult{OK: false, Section: "models", Note: reason}, nil
			}
		}

		path, layer, werr := configedit.WriteSection("models", root)
		if werr != nil {
			return writeResult{}, werr
		}
		after := configedit.EmbedderFingerprint(root)
		res := writeResult{OK: true, Section: "models", WrittenPath: path, Layer: layer, Reloaded: deps.reload()}
		if before != after {
			res.RestartRequired = true
			res.Note = "the active embedding model's identity changed — restart the server to apply (hot-reload does not rebuild the embedder)."
		}
		return res, nil
	}
}

// ---- update_config / remove_config --------------------------------------

type updateConfigIn struct {
	Section   string `json:"section"`
	Pointer   string `json:"pointer"`
	ValueJSON string `json:"value_json"`
}

func updateConfig(deps Deps) functiontool.Func[updateConfigIn, writeResult] {
	return func(tc tool.Context, in updateConfigIn) (writeResult, error) {
		section := normalizeSection(in.Section)
		if strings.TrimSpace(in.ValueJSON) == "" {
			return writeResult{}, fmt.Errorf("value_json is required (the new value, encoded as JSON)")
		}
		var value any
		if err := json.Unmarshal([]byte(in.ValueJSON), &value); err != nil {
			return writeResult{}, fmt.Errorf("value_json is not valid JSON: %w", err)
		}
		sensitive := section == "permissions" || section == "hooks" || pointerTouchesCredential(in.Pointer) || valueTouchesCredential(value)
		return applyConfigEdit(tc, deps, section, in.Pointer, sensitive, "Set", func(root any) (any, error) {
			return configedit.SetByPointer(root, in.Pointer, value)
		})
	}
}

type removeConfigIn struct {
	Section string `json:"section"`
	Pointer string `json:"pointer"`
}

func removeConfig(deps Deps) functiontool.Func[removeConfigIn, writeResult] {
	return func(tc tool.Context, in removeConfigIn) (writeResult, error) {
		section := normalizeSection(in.Section)
		sensitive := section == "permissions" || section == "hooks" || pointerTouchesCredential(in.Pointer)
		return applyConfigEdit(tc, deps, section, in.Pointer, sensitive, "Remove", func(root any) (any, error) {
			return configedit.RemoveByPointer(root, in.Pointer)
		})
	}
}

// applyConfigEdit loads the section's root, optionally confirms, applies edit,
// computes restart_required for models, writes, and reloads.
func applyConfigEdit(tc tool.Context, deps Deps, section, pointer string, sensitive bool, verb string, edit func(root any) (any, error)) (writeResult, error) {
	// Resolve where the section's data lives and how it is written back.
	var (
		root         any
		isAgentsFile bool
	)
	switch section {
	case "models", "permissions", "mcp", "a2a", "hooks":
		parsed, _, _, _, err := configedit.ReadSection(section)
		if err != nil {
			return writeResult{}, err
		}
		if parsed == nil {
			parsed = map[string]any{}
		}
		root = parsed
	case "agents", "squads":
		cfg, _, _, err := configedit.ReadAgentsConfig()
		if err != nil {
			return writeResult{}, err
		}
		root = cfg
		isAgentsFile = true
	case "preferences":
		return writeResult{}, fmt.Errorf("use set_preference to change theme/locale/notifications")
	case "server":
		return writeResult{}, fmt.Errorf("server.yaml is read-only via chat; edit it directly")
	default:
		return writeResult{}, fmt.Errorf("unknown section %q; valid: models, permissions, mcp, a2a, hooks, squads, agents", section)
	}

	before := ""
	if section == "models" {
		before = configedit.EmbedderFingerprint(root)
	}

	if sensitive {
		summary := fmt.Sprintf("%s `%s` in the %s settings?", verb, pointer, section)
		if ok, reason := confirmSensitive(tc, summary); !ok {
			return writeResult{OK: false, Section: section, Note: reason}, nil
		}
	}

	newRoot, err := edit(root)
	if err != nil {
		return writeResult{}, err
	}

	var (
		path, layer string
		werr        error
	)
	if isAgentsFile {
		path, layer, werr = configedit.WriteSection("agent", newRoot)
	} else {
		cs, _ := configEditSection(section)
		path, layer, werr = configedit.WriteSection(cs, newRoot)
	}
	if werr != nil {
		return writeResult{}, werr
	}

	res := writeResult{OK: true, Section: section, WrittenPath: path, Layer: layer, Reloaded: deps.reload()}
	if section == "models" {
		if after := configedit.EmbedderFingerprint(newRoot); after != before {
			res.RestartRequired = true
			res.Note = "the active embedding model's identity changed — restart the server to apply (hot-reload does not rebuild the embedder)."
		}
	}
	return res, nil
}

// ---- small helpers -------------------------------------------------------

func mustTool[A, R any](name, desc string, h functiontool.Func[A, R]) tool.Tool {
	t, err := functiontool.New(functiontool.Config{Name: name, Description: desc}, h)
	if err != nil {
		panic(fmt.Errorf("build settings tool %s: %w", name, err))
	}
	return t
}

func modelRefExists(ref string) bool {
	parsed, _, _, _, err := configedit.ReadSection("models")
	if err != nil {
		return true // can't tell — don't warn
	}
	root, _ := parsed.(map[string]any)
	models, _ := root["models"].(map[string]any)
	for k := range models {
		if strings.EqualFold(k, strings.TrimSpace(ref)) {
			return true
		}
	}
	return false
}

func toStringSlice(v any) []string {
	raw, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, e := range raw {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func contains(list []string, v string) bool {
	for _, e := range list {
		if e == v {
			return true
		}
	}
	return false
}

func parseBool(s string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "1", "yes", "on":
		return true, nil
	case "false", "0", "no", "off":
		return false, nil
	}
	return false, fmt.Errorf("not a boolean")
}

// normalizeYAML converts map[interface{}]interface{} (from older yaml decoders)
// into map[string]any so redact() and JSON marshalling work. yaml.v3 already
// decodes into map[string]any, so this is a defensive pass-through.
func normalizeYAML(v any) any {
	switch n := v.(type) {
	case map[string]any:
		for k, val := range n {
			n[k] = normalizeYAML(val)
		}
		return n
	case map[any]any:
		out := make(map[string]any, len(n))
		for k, val := range n {
			out[fmt.Sprintf("%v", k)] = normalizeYAML(val)
		}
		return out
	case []any:
		for i, val := range n {
			n[i] = normalizeYAML(val)
		}
		return n
	default:
		return v
	}
}

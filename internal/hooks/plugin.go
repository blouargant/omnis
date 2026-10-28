package hooks

// PluginLayer is one Claude Code plugin's hooks declaration: the plugin's name
// and install directory (what ${CLAUDE_PLUGIN_ROOT} stands for) and the path of
// its hooks/hooks.json.
type PluginLayer struct {
	Name string
	Root string
	Path string
}

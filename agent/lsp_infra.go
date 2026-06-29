package agent

import (
	"sync"

	"github.com/blouargant/omnis/internal/lsp"
)

// lspCache memoises the process-wide LSP server pool on Infrastructure. Like the
// MCP pool and the embedder, it is built once and survives hot-reload: language
// servers are expensive to start (they index the whole project) and are shared
// across squads/sessions that touch the same workspace.
type lspCache struct {
	once sync.Once
	mgr  *lsp.Manager
}

// LSP returns the process-wide LSP manager, building it on first use. The
// manager reads lsp_config.json through the config search chain on every
// resolve (via LoadDefault), so a hot-reload of that file is picked up without
// a rebuild. Always non-nil; with no lsp_config.json every resolve yields
// ErrNoServer and the lsp_* tools degrade to "use Grep/Read".
func (i *Infrastructure) LSP() *lsp.Manager {
	if i == nil {
		return nil
	}
	i.lsp.once.Do(func() {
		i.lsp.mgr = lsp.NewManager(func() *lsp.Config {
			c, err := lsp.LoadDefault()
			if err != nil || c == nil {
				return &lsp.Config{}
			}
			return c
		})
	})
	return i.lsp.mgr
}

// shutdownLSP stops every pooled language server. Called from Close.
func (i *Infrastructure) shutdownLSP() {
	if i == nil || i.lsp.mgr == nil {
		return
	}
	i.lsp.mgr.Shutdown()
}

package mcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Server connection states reported by Pool.Status (environment panel's
// Connecteurs card). The pool connects lazily, so "idle" — never connected
// yet — is the normal state of a server no turn has used.
const (
	StateIdle      = "idle"
	StateConnected = "connected"
	StateError     = "error"
)

// ServerStatus is the last known connection state of a pooled server.
type ServerStatus struct {
	State string `json:"state"`
	Err   string `json:"error,omitempty"`
}

// statusTransport records the outcome of each Connect of the wrapped
// transport in the pool's status table. It never connects on its own.
type statusTransport struct {
	inner mcp.Transport
	pool  *Pool
	name  string
}

func (t *statusTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	conn, err := t.inner.Connect(ctx)
	st := ServerStatus{State: StateConnected}
	if err != nil {
		st = ServerStatus{State: StateError, Err: err.Error()}
	}
	t.pool.statusMu.Lock()
	t.pool.status[t.name] = st
	t.pool.statusMu.Unlock()
	return conn, err
}

// trackStatus wraps a server's transport so its connects update Status, and
// registers the server as idle until its first connect.
func (p *Pool) trackStatus(name string, inner mcp.Transport) mcp.Transport {
	p.statusMu.Lock()
	if p.status == nil {
		p.status = map[string]ServerStatus{}
	}
	if _, ok := p.status[name]; !ok {
		p.status[name] = ServerStatus{State: StateIdle}
	}
	p.statusMu.Unlock()
	return &statusTransport{inner: inner, pool: p, name: name}
}

// Status returns a copy of the pool's per-server connection states, keyed by
// server name. Process-wide: a server another session started shows connected.
func (p *Pool) Status() map[string]ServerStatus {
	p.statusMu.Lock()
	defer p.statusMu.Unlock()
	out := make(map[string]ServerStatus, len(p.status))
	for k, v := range p.status {
		out[k] = v
	}
	return out
}

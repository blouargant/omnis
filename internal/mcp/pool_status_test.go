package mcp

import (
	"context"
	"errors"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type stubTransport struct{ err error }

func (s stubTransport) Connect(context.Context) (mcp.Connection, error) { return nil, s.err }

func TestPoolStatusIdleUntilConnect(t *testing.T) {
	p := NewPool(nil)
	if _, err := p.Acquire(Server{Name: "fs", Command: "true"}, nil); err != nil {
		t.Fatal(err)
	}
	st := p.Status()
	if st["fs"].State != StateIdle {
		t.Fatalf("status = %+v, want idle before any connect", st)
	}
}

func TestStatusTransportRecordsOutcome(t *testing.T) {
	p := NewPool(nil)
	ok := p.trackStatus("good", stubTransport{})
	bad := p.trackStatus("bad", stubTransport{err: errors.New("boom")})
	_, _ = ok.Connect(context.Background())
	_, _ = bad.Connect(context.Background())
	st := p.Status()
	if st["good"].State != StateConnected {
		t.Fatalf("good = %+v", st["good"])
	}
	if st["bad"].State != StateError || st["bad"].Err != "boom" {
		t.Fatalf("bad = %+v", st["bad"])
	}
}

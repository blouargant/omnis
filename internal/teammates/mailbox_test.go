package teammates

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"google.golang.org/genai"

	"google.golang.org/adk/tool"
)

func TestJSONLBackendSendAndReceive(t *testing.T) {
	b, err := NewJSONLBackend(filepath.Join(t.TempDir(), "mailboxes"))
	if err != nil {
		t.Fatalf("NewJSONLBackend() error = %v", err)
	}
	t.Cleanup(func() { _ = b.Close() })

	ctx := context.Background()
	if err := b.Send(ctx, "bob", Message{From: "alice", Body: "hello"}); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	msg, err := b.Receive(ctx, "bob", time.Second)
	if err != nil {
		t.Fatalf("Receive() error = %v", err)
	}
	if msg == nil || msg.From != "alice" || msg.Body != "hello" {
		t.Fatalf("Receive() = %+v", msg)
	}

	msg, err = b.Receive(ctx, "bob", 250*time.Millisecond)
	if err != nil {
		t.Fatalf("Receive() second error = %v", err)
	}
	if msg != nil {
		t.Fatalf("Receive() second = %+v, want nil after consuming inbox", msg)
	}
}

func TestSplitLines(t *testing.T) {
	t.Parallel()

	lines := splitLines([]byte("a\n\nb\n"))
	if len(lines) != 2 || string(lines[0]) != "a" || string(lines[1]) != "b" {
		t.Fatalf("splitLines() = %#v", lines)
	}
}

func TestAgentAskTellAndIllegalTransition(t *testing.T) {
	b, err := NewJSONLBackend(filepath.Join(t.TempDir(), "mailboxes"))
	if err != nil {
		t.Fatalf("NewJSONLBackend() error = %v", err)
	}
	t.Cleanup(func() { _ = b.Close() })

	alice := NewAgent("alice", b)
	bob := NewAgent("bob", b)

	go func() {
		msg, recvErr := bob.Check(context.Background(), 2*time.Second)
		if recvErr != nil || msg == nil {
			return
		}
		_ = bob.Tell(context.Background(), msg.From, "reply: "+msg.Body)
	}()

	reply, err := alice.Ask(context.Background(), "bob", "question", 3*time.Second)
	if err != nil {
		t.Fatalf("Ask() error = %v", err)
	}
	if reply != "[bob] reply: question" {
		t.Fatalf("Ask() = %q", reply)
	}
	if state := alice.State(); state != StateIdle {
		t.Fatalf("alice state = %s, want IDLE", state)
	}

	if err := alice.transition(StateWaiting); err == nil {
		t.Fatal("transition(IDLE -> WAITING) error = nil, want illegal transition")
	}
}

type errBackend struct{}

func (errBackend) Send(context.Context, string, Message) error                      { return errors.New("send failed") }
func (errBackend) Receive(context.Context, string, time.Duration) (*Message, error) { return nil, nil }
func (errBackend) Close() error                                                     { return nil }

func TestAgentAskResetsToIdleOnSendError(t *testing.T) {
	t.Parallel()

	a := NewAgent("alice", errBackend{})
	if _, err := a.Ask(context.Background(), "bob", "question", time.Second); err == nil {
		t.Fatal("Ask() error = nil, want send failure")
	}
	if state := a.State(); state != StateIdle {
		t.Fatalf("state = %s, want IDLE after send failure", state)
	}
}

func TestTeammateToolSchemasDoNotRejectMissingArgs(t *testing.T) {
	t.Parallel()

	b, err := NewJSONLBackend(filepath.Join(t.TempDir(), "mailboxes"))
	if err != nil {
		t.Fatalf("NewJSONLBackend() error = %v", err)
	}
	t.Cleanup(func() { _ = b.Close() })

	tools := NewAgent("alice", b).Tools()
	assertOptionalArgs(t, findTool(t, tools, "teammate_ask"), "to", "question")
	assertOptionalArgs(t, findTool(t, tools, "teammate_tell"), "to", "body")
}

func findTool(t *testing.T, tools []tool.Tool, name string) tool.Tool {
	t.Helper()
	for _, current := range tools {
		if current.Name() == name {
			return current
		}
	}
	t.Fatalf("tool %q not found", name)
	return nil
}

func assertOptionalArgs(t *testing.T, tl tool.Tool, names ...string) {
	t.Helper()
	declTool, ok := tl.(interface {
		Declaration() *genai.FunctionDeclaration
	})
	if !ok {
		t.Fatalf("%s has no declaration", tl.Name())
	}
	decl := declTool.Declaration()
	schema, ok := decl.ParametersJsonSchema.(*jsonschema.Schema)
	if !ok {
		t.Fatalf("%s parameters schema = %T, want *jsonschema.Schema", tl.Name(), decl.ParametersJsonSchema)
	}
	if len(schema.Required) != 0 {
		t.Fatalf("%s required schema args = %v, want none", tl.Name(), schema.Required)
	}
	for _, name := range names {
		if _, ok := schema.Properties[name]; !ok {
			t.Fatalf("%s missing property %q in schema", tl.Name(), name)
		}
		if !strings.Contains(decl.Description, "`"+name+"`") {
			t.Fatalf("%s description does not mention %q: %s", tl.Name(), name, decl.Description)
		}
	}
}

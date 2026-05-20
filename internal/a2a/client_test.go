package a2a

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// rpcRequestIn mirrors the JSON-RPC shape on the server side. Kept inside the
// test because the production rpcRequest uses `any` for Params (it gets
// marshalled outbound), which is awkward to decode in reverse.
type rpcRequestIn struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
	ID      string          `json:"id"`
}

func TestSendTask_CompletedRoundTrip(t *testing.T) {
	var (
		gotMethod string
		gotAuth   string
		gotParams sendTaskParams
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("want POST, got %s", r.Method)
		}
		gotAuth = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		var in rpcRequestIn
		if err := json.Unmarshal(raw, &in); err != nil {
			t.Fatalf("decode req: %v", err)
		}
		gotMethod = in.Method
		if err := json.Unmarshal(in.Params, &gotParams); err != nil {
			t.Fatalf("decode params: %v", err)
		}

		result, _ := json.Marshal(wireTask{
			ID:     gotParams.ID,
			Status: wireStatus{State: "completed"},
			Artifacts: []wireArtifact{{Parts: []wirePart{
				{Type: "text", Text: "pong: " + gotParams.Message.Parts[0].Text},
			}}},
		})
		_ = json.NewEncoder(w).Encode(rpcResponse{
			JSONRPC: "2.0",
			ID:      in.ID,
			Result:  result,
		})
	}))
	defer srv.Close()

	agent := Agent{
		Name:    "stub",
		URL:     srv.URL,
		Headers: map[string]string{"Authorization": "Bearer xyz"},
	}

	got, err := SendTask(context.Background(), agent, "ping")
	if err != nil {
		t.Fatalf("SendTask: %v", err)
	}
	if got != "pong: ping" {
		t.Fatalf("response text: got %q, want %q", got, "pong: ping")
	}
	if gotMethod != "tasks/send" {
		t.Fatalf("method: got %q, want tasks/send", gotMethod)
	}
	if gotAuth != "Bearer xyz" {
		t.Fatalf("auth header not forwarded: got %q", gotAuth)
	}
	if gotParams.Message.Role != "user" {
		t.Fatalf("message role: got %q, want user", gotParams.Message.Role)
	}
	if gotParams.ID == "" {
		t.Fatal("task id should not be empty")
	}
}

func TestSendTask_FailedStateBecomesError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		result, _ := json.Marshal(wireTask{
			ID: "t1",
			Status: wireStatus{
				State: "failed",
				Message: &wireMessage{
					Role:  "agent",
					Parts: []wirePart{{Type: "text", Text: "downstream blew up"}},
				},
			},
		})
		_ = json.NewEncoder(w).Encode(rpcResponse{JSONRPC: "2.0", ID: "t1", Result: result})
	}))
	defer srv.Close()

	_, err := SendTask(context.Background(), Agent{Name: "stub", URL: srv.URL}, "ping")
	if err == nil {
		t.Fatal("expected error for failed task, got nil")
	}
	if !strings.Contains(err.Error(), "downstream blew up") {
		t.Fatalf("error should surface remote message; got %v", err)
	}
}

func TestSendTask_CanceledStateBecomesError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		result, _ := json.Marshal(wireTask{
			ID:     "t1",
			Status: wireStatus{State: "canceled"},
		})
		_ = json.NewEncoder(w).Encode(rpcResponse{JSONRPC: "2.0", ID: "t1", Result: result})
	}))
	defer srv.Close()

	_, err := SendTask(context.Background(), Agent{Name: "stub", URL: srv.URL}, "ping")
	if err == nil {
		t.Fatal("expected error for canceled task, got nil")
	}
	if !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("error should mention canceled state; got %v", err)
	}
}

func TestSendTask_RPCErrorPropagates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(rpcResponse{
			JSONRPC: "2.0",
			ID:      "t1",
			Error:   &rpcErrorBody{Code: -32601, Message: "method not found"},
		})
	}))
	defer srv.Close()

	_, err := SendTask(context.Background(), Agent{Name: "stub", URL: srv.URL}, "ping")
	if err == nil {
		t.Fatal("expected error for JSON-RPC error response")
	}
	if !strings.Contains(err.Error(), "method not found") || !strings.Contains(err.Error(), "-32601") {
		t.Fatalf("error should include both code and message; got %v", err)
	}
}

func TestSendTask_HTTPErrorPropagates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "go away", http.StatusUnauthorized)
	}))
	defer srv.Close()

	_, err := SendTask(context.Background(), Agent{Name: "stub", URL: srv.URL}, "ping")
	if err == nil {
		t.Fatal("expected error for HTTP 401")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Fatalf("error should mention status code; got %v", err)
	}
}

func TestSendTask_ConcatenatesMultipleArtifactParts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		result, _ := json.Marshal(wireTask{
			ID:     "t1",
			Status: wireStatus{State: "completed"},
			Artifacts: []wireArtifact{
				{Parts: []wirePart{{Type: "text", Text: "alpha"}, {Type: "text", Text: "beta"}}},
				{Parts: []wirePart{{Type: "text", Text: "gamma"}, {Type: "data", Text: "IGNORED"}}},
			},
		})
		_ = json.NewEncoder(w).Encode(rpcResponse{JSONRPC: "2.0", ID: "t1", Result: result})
	}))
	defer srv.Close()

	got, err := SendTask(context.Background(), Agent{Name: "stub", URL: srv.URL}, "ping")
	if err != nil {
		t.Fatalf("SendTask: %v", err)
	}
	if got != "alphabetagamma" {
		t.Fatalf("got %q, want %q", got, "alphabetagamma")
	}
}

package a2a

// A2A protocol client — sends synchronous `tasks/send` JSON-RPC calls to a
// remote A2A endpoint described by an Agent entry from a2a_config.json.
//
// Wire format mirrors server/a2a_server.go on the receiving side. Only the
// subset of fields the client reads is declared here.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type wirePart struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type wireMessage struct {
	Role  string     `json:"role"`
	Parts []wirePart `json:"parts"`
}

type wireArtifact struct {
	Parts []wirePart `json:"parts"`
}

type wireStatus struct {
	State   string       `json:"state"`
	Message *wireMessage `json:"message,omitempty"`
}

type wireTask struct {
	ID        string         `json:"id"`
	Status    wireStatus     `json:"status"`
	Artifacts []wireArtifact `json:"artifacts,omitempty"`
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
	ID      string `json:"id"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcErrorBody   `json:"error,omitempty"`
	ID      string          `json:"id"`
}

type rpcErrorBody struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type sendTaskParams struct {
	ID      string      `json:"id"`
	Message wireMessage `json:"message"`
}

// SendTask sends a synchronous tasks/send call to the remote A2A endpoint and
// returns the concatenated text of every text part across all returned
// artifacts. A non-completed (failed / canceled) terminal state is surfaced
// as an error.
func SendTask(ctx context.Context, agent Agent, prompt string) (string, error) {
	endpoint := strings.TrimRight(agent.URL, "/") + "/"
	taskID := fmt.Sprintf("task-%d", time.Now().UnixNano())

	body, err := json.Marshal(rpcRequest{
		JSONRPC: "2.0",
		Method:  "tasks/send",
		ID:      taskID,
		Params: sendTaskParams{
			ID: taskID,
			Message: wireMessage{
				Role:  "user",
				Parts: []wirePart{{Type: "text", Text: prompt}},
			},
		},
	})
	if err != nil {
		return "", fmt.Errorf("a2a %s: marshal request: %w", agent.Name, err)
	}

	cctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("a2a %s: build request: %w", agent.Name, err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range agent.Headers {
		req.Header.Set(k, v)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("a2a %s: HTTP %s: %w", agent.Name, endpoint, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("a2a %s: HTTP %d: %s", agent.Name, resp.StatusCode, strings.TrimSpace(string(b)))
	}

	var rpc rpcResponse
	if err := json.NewDecoder(resp.Body).Decode(&rpc); err != nil {
		return "", fmt.Errorf("a2a %s: decode response: %w", agent.Name, err)
	}
	if rpc.Error != nil {
		return "", fmt.Errorf("a2a %s: rpc error %d: %s", agent.Name, rpc.Error.Code, rpc.Error.Message)
	}

	var task wireTask
	if err := json.Unmarshal(rpc.Result, &task); err != nil {
		return "", fmt.Errorf("a2a %s: decode task result: %w", agent.Name, err)
	}
	if task.Status.State == "failed" || task.Status.State == "canceled" {
		errMsg := task.Status.State
		if task.Status.Message != nil {
			var sb strings.Builder
			for _, p := range task.Status.Message.Parts {
				if p.Type == "text" {
					sb.WriteString(p.Text)
				}
			}
			if sb.Len() > 0 {
				errMsg = sb.String()
			}
		}
		return "", fmt.Errorf("a2a %s: task %s: %s", agent.Name, task.Status.State, errMsg)
	}

	var out strings.Builder
	for _, art := range task.Artifacts {
		for _, p := range art.Parts {
			if p.Type == "text" {
				out.WriteString(p.Text)
			}
		}
	}
	return out.String(), nil
}

// feedback.go — explicit user feedback persistence for the wrap-session
// soft-skill (Phase 5 of the ACE evolution).
//
// The wrap-session skill instructs the leader to ask one closing
// question on interactive surfaces (TUI / Web UI). The answer is
// persisted to `$YOKE_HOME/logs/agent_feedback_<sessionSuffix>.json`
// via the `record_session_feedback` tool exposed here, and the
// downstream reflectors (heuristic + LLM) consult it before scanning
// the implicit user-message tone.

package softskills

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
)

// Feedback is the on-disk record produced by `record_session_feedback`.
// One record per session — repeat calls overwrite earlier answers (the
// leader is instructed to ask the wrap question at most once anyway).
type Feedback struct {
	Question  string    `json:"question"`
	Answer    string    `json:"answer"`
	Timestamp time.Time `json:"timestamp"`
}

// feedbackMu serialises in-process writes; the on-disk JSON is small
// enough that a host-level flock is overkill (unlike _stats.json which
// has a lock-fan-out path).
var feedbackMu sync.Mutex

// FeedbackPath returns the absolute path of the feedback sidecar for the
// session identified by suffix (the same value Infrastructure.SessionSuffix
// returns). logsDir is typically paths.LogsDir().
func FeedbackPath(logsDir, suffix string) string {
	return filepath.Join(logsDir, fmt.Sprintf("agent_feedback_%s.json", suffix))
}

// RecordFeedback writes the answer atomically (temp-file + rename). A
// blank answer is rejected so the leader can't accidentally clobber a
// previously-captured response. logsDir/suffix locate the sidecar.
func RecordFeedback(logsDir, suffix, question, answer string) error {
	if strings.TrimSpace(suffix) == "" {
		return errors.New("feedback: suffix is required")
	}
	if strings.TrimSpace(answer) == "" {
		return errors.New("feedback: answer must not be blank")
	}
	rec := Feedback{
		Question:  strings.TrimSpace(question),
		Answer:    strings.TrimSpace(answer),
		Timestamp: time.Now().UTC(),
	}

	feedbackMu.Lock()
	defer feedbackMu.Unlock()

	if err := os.MkdirAll(logsDir, 0o755); err != nil {
		return fmt.Errorf("feedback: mkdir %s: %w", logsDir, err)
	}
	path := FeedbackPath(logsDir, suffix)
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return fmt.Errorf("feedback: marshal: %w", err)
	}
	tmp, err := os.CreateTemp(logsDir, "agent_feedback.*.json.tmp")
	if err != nil {
		return fmt.Errorf("feedback: temp file: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("feedback: write: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("feedback: close: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		cleanup()
		return fmt.Errorf("feedback: rename: %w", err)
	}
	return nil
}

// LoadFeedback reads the per-session feedback file. A missing file is
// not an error and returns (nil, nil).
func LoadFeedback(logsDir, suffix string) (*Feedback, error) {
	path := FeedbackPath(logsDir, suffix)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("feedback: read %s: %w", path, err)
	}
	if len(data) == 0 {
		return nil, nil
	}
	var rec Feedback
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, fmt.Errorf("feedback: parse %s: %w", path, err)
	}
	return &rec, nil
}

// feedbackToolIn / Out are the parameter / result shapes for the
// `record_session_feedback` tool exposed to the leader.
type feedbackToolIn struct {
	Question string `json:"question"`
	Answer   string `json:"answer"`
}

type feedbackToolOut struct {
	Result string `json:"result"`
}

// NewFeedbackTool returns the `record_session_feedback` tool. suffixFor
// resolves the per-session filename suffix from the tool.Context
// (UserID + SessionID); logsDir is the directory where the sidecar
// lives ($YOKE_HOME/logs by default).
//
// Returning a single tool (rather than a toolset) keeps the wiring
// trivial: callers append it to the leader's tool list at squad-build
// time.
func NewFeedbackTool(logsDir string, suffixFor func(userID, sessionID string) string) tool.Tool {
	if suffixFor == nil {
		// Fall back to "userID_sessionID" so unit tests can mount the
		// tool without an Infrastructure.
		suffixFor = func(u, s string) string {
			if u == "" && s == "" {
				return ""
			}
			if u == "" {
				return s
			}
			if s == "" {
				return u
			}
			return u + "_" + s
		}
	}
	h := func(tctx tool.Context, in feedbackToolIn) (feedbackToolOut, error) {
		suffix := suffixFor(tctx.UserID(), tctx.SessionID())
		if err := RecordFeedback(logsDir, suffix, in.Question, in.Answer); err != nil {
			return feedbackToolOut{Result: "Error: " + err.Error()}, nil
		}
		return feedbackToolOut{Result: fmt.Sprintf("feedback recorded for session %q", suffix)}, nil
	}
	t, err := functiontool.New(functiontool.Config{
		Name: "record_session_feedback",
		Description: "Persist the user's wrap-up answer for this session. Call this exactly once per session, right after the wrap-up question is answered.\n" +
			"Arguments:\n" +
			"  `question` (string, required) — the wrap-up question you asked verbatim.\n" +
			"  `answer`   (string, required) — the user's answer (≥1 non-whitespace char). Blank answers are rejected so a prior record is never clobbered.\n" +
			"Returns: a short status string. The file lands at logs/agent_feedback_<session-suffix>.json and is consumed by the post-session reflector.",
	}, h)
	if err != nil {
		panic(fmt.Errorf("softskills: build feedback tool: %w", err))
	}
	return t
}

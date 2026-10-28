package delegation

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/agent"
)

// providerHeaderTimeoutErr mimics a provider transport timeout: the HTTP client
// deadline expires while the child context is still live, so the error wraps
// context.DeadlineExceeded even though nothing cancelled the child.
func providerHeaderTimeoutErr() error {
	return fmt.Errorf("chat completion: Post %q: net/http: timeout awaiting response headers: %w",
		"https://api.example/v1/messages", context.DeadlineExceeded)
}

func usefulActivityState(stop agent.StopReason) agent.RunState {
	return agent.RunState{
		StopReason:   stop,
		TurnCount:    2,
		TokenCount:   40,
		Conversation: []agent.Message{{Role: agent.MessageRoleAssistant, ToolCalls: []agent.ToolCall{{ID: "c0", Name: "read"}}, Content: "partial findings"}},
	}
}

func TestFailedDelegateExecutionContextStatusMapping(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		err       error
		childErr  error
		state     agent.RunState
		status    Status
		reason    string
		resumable bool
	}{
		{name: "cancelled without activity", err: context.Canceled, childErr: context.Canceled, status: StatusCancelled, resumable: true},
		{name: "cancelled with output", err: context.Canceled, childErr: context.Canceled, state: agent.RunState{Conversation: []agent.Message{{Role: agent.MessageRoleAssistant, Content: "work"}}}, status: StatusPartial, reason: "cancelled", resumable: true},
		{name: "deadline without activity", err: context.DeadlineExceeded, childErr: context.DeadlineExceeded, status: StatusCancelled, reason: "limit reached", resumable: true},
		{name: "deadline with tool activity", err: context.DeadlineExceeded, childErr: context.DeadlineExceeded, state: agent.RunState{Conversation: []agent.Message{{Role: agent.MessageRoleAssistant, ToolCalls: []agent.ToolCall{{Name: "read"}}}}}, status: StatusPartial, reason: "limit reached", resumable: true},
		{name: "provider header timeout without activity", err: providerHeaderTimeoutErr(), status: StatusFailed},
		{name: "provider header timeout with tool activity", err: providerHeaderTimeoutErr(), state: usefulActivityState(agent.StopReasonError), status: StatusFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := failedDelegateExecution(Spec{AgentID: "agent-test"}, tt.state, TokenUsage{}, tt.err, tt.childErr, newTraceCollector("agent-test", "task"), nil).Value.(Result)
			if result.Status != tt.status || result.StopReason != tt.reason || result.SessionResumable != tt.resumable {
				t.Fatalf("result = %#v, want status=%q reason=%q resumable=%v", result, tt.status, tt.reason, tt.resumable)
			}
			envelope := result.ProjectToolResult()
			switch tt.status {
			case StatusFailed:
				if envelope.Status != "failed" || !strings.Contains(envelope.Reason, "delegation failed: ") {
					t.Fatalf("envelope = %#v, want failed with failure explanation", envelope)
				}
				if strings.Contains(envelope.Reason, "session is preserved") {
					t.Errorf("failed result claims resumability: %q", envelope.Reason)
				}
			default:
				// A cancelled result carries its own explanation; a partial result
				// keeps the short projection token.
				wantReason := tt.reason
				if tt.status == StatusCancelled {
					if result.Reason == "" {
						t.Fatalf("cancelled result has no reason: %#v", result)
					}
					wantReason = result.Reason
				}
				if envelope.Status != string(tt.status) || envelope.Reason != wantReason {
					t.Fatalf("envelope = %#v, want status=%q reason=%q", envelope, tt.status, wantReason)
				}
			}
		})
	}
}

func readTraceRecord(t *testing.T, path string) traceRecord {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", path, err)
	}
	var record traceRecord
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatalf("Unmarshal trace: %v (data: %s)", err, string(data))
	}
	return record
}

func traceEntry(t *testing.T, record traceRecord, phase string) map[string]any {
	t.Helper()
	for _, entry := range record.Entries {
		if entry.Phase == phase {
			return entry.Fields
		}
	}
	t.Fatalf("no trace entry with phase %q in %#v", phase, record.Entries)
	return nil
}

func TestSpawnDelegate_ProviderTimeoutStaysFailed(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "delegation.log")
	logger, err := NewTraceLogger(path)
	if err != nil {
		t.Fatalf("NewTraceLogger: %v", err)
	}
	state := usefulActivityState(agent.StopReasonError)
	runner := &mockRunner{runFunc: func(ctx context.Context, _ agent.RunRequest) (agent.RunState, error) {
		if ctx.Err() != nil {
			t.Fatalf("child context already ended before returning: %v", ctx.Err())
		}
		return state, providerHeaderTimeoutErr()
	}}
	res, _, _, err := SpawnDelegate(context.Background(), Spec{AgentID: "timeout-child"}, agent.RunRequest{}, runner, nil, logger)
	if err != nil {
		t.Fatalf("SpawnDelegate error: %v", err)
	}
	if err := logger.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	result := res.Value.(Result)
	if result.Status != StatusFailed || result.SessionResumable {
		t.Fatalf("result = %#v, want failed and not resumable", result)
	}
	if !strings.Contains(result.Reason, "timeout awaiting response headers") {
		t.Errorf("reason = %q, want the provider error", result.Reason)
	}
	if strings.Contains(result.Reason, "session is preserved") {
		t.Errorf("reason claims resumability: %q", result.Reason)
	}
	if envelope := result.ProjectToolResult(); envelope.Status != "failed" {
		t.Errorf("envelope = %#v, want failed", envelope)
	}

	record := readTraceRecord(t, path)
	complete := traceEntry(t, record, "child_run_complete")
	failed := traceEntry(t, record, "failed")
	if complete["is_context_cancelled"] != false || complete["is_context_deadline"] != false {
		t.Errorf("child_run_complete context flags = %#v, want both false", complete)
	}
	if _, ok := complete["ctx_err"]; ok {
		t.Errorf("child_run_complete recorded ctx_err %#v on a live context", complete["ctx_err"])
	}
	if failed["context_cancelled"] != false || failed["context_deadline"] != false {
		t.Errorf("failed context flags = %#v, want both false", failed)
	}
	if failed["status"] != string(StatusFailed) {
		t.Errorf("failed status = %#v, want %q", failed["status"], StatusFailed)
	}
}

func TestSpawnDelegate_ChildCancellationStaysResumablePartial(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "delegation.log")
	logger, err := NewTraceLogger(path)
	if err != nil {
		t.Fatalf("NewTraceLogger: %v", err)
	}
	parentCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	state := usefulActivityState(agent.StopReasonCancelled)
	runner := &mockRunner{runFunc: func(context.Context, agent.RunRequest) (agent.RunState, error) {
		cancel()
		return state, context.Canceled
	}}
	res, _, _, err := SpawnDelegate(parentCtx, Spec{AgentID: "cancelled-child"}, agent.RunRequest{}, runner, nil, logger)
	if err != nil {
		t.Fatalf("SpawnDelegate error: %v", err)
	}
	if err := logger.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	result := res.Value.(Result)
	if result.Status != StatusPartial || result.StopReason != "cancelled" || !result.SessionResumable {
		t.Fatalf("result = %#v, want partial/cancelled and resumable", result)
	}
	if !strings.Contains(result.Reason, "session is preserved") {
		t.Errorf("reason = %q, want preserved-session notice", result.Reason)
	}
	envelope := result.ProjectToolResult()
	if envelope.Status != "partial" || envelope.Reason != "cancelled" {
		t.Errorf("envelope = %#v, want partial/cancelled", envelope)
	}

	record := readTraceRecord(t, path)
	complete := traceEntry(t, record, "child_run_complete")
	failed := traceEntry(t, record, "failed")
	if complete["is_context_cancelled"] != true || complete["ctx_err"] != context.Canceled.Error() {
		t.Errorf("child_run_complete fields = %#v, want cancelled child context", complete)
	}
	if failed["context_cancelled"] != true {
		t.Errorf("failed context_cancelled = %#v, want true", failed["context_cancelled"])
	}
}

func TestSpawnDelegate_TimeoutSnapshotsDeadlineBeforeCleanup(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "delegation.log")
	logger, err := NewTraceLogger(path)
	if err != nil {
		t.Fatalf("NewTraceLogger: %v", err)
	}
	state := usefulActivityState(agent.StopReasonCancelled)
	deadlineErr := context.DeadlineExceeded
	runner := &mockRunner{runFunc: func(ctx context.Context, _ agent.RunRequest) (agent.RunState, error) {
		<-ctx.Done()
		deadlineErr = ctx.Err()
		return state, ctx.Err()
	}}
	spec := Spec{AgentID: "deadline-child", Limits: Limits{Timeout: 10 * time.Millisecond}}
	res, _, _, err := SpawnDelegate(context.Background(), spec, agent.RunRequest{}, runner, nil, logger)
	if err != nil {
		t.Fatalf("SpawnDelegate error: %v", err)
	}
	if err := logger.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if deadlineErr != context.DeadlineExceeded {
		t.Fatalf("runner saw child context err = %v, want deadline exceeded", deadlineErr)
	}
	result := res.Value.(Result)
	if result.Status != StatusPartial || result.StopReason != "limit reached" || !result.SessionResumable {
		t.Fatalf("result = %#v, want partial/limit reached and resumable", result)
	}
	if !strings.Contains(result.Reason, "session is preserved") {
		t.Errorf("reason = %q, want preserved-session notice", result.Reason)
	}
	envelope := result.ProjectToolResult()
	if envelope.Status != "partial" || envelope.Reason != "limit reached" {
		t.Errorf("envelope = %#v, want partial/limit reached", envelope)
	}

	record := readTraceRecord(t, path)
	complete := traceEntry(t, record, "child_run_complete")
	failed := traceEntry(t, record, "failed")
	if complete["is_context_deadline"] != true || complete["is_context_cancelled"] != false {
		t.Errorf("child_run_complete fields = %#v, want deadline child context", complete)
	}
	if complete["ctx_err"] != context.DeadlineExceeded.Error() {
		t.Errorf("child_run_complete ctx_err = %#v, want %q", complete["ctx_err"], context.DeadlineExceeded.Error())
	}
	if failed["context_deadline"] != true || failed["status"] != string(StatusPartial) {
		t.Errorf("failed fields = %#v, want deadline partial outcome", failed)
	}
}

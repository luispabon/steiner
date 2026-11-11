package agent

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

// A TUI terminal reset preserves every approval the coordinator still reports,
// so the producer ordering must guarantee that a cancelled run approval is
// unregistered before the run's stop reason is emitted. The responder
// unregisters in a defer that runs before its decision event, and the runner
// emits the stop only after every tool goroutine of the turn has returned.
func TestRunnerStopReasonFollowsCancelledApprovalUnregistration(t *testing.T) {
	var mu sync.Mutex
	registered := false
	registeredAtStop := true
	var types []string
	requested := make(chan struct{})
	var requestedOnce sync.Once

	sink := output.SinkFunc(func(event output.Event) {
		mu.Lock()
		defer mu.Unlock()
		types = append(types, event.Type)
		if event.Type == output.EventTypeStopReason {
			registeredAtStop = registered
		}
	})
	// The responder stands in for approvalResponder: it registers on entry and
	// unregisters before returning, which is what the coordinator sees.
	approver := NewEventingApprover(sink, tool.ApprovalResponderFunc(func(ctx context.Context, _ tool.ApprovalRequest) error {
		mu.Lock()
		registered = true
		mu.Unlock()
		requestedOnce.Do(func() { close(requested) })
		defer func() {
			mu.Lock()
			registered = false
			mu.Unlock()
		}()
		<-ctx.Done()
		return ctx.Err()
	}))

	executor := &fakeExecutor{
		execute: func(ctx context.Context, toolName string, _ map[string]any) (any, error) {
			if err := approver.RequestApproval(ctx, tool.ApprovalRequest{
				Tool:     tool.ToolDef{Name: toolName},
				CallID:   "call_1",
				Reason:   "sandbox_violation",
				Response: make(chan tool.ApprovalResponse, 1),
				Kind:     tool.ApprovalKindPath,
			}); err != nil {
				return nil, err
			}
			return tool.ExecutionResult{Value: map[string]any{"ok": true}}, nil
		},
	}
	providerStub := &fakeProvider{
		responses: []provider.ChatResponse{
			{
				Message: provider.Message{
					Role: provider.MessageRoleAssistant,
					ToolCalls: []provider.ToolCall{
						{ID: "call_1", Name: "write", Arguments: map[string]any{"path": "note.txt"}},
					},
				},
				FinishReason: "tool_calls",
			},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type runResult struct {
		state RunState
		err   error
	}
	done := make(chan runResult, 1)
	go func() {
		state, err := NewRunner().Run(ctx, RunRequest{
			Provider:      providerStub,
			Executor:      executor,
			Tools:         []provider.ToolSpec{{Type: "function", Function: provider.ToolFunctionSpec{Name: "write", Description: "Write files", Parameters: map[string]any{"type": "object"}}}},
			Prompt:        prompt.AssemblyOptions{Conversation: []provider.Message{{Role: provider.MessageRoleUser, Content: "write the note"}}, ProjectContextBudgetBytes: 128},
			ResolvedModel: provider.ResolvedModel{BackendModelID: "test-model"},
			MaxTokens:     intPtr(64),
			Limits:        Limits{MaxTurns: 4, MaxTokens: 50},
			Events:        sink,
		})
		done <- runResult{state: state, err: err}
	}()

	select {
	case <-requested:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the approval request")
	}
	cancel()

	var result runResult
	select {
	case result = <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("run did not stop after the approval was cancelled")
	}
	if result.err != nil {
		t.Fatalf("Run() error = %v, want nil for a cancelled run", result.err)
	}
	if got, want := result.state.StopReason, StopReasonCancelled; got != want {
		t.Fatalf("StopReason = %q, want %q", got, want)
	}

	mu.Lock()
	defer mu.Unlock()
	if registeredAtStop {
		t.Fatal("stop reason was emitted while the cancelled approval was still registered")
	}
	if registered {
		t.Fatal("approval responder still registered after the cancelled run returned")
	}
	// The requested and denied events are not necessarily adjacent to the stop
	// reason: the turn records the cancelled tool result in between.
	wantOrder := []string{output.EventTypeApprovalRequested, output.EventTypeApprovalDenied, output.EventTypeStopReason}
	next := 0
	for _, typ := range types {
		if next < len(wantOrder) && typ == wantOrder[next] {
			next++
		}
	}
	if next != len(wantOrder) {
		t.Fatalf("event order = %v, want approval_requested before approval_denied before stop_reason", types)
	}
}

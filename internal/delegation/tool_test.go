package delegation

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

type mockRunner struct {
	runFunc func(context.Context, agent.RunRequest) (agent.RunState, error)
}

func (m *mockRunner) Run(ctx context.Context, req agent.RunRequest) (agent.RunState, error) {
	return m.runFunc(ctx, req)
}

type noopEventSink struct{}

func (noopEventSink) Emit(output.Event) {}

type stubProvider struct{}

func (stubProvider) ChatCompletion(context.Context, provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, nil
}

func (stubProvider) StreamChatCompletion(context.Context, provider.ChatRequest) (<-chan provider.ChatChunk, error) {
	return nil, nil
}

func (stubProvider) SupportsUsageStats() bool { return false }

func successRunState() agent.RunState {
	return agent.RunState{
		Conversation: []agent.Message{
			{Role: agent.MessageRoleAssistant, Content: "task result"},
		},
		TurnCount:  1,
		TokenCount: 100,
		StopReason: agent.StopReasonComplete,
	}
}

func TestToolDef(t *testing.T) {
	called := false
	handler := func(_ context.Context, _ map[string]any) (any, error) {
		called = true
		return nil, nil
	}
	def := DelegateToolDef(handler)

	if def.Name != "delegate" {
		t.Errorf("Name=%q, want %q", def.Name, "delegate")
	}
	if def.Description != "Spawn an isolated sub-agent to complete a task. Returns structured result. The sub-agent cannot itself delegate further." {
		t.Errorf("Description mismatch")
	}
	if def.Handler == nil {
		t.Fatal("Handler is nil")
	}
	if def.Approval != config.ApprovalModeAuto {
		t.Errorf("Approval=%v, want %v", def.Approval, config.ApprovalModeAuto)
	}

	schema, ok := def.ParameterSchema["type"].(string)
	if !ok || schema != "object" {
		t.Errorf("schema type=%v, want 'object'", schema)
	}
	props, ok := def.ParameterSchema["properties"].(map[string]any)
	if !ok {
		t.Fatal("properties missing from schema")
	}
	for _, key := range []string{"task", "context", "system_prompt", "max_turns", "timeout"} {
		if _, exists := props[key]; !exists {
			t.Errorf("property %q missing from properties", key)
		}
	}
	required, ok := def.ParameterSchema["required"].([]any)
	if !ok || len(required) != 1 || required[0] != "task" {
		t.Errorf("required=%v, want [\"task\"]", required)
	}

	if _, err := handler(context.Background(), nil); err != nil {
		t.Fatalf("handler() error = %v", err)
	}
	if !called {
		t.Error("handler was not called")
	}
}

func TestToolHandler_EmptyTask(t *testing.T) {
	deps := DelegateHandlerDeps{
		SubAgentCfg: config.SubAgentConfig{},
		Provider:    stubProvider{},
		ParentReg:   tool.NewRegistry(),
		Runner: &mockRunner{runFunc: func(_ context.Context, _ agent.RunRequest) (agent.RunState, error) {
			return agent.RunState{}, nil
		}},
		Events:  noopEventSink{},
		WorkDir: "/tmp/work",
	}
	handler := NewDelegateHandler(deps)

	_, err := handler(context.Background(), map[string]any{})
	if err == nil {
		t.Error("expected error for missing task")
	}

	_, err = handler(context.Background(), map[string]any{"task": ""})
	if err == nil {
		t.Error("expected error for empty task string")
	}
}

func TestToolHandler_ReturnsStructuredFailureResult(t *testing.T) {
	deps := DelegateHandlerDeps{
		SubAgentCfg: config.SubAgentConfig{},
		Provider:    stubProvider{},
		ParentReg:   tool.NewRegistry(),
		Runner: &mockRunner{runFunc: func(_ context.Context, _ agent.RunRequest) (agent.RunState, error) {
			return agent.RunState{}, fmt.Errorf("child run exploded")
		}},
		Events:  noopEventSink{},
		WorkDir: "/tmp/work",
	}
	handler := NewDelegateHandler(deps)

	raw, err := handler(context.Background(), map[string]any{"task": "do something"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	execResult, ok := raw.(tool.ExecutionResult)
	if !ok {
		t.Fatalf("handler returned %T, want tool.ExecutionResult", raw)
	}
	result, ok := execResult.Value.(DelegationResult)
	if !ok {
		t.Fatalf("result.Value type = %T, want DelegationResult", execResult.Value)
	}
	if result.Status != StatusFailed {
		t.Fatalf("Status = %q, want %q", result.Status, StatusFailed)
	}
	if result.Error != "child run exploded" {
		t.Fatalf("Error = %q, want %q", result.Error, "child run exploded")
	}
	if result.Summary != "delegation failed: child run exploded" {
		t.Fatalf("Summary = %q, want failure summary", result.Summary)
	}
	if execResult.Retention == nil {
		t.Fatal("Retention = nil, want failure retention")
	}
	if execResult.Retention.Kind != tool.RetentionKindDelegateSummary {
		t.Fatalf("Retention.Kind = %q, want %q", execResult.Retention.Kind, tool.RetentionKindDelegateSummary)
	}
	if execResult.Retention.Status != string(StatusFailed) {
		t.Fatalf("Retention.Status = %q, want %q", execResult.Retention.Status, StatusFailed)
	}
}

func TestToolHandler_ParsesMaxTurns(t *testing.T) {
	var got []agent.Limits
	deps := DelegateHandlerDeps{
		SubAgentCfg: config.SubAgentConfig{MaxTurns: 15},
		Provider:    stubProvider{},
		ParentReg:   tool.NewRegistry(),
		Runner: &mockRunner{runFunc: func(_ context.Context, req agent.RunRequest) (agent.RunState, error) {
			got = append(got, req.Limits)
			return successRunState(), nil
		}},
		Events:  noopEventSink{},
		WorkDir: "/tmp/work",
	}
	handler := NewDelegateHandler(deps)

	_, err := handler(context.Background(), map[string]any{
		"task":      "do something",
		"max_turns": float64(5),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("runner was not called")
	}
	if got[0].MaxTurns != 5 {
		t.Errorf("MaxTurns=%d, want 5", got[0].MaxTurns)
	}
	if got[0].MaxTokens != 100000 {
		t.Errorf("MaxTokens=%d, want 100000 (default)", got[0].MaxTokens)
	}
}

func TestToolHandler_ParsesTimeout(t *testing.T) {
	tests := []struct {
		name       string
		timeoutVal any
		wantMin    time.Duration
	}{
		{name: "short 15s floored", timeoutVal: "15s", wantMin: time.Minute},
		{name: "short 30s floored", timeoutVal: "30s", wantMin: time.Minute},
		{name: "valid 5m", timeoutVal: "5m", wantMin: 5 * time.Minute},
		{name: "empty ignored", timeoutVal: "", wantMin: 0},
		{name: "missing", timeoutVal: nil, wantMin: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotDeadline time.Time
			var callCount int
			deps := DelegateHandlerDeps{
				SubAgentCfg: config.SubAgentConfig{},
				Provider:    stubProvider{},
				ParentReg:   tool.NewRegistry(),
				Runner: &mockRunner{runFunc: func(ctx context.Context, _ agent.RunRequest) (agent.RunState, error) {
					callCount++
					if callCount == 1 {
						deadline, ok := ctx.Deadline()
						if ok {
							gotDeadline = deadline
						}
					}
					return successRunState(), nil
				}},
				Events:  noopEventSink{},
				WorkDir: "/tmp/work",
			}
			handler := NewDelegateHandler(deps)

			start := time.Now()
			input := map[string]any{"task": "do something"}
			if tt.timeoutVal != nil {
				input["timeout"] = tt.timeoutVal
			}

			_, err := handler(context.Background(), input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.wantMin == 0 {
				if !gotDeadline.IsZero() {
					t.Errorf("context has deadline=%v, want none", gotDeadline)
				}
				return
			}
			if gotDeadline.IsZero() {
				t.Fatal("context deadline missing")
			}
			if gotDeadline.Sub(start) < tt.wantMin {
				t.Errorf("deadline after start = %v, want at least %v", gotDeadline.Sub(start), tt.wantMin)
			}
		})
	}
}

func TestToolHandler_RejectsInvalidTimeout(t *testing.T) {
	tests := []struct {
		name       string
		timeoutVal string
	}{
		{name: "not-a-duration", timeoutVal: "not-a-duration"},
		{name: "99x", timeoutVal: "99x"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deps := DelegateHandlerDeps{
				SubAgentCfg: config.SubAgentConfig{},
				Provider:    stubProvider{},
				ParentReg:   tool.NewRegistry(),
				Runner: &mockRunner{runFunc: func(_ context.Context, _ agent.RunRequest) (agent.RunState, error) {
					t.Error("runner must not be called for invalid timeout")
					return agent.RunState{}, nil
				}},
				Events:  noopEventSink{},
				WorkDir: "/tmp/work",
			}
			handler := NewDelegateHandler(deps)

			_, err := handler(context.Background(), map[string]any{
				"task":    "do something",
				"timeout": tt.timeoutVal,
			})
			if err == nil {
				t.Fatal("expected error for invalid timeout, got nil")
			}
			if !strings.Contains(err.Error(), "delegate: invalid timeout") {
				t.Errorf("error=%q, want to contain %q", err.Error(), "delegate: invalid timeout")
			}
		})
	}
}

func TestToolHandler_AppliesLimitOverrides(t *testing.T) {
	var got []agent.Limits
	deps := DelegateHandlerDeps{
		SubAgentCfg: config.SubAgentConfig{MaxTurns: 15, MaxTokens: 100000},
		Provider:    stubProvider{},
		ParentReg:   tool.NewRegistry(),
		Runner: &mockRunner{runFunc: func(_ context.Context, req agent.RunRequest) (agent.RunState, error) {
			got = append(got, req.Limits)
			return successRunState(), nil
		}},
		Events:  noopEventSink{},
		WorkDir: "/tmp/work",
	}
	handler := NewDelegateHandler(deps)

	_, err := handler(context.Background(), map[string]any{
		"task":      "do something",
		"max_turns": float64(5),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("runner was not called")
	}
	if got[0].MaxTurns != 5 {
		t.Errorf("MaxTurns=%d, want 5 (tighter override from 15)", got[0].MaxTurns)
	}
	if got[0].MaxTokens != 100000 {
		t.Errorf("MaxTokens=%d, want 100000 (no override)", got[0].MaxTokens)
	}
}

func TestToolHandler_UniqueAgentID(t *testing.T) {
	deps := DelegateHandlerDeps{
		SubAgentCfg: config.SubAgentConfig{},
		Provider:    stubProvider{},
		ParentReg:   tool.NewRegistry(),
		Runner: &mockRunner{runFunc: func(_ context.Context, _ agent.RunRequest) (agent.RunState, error) {
			return successRunState(), nil
		}},
		Events:  noopEventSink{},
		WorkDir: "/tmp/work",
	}
	handler := NewDelegateHandler(deps)
	ctx := context.Background()

	ids := make([]string, 5)
	for i := range ids {
		result, err := handler(ctx, map[string]any{"task": "do something"})
		if err != nil {
			t.Fatalf("call %d: unexpected error: %v", i, err)
		}
		execResult, ok := result.(tool.ExecutionResult)
		if !ok {
			t.Fatalf("call %d: result type=%T, want tool.ExecutionResult", i, result)
		}
		r, ok := execResult.Value.(DelegationResult)
		if !ok {
			t.Fatalf("call %d: result.Value type=%T, want DelegationResult", i, execResult.Value)
		}
		ids[i] = r.AgentID
	}

	seen := make(map[string]bool, len(ids))
	for i, id := range ids {
		if !strings.HasPrefix(id, "child-") {
			t.Errorf("id[%d]=%q does not have child- prefix", i, id)
		}
		if seen[id] {
			t.Fatalf("duplicate agentID at index %d: %q", i, id)
		}
		seen[id] = true
	}
}

// TestGenerateAgentID_UniqueUnderRace verifies that concurrent calls produce
// unique IDs with no data races. Run with -race.
func TestGenerateAgentID_UniqueUnderRace(t *testing.T) {
	const n = 200
	results := make([]string, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			results[i] = generateAgentID()
		}()
	}
	wg.Wait()

	seen := make(map[string]struct{}, n)
	for _, id := range results {
		if !strings.HasPrefix(id, "child-") {
			t.Errorf("id %q missing child- prefix", id)
		}
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate id %q", id)
		}
		seen[id] = struct{}{}
	}
}

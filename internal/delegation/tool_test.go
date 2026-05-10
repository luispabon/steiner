package delegation

import (
	"context"
	"testing"

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
	handler := func(ctx context.Context, input map[string]any) (any, error) {
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

	handler(context.Background(), nil)
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
		name         string
		timeoutVal   any
		wantDeadline bool
	}{
		{name: "valid 30s", timeoutVal: "30s", wantDeadline: true},
		{name: "valid 5m", timeoutVal: "5m", wantDeadline: true},
		{name: "invalid ignored", timeoutVal: "not-a-duration", wantDeadline: false},
		{name: "empty ignored", timeoutVal: "", wantDeadline: false},
		{name: "missing", timeoutVal: nil, wantDeadline: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotDeadline bool
			deps := DelegateHandlerDeps{
				SubAgentCfg: config.SubAgentConfig{},
				Provider:    stubProvider{},
				ParentReg:   tool.NewRegistry(),
				Runner: &mockRunner{runFunc: func(ctx context.Context, _ agent.RunRequest) (agent.RunState, error) {
					_, gotDeadline = ctx.Deadline()
					return successRunState(), nil
				}},
				Events:  noopEventSink{},
				WorkDir: "/tmp/work",
			}
			handler := NewDelegateHandler(deps)

			input := map[string]any{"task": "do something"}
			if tt.timeoutVal != nil {
				input["timeout"] = tt.timeoutVal
			}

			_, err := handler(context.Background(), input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if gotDeadline != tt.wantDeadline {
				t.Errorf("context has deadline=%v, want %v", gotDeadline, tt.wantDeadline)
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

	for i := 1; i < len(ids); i++ {
		if ids[i] == ids[i-1] {
			t.Fatalf("duplicate agentID at index %d: %q", i, ids[i])
		}
	}
}

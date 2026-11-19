package delegation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

// followUpBudgetProvider drives a parent that dispatches one explore child and,
// optionally, resumes it once with follow_up. Every child turn calls the read
// tool, which always fails, so each child run trips the repeat guard.
type followUpBudgetProvider struct {
	followUp    bool
	parentCalls int
	childCalls  int
}

func (p *followUpBudgetProvider) ChatCompletion(_ context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
	if !isRepeatGuardParent(req) {
		p.childCalls++
		return provider.ChatResponse{
			Message: provider.Message{
				Role: provider.MessageRoleAssistant,
				ToolCalls: []provider.ToolCall{{
					ID:        fmt.Sprintf("child_%d", p.childCalls),
					Name:      "read",
					Arguments: map[string]any{},
				}},
			},
			FinishReason: "tool_calls",
		}, nil
	}

	p.parentCalls++
	switch {
	case p.parentCalls == 1:
		return provider.ChatResponse{
			Message: provider.Message{
				Role: provider.MessageRoleAssistant,
				ToolCalls: []provider.ToolCall{{
					ID:   "parent_1",
					Name: SubAgentToolName,
					Arguments: map[string]any{
						"type":             "explore",
						"objective":        "fail-task",
						"context":          "context",
						"deliverable":      "deliverable",
						"constraints":      []any{},
						"success_criteria": []any{},
						"checks":           []any{},
					},
				}},
			},
			FinishReason: "tool_calls",
		}, nil
	case p.followUp && p.parentCalls == 2:
		agentID := continuationAgentID(req.Messages, "parent_1")
		if agentID == "" {
			return provider.ChatResponse{}, fmt.Errorf("parent follow_up: no continuation agent id in %d messages", len(req.Messages))
		}
		return provider.ChatResponse{
			Message: provider.Message{
				Role: provider.MessageRoleAssistant,
				ToolCalls: []provider.ToolCall{{
					ID:   "parent_2",
					Name: FollowUpToolName,
					Arguments: map[string]any{
						"agent_id": agentID,
						"message":  "retry with a corrected call",
					},
				}},
			},
			FinishReason: "tool_calls",
		}, nil
	default:
		return provider.ChatResponse{
			Message:      provider.Message{Role: provider.MessageRoleAssistant, Content: "parent done"},
			FinishReason: "stop",
		}, nil
	}
}

func (*followUpBudgetProvider) StreamChatCompletion(context.Context, provider.ChatRequest) (<-chan provider.ChatChunk, error) {
	return nil, errors.New("streaming not expected in follow-up budget test")
}

func (*followUpBudgetProvider) SupportsUsageStats() bool { return false }

// continuationAgentID extracts the persisted agent id from a prior delegation
// tool result in the parent conversation.
func continuationAgentID(messages []provider.Message, toolCallID string) string {
	for _, msg := range messages {
		if msg.Role != provider.MessageRoleTool || msg.ToolCallID != toolCallID {
			continue
		}
		var envelope struct {
			Continuation *struct {
				AgentID string `json:"agent_id"`
			} `json:"continuation"`
		}
		if err := json.Unmarshal([]byte(msg.Content), &envelope); err != nil {
			continue
		}
		if envelope.Continuation != nil {
			return envelope.Continuation.AgentID
		}
	}
	return ""
}

// projectedDelegation is the provider-facing delegation result as the parent
// sees it.
type projectedDelegation struct {
	Status       string `json:"status"`
	Reason       string `json:"reason"`
	Continuation *struct {
		AgentID string `json:"agent_id"`
	} `json:"continuation"`
}

func findProjectedDelegation(t *testing.T, state agent.RunState, toolCallID string) projectedDelegation {
	t.Helper()
	for _, msg := range state.Conversation {
		if msg.Role != agent.MessageRoleTool || msg.ToolCallID != toolCallID {
			continue
		}
		var projected projectedDelegation
		if err := json.Unmarshal([]byte(msg.Content), &projected); err != nil {
			t.Fatalf("delegation result %s is not JSON: %v (%q)", toolCallID, err, msg.Content)
		}
		return projected
	}
	t.Fatalf("no delegation tool result for %s", toolCallID)
	return projectedDelegation{}
}

// runFollowUpBudgetRun drives one parent run through the real delegate registry
// and the real child runner with the given follow-up cap.
func runFollowUpBudgetRun(t *testing.T, maxFollowUps int, store *SessionStore) (agent.RunState, *followUpBudgetProvider) {
	t.Helper()
	prov := &followUpBudgetProvider{followUp: true}
	base := tool.NewRegistry(tool.ToolDef{
		Name:        "read",
		Description: "read a file",
		Handler: func(context.Context, map[string]any) (any, error) {
			return nil, errors.New("boom")
		},
	})
	reg, err := BuildDelegateRegistry(DelegateDeps{
		BaseRegistry: base,
		SubAgentCfg:  config.SubAgentConfig{Enabled: true, MaxTurns: 10, MaxTokens: 1000, MaxFollowUps: maxFollowUps},
		Provider:     prov,
		Config:       config.Config{},
		WorkDir:      t.TempDir(),
		Events:       output.NoopSink{},
		SessionStore: store,
	})
	if err != nil {
		t.Fatalf("BuildDelegateRegistry() error = %v", err)
	}

	state, err := agent.NewRunner().Run(context.Background(), agent.RunRequest{
		Provider: prov,
		Executor: tool.NewExecutor(reg, config.Config{}, nil, t.TempDir(), "", tool.Unsandboxed{}),
		Tools:    reg.ToProviderSpecs(),
		Prompt: prompt.AssemblyOptions{Conversation: []provider.Message{
			{Role: provider.MessageRoleUser, Content: "start"},
		}},
		Limits: agent.Limits{MaxTurns: 10},
		Events: output.NoopSink{},
	})
	if err != nil {
		t.Fatalf("parent Run() error = %v", err)
	}
	return state, prov
}

func TestFollowUpRepeatedFailureRemainingBudgetHint(t *testing.T) {
	state, prov := runFollowUpBudgetRun(t, 5, nil)
	if prov.parentCalls != 3 {
		t.Fatalf("parent model calls = %d, want 3", prov.parentCalls)
	}

	initial := findProjectedDelegation(t, state, "parent_1")
	if initial.Status != "failed" {
		t.Fatalf("initial status = %q, want failed", initial.Status)
	}
	if !strings.Contains(initial.Reason, repeatedToolFailureFollowUpHint) {
		t.Errorf("initial reason = %q, want follow-up guidance", initial.Reason)
	}

	resumed := findProjectedDelegation(t, state, "parent_2")
	if resumed.Status != "failed" {
		t.Fatalf("follow_up status = %q, want failed", resumed.Status)
	}
	if !strings.Contains(resumed.Reason, repeatedToolFailureFollowUpHint) {
		t.Errorf("follow_up reason = %q, want follow-up guidance with budget remaining", resumed.Reason)
	}
	if strings.Contains(resumed.Reason, repeatedToolFailureExhaustedHint) {
		t.Errorf("follow_up reason = %q, want no exhausted-budget guidance", resumed.Reason)
	}
}

func TestFollowUpRepeatedFailureExhaustedBudgetHint(t *testing.T) {
	state, prov := runFollowUpBudgetRun(t, 1, nil)
	if prov.parentCalls != 3 {
		t.Fatalf("parent model calls = %d, want 3", prov.parentCalls)
	}

	initial := findProjectedDelegation(t, state, "parent_1")
	if !strings.Contains(initial.Reason, repeatedToolFailureFollowUpHint) {
		t.Errorf("initial reason = %q, want follow-up guidance before the limit is used", initial.Reason)
	}

	resumed := findProjectedDelegation(t, state, "parent_2")
	if resumed.Status != "failed" {
		t.Fatalf("follow_up status = %q, want failed", resumed.Status)
	}
	if !strings.Contains(resumed.Reason, repeatedToolFailureExhaustedHint) {
		t.Errorf("follow_up reason = %q, want exhausted-budget guidance", resumed.Reason)
	}
	if strings.Contains(resumed.Reason, repeatedToolFailureFollowUpHint) {
		t.Errorf("follow_up reason = %q, want no follow-up guidance", resumed.Reason)
	}
}

// TestSpecializedHandlerUnsavedRepeatedFailureHasNoHint drives the real explore
// handler with a session store that rejects the save, so the persisted gate
// suppresses both hint variants.
func TestSpecializedHandlerUnsavedRepeatedFailureHasNoHint(t *testing.T) {
	originalIDGen := idGen
	idGen = func() string { return "unsaved-child" }
	t.Cleanup(func() { idGen = originalIDGen })

	store := NewSessionStore()
	store.Invalidate("unsaved-child")

	prov := &followUpBudgetProvider{}
	base := tool.NewRegistry(tool.ToolDef{
		Name:        "read",
		Description: "read a file",
		Handler: func(context.Context, map[string]any) (any, error) {
			return nil, errors.New("boom")
		},
	})
	reg, err := BuildDelegateRegistry(DelegateDeps{
		BaseRegistry: base,
		SubAgentCfg:  config.SubAgentConfig{Enabled: true, MaxTurns: 10, MaxTokens: 1000, MaxFollowUps: 5},
		Provider:     prov,
		Config:       config.Config{},
		WorkDir:      t.TempDir(),
		Events:       output.NoopSink{},
		SessionStore: store,
	})
	if err != nil {
		t.Fatalf("BuildDelegateRegistry() error = %v", err)
	}

	state, err := agent.NewRunner().Run(context.Background(), agent.RunRequest{
		Provider: prov,
		Executor: tool.NewExecutor(reg, config.Config{}, nil, t.TempDir(), "", tool.Unsandboxed{}),
		Tools:    reg.ToProviderSpecs(),
		Prompt: prompt.AssemblyOptions{Conversation: []provider.Message{
			{Role: provider.MessageRoleUser, Content: "start"},
		}},
		Limits: agent.Limits{MaxTurns: 10},
		Events: output.NoopSink{},
	})
	if err != nil {
		t.Fatalf("parent Run() error = %v", err)
	}

	projected := findProjectedDelegation(t, state, "parent_1")
	if projected.Status != "failed" {
		t.Fatalf("status = %q, want failed", projected.Status)
	}
	if projected.Continuation != nil {
		t.Errorf("continuation = %+v, want none for an unsaved session", projected.Continuation)
	}
	if strings.Contains(projected.Reason, repeatedToolFailureFollowUpHint) || strings.Contains(projected.Reason, repeatedToolFailureExhaustedHint) {
		t.Errorf("reason = %q, want no follow-up guidance for an unsaved session", projected.Reason)
	}
}

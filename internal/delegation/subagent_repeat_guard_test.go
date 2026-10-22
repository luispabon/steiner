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

// repeatGuardSubAgentCalls is the number of sub_agent calls the parent makes.
// Three identical failures would trip the parent's repeat guard if a failed
// sub-agent result counted as a tool failure.
const repeatGuardSubAgentCalls = 3

// repeatGuardProvider serves both the parent and the child of a delegate run.
// The parent calls sub_agent repeatGuardSubAgentCalls times, then answers in
// text. Every child turn calls the read tool, which always fails.
type repeatGuardProvider struct {
	parentCalls int
	childCalls  int
}

func (p *repeatGuardProvider) ChatCompletion(_ context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
	if !isRepeatGuardParent(req) {
		p.childCalls++
		return provider.ChatResponse{
			Message: provider.Message{
				Role: provider.MessageRoleAssistant,
				ToolCalls: []provider.ToolCall{
					{ID: fmt.Sprintf("child_%d", p.childCalls), Name: "read", Arguments: map[string]any{}},
				},
			},
			FinishReason: "tool_calls",
		}, nil
	}
	p.parentCalls++
	if p.parentCalls <= repeatGuardSubAgentCalls {
		return provider.ChatResponse{
			Message: provider.Message{
				Role: provider.MessageRoleAssistant,
				ToolCalls: []provider.ToolCall{{
					ID:   fmt.Sprintf("parent_%d", p.parentCalls),
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
	}
	return provider.ChatResponse{
		Message:      provider.Message{Role: provider.MessageRoleAssistant, Content: "parent done"},
		FinishReason: "stop",
	}, nil
}

func (*repeatGuardProvider) StreamChatCompletion(context.Context, provider.ChatRequest) (<-chan provider.ChatChunk, error) {
	return nil, errors.New("streaming not expected in repeat guard test")
}

func (*repeatGuardProvider) SupportsUsageStats() bool { return false }

// isRepeatGuardParent reports whether req is the parent turn. The parent's only
// user message is "start"; child requests carry the delegated objective.
func isRepeatGuardParent(req provider.ChatRequest) bool {
	for _, msg := range req.Messages {
		if msg.Role == provider.MessageRoleUser {
			return msg.Content == "start"
		}
	}
	return false
}

// TestSubAgentRepeatedChildFailureDoesNotTripParentGuard drives a real parent
// run through the delegate registry and the real child runner. Each child
// stops after three identical read failures with a failed result. The parent
// receives that as a successful sub_agent tool result, so its own guard does
// not trip and it finishes its run.
func TestSubAgentRepeatedChildFailureDoesNotTripParentGuard(t *testing.T) {
	prov := &repeatGuardProvider{}
	readCalls := 0
	base := tool.NewRegistry(tool.ToolDef{
		Name:        "read",
		Description: "read a file",
		Handler: func(context.Context, map[string]any) (any, error) {
			readCalls++
			return nil, errors.New("boom")
		},
	})
	reg, err := BuildDelegateRegistry(DelegateDeps{
		BaseRegistry: base,
		SubAgentCfg:  config.SubAgentConfig{Enabled: true, MaxTurns: 10, MaxTokens: 1000, MaxFollowUps: 100},
		Provider:     prov,
		Config:       config.Config{},
		WorkDir:      "/tmp",
		Events:       output.NoopSink{},
	})
	if err != nil {
		t.Fatalf("BuildDelegateRegistry() error = %v", err)
	}

	state, err := agent.NewRunner().Run(context.Background(), agent.RunRequest{
		Provider: prov,
		Executor: tool.NewExecutor(reg, config.Config{}, nil, "/tmp", "", tool.Unsandboxed{}),
		Tools:    reg.ToProviderSpecs(),
		Prompt: prompt.AssemblyOptions{Conversation: []provider.Message{
			{Role: provider.MessageRoleUser, Content: "start"},
		}},
		Limits: agent.Limits{MaxTurns: 10},
		Events: output.NoopSink{},
	})
	if err != nil {
		t.Fatalf("parent Run() error = %v, want nil: the parent guard tripped", err)
	}
	if state.StopReason != agent.StopReasonComplete {
		t.Fatalf("parent StopReason = %q, want %q", state.StopReason, agent.StopReasonComplete)
	}
	if prov.parentCalls != repeatGuardSubAgentCalls+1 {
		t.Fatalf("parent model calls = %d, want %d", prov.parentCalls, repeatGuardSubAgentCalls+1)
	}

	// Each child stops after maxIdenticalToolFailures read failures, so the
	// child model is called three times per sub_agent call.
	wantChildCalls := repeatGuardSubAgentCalls * 3
	if prov.childCalls != wantChildCalls {
		t.Fatalf("child model calls = %d, want %d", prov.childCalls, wantChildCalls)
	}
	if readCalls != wantChildCalls {
		t.Fatalf("read executions = %d, want %d", readCalls, wantChildCalls)
	}

	subAgentResults := 0
	for _, msg := range state.Conversation {
		if msg.Role != agent.MessageRoleTool || !strings.HasPrefix(msg.ToolCallID, "parent_") {
			continue
		}
		subAgentResults++
		var envelope map[string]any
		if err := json.Unmarshal([]byte(msg.Content), &envelope); err != nil {
			t.Fatalf("sub_agent result %s is not JSON: %v (%q)", msg.ToolCallID, err, msg.Content)
		}
		if envelope["status"] != "failed" {
			t.Fatalf("sub_agent result %s status = %v, want failed (content %q)", msg.ToolCallID, envelope["status"], msg.Content)
		}
	}
	if subAgentResults != repeatGuardSubAgentCalls {
		t.Fatalf("sub_agent results = %d, want %d", subAgentResults, repeatGuardSubAgentCalls)
	}
}

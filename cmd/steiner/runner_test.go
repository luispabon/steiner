package main

import (
	"context"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/delegation"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

func TestToProviderConversationPreservesTurn(t *testing.T) {
	messages := []agent.Message{
		{Role: agent.MessageRoleUser, Content: "hello", Turn: 4},
		{Role: agent.MessageRoleAssistant, Content: "world", Turn: 5},
		{
			Role:       agent.MessageRoleTool,
			Content:    "result",
			ToolCallID: "call_1",
			Turn:       6,
		},
	}

	got := toProviderConversation(messages)
	if len(got) != len(messages) {
		t.Fatalf("len(got) = %d, want %d", len(got), len(messages))
	}
	for i, message := range got {
		if message.Turn != messages[i].Turn {
			t.Fatalf("message %d turn = %d, want %d", i, message.Turn, messages[i].Turn)
		}
	}
	if got[2].ToolCallID != "call_1" {
		t.Fatalf("tool call id = %q, want call_1", got[2].ToolCallID)
	}
	if got[0].Role != provider.MessageRoleUser || got[1].Role != provider.MessageRoleAssistant || got[2].Role != provider.MessageRoleTool {
		t.Fatalf("roles preserved incorrectly: %#v", got)
	}
}

// stubProvider satisfies provider.Provider for testing.
type stubProvider struct{}

func (stubProvider) ChatCompletion(context.Context, provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, nil
}
func (stubProvider) StreamChatCompletion(context.Context, provider.ChatRequest) (<-chan provider.ChatChunk, error) {
	return nil, nil
}
func (stubProvider) SupportsUsageStats() bool { return false }

type noopSink struct{}

func (noopSink) Emit(output.Event) {}

func TestBuildActiveRegistry_DelegatePresent_WhenEnabled(t *testing.T) {
	base := tool.NewRegistry(tool.ToolDef{Name: "bash", Description: "run bash"})
	cfg := config.SubAgentConfig{Enabled: true}
	reg := buildActiveRegistry(base, cfg, stubProvider{}, noopSink{}, "/tmp")

	found := false
	for _, n := range reg.Names() {
		if n == delegation.DelegateToolName {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("delegate tool not found in registry when sub_agent.enabled=true; got %v", reg.Names())
	}

	// base registry must not be polluted
	for _, n := range base.Names() {
		if n == delegation.DelegateToolName {
			t.Error("delegate tool leaked into base registry")
		}
	}
}

func TestBuildActiveRegistry_DelegateAbsent_WhenDisabled(t *testing.T) {
	base := tool.NewRegistry(tool.ToolDef{Name: "bash", Description: "run bash"})
	cfg := config.SubAgentConfig{Enabled: false}
	reg := buildActiveRegistry(base, cfg, stubProvider{}, noopSink{}, "/tmp")

	for _, n := range reg.Names() {
		if n == delegation.DelegateToolName {
			t.Errorf("delegate tool present in registry when sub_agent.enabled=false")
		}
	}
}

func TestBuildActiveRegistry_DisabledReturnsSamePointer(t *testing.T) {
	base := tool.NewRegistry()
	cfg := config.SubAgentConfig{Enabled: false}
	reg := buildActiveRegistry(base, cfg, stubProvider{}, noopSink{}, "/tmp")
	if reg != base {
		t.Error("expected same registry pointer when sub_agent disabled")
	}
}

// Ensure delegation package's AgentRunner interface is satisfied by agent.Runner
// (compile-time check via assignment).
var _ delegation.AgentRunner = agent.NewRunner()

package delegation_test

import (
	"context"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/delegation"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

// fakeProvider is an in-memory provider that returns pre-configured responses.
type fakeProvider struct {
	responses []provider.ChatResponse
	callCount int
}

func (f *fakeProvider) ChatCompletion(ctx context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
	resp := f.responses[f.callCount%len(f.responses)]
	f.callCount++
	return resp, nil
}

func (f *fakeProvider) StreamChatCompletion(ctx context.Context, req provider.ChatRequest) (<-chan provider.ChatChunk, error) {
	ch := make(chan provider.ChatChunk, 1)
	resp := f.responses[f.callCount%len(f.responses)]
	f.callCount++
	ch <- provider.ChatChunk{
		Done:         true,
		FinishReason: "stop",
		Delta: provider.Message{
			Role:    provider.MessageRoleAssistant,
			Content: resp.Message.Content,
		},
	}
	close(ch)
	return ch, nil
}

func (f *fakeProvider) SupportsUsageStats() bool { return false }

// collectingSink collects emitted events for assertions.
type collectingSink struct {
	events []output.Event
}

func (c *collectingSink) Emit(e output.Event) {
	c.events = append(c.events, e)
}

func makeSpec(agentID string, outputLimitTokens int) delegation.DelegationSpec {
	return delegation.DelegationSpec{
		Task:    "test task",
		AgentID: agentID,
		Limits: delegation.DelegationLimits{
			MaxTurns:          5,
			OutputLimitTokens: outputLimitTokens,
		},
	}
}

func TestBasicDelegationResult(t *testing.T) {
	prov := &fakeProvider{
		responses: []provider.ChatResponse{
			{Message: provider.Message{Content: "done"}, FinishReason: "stop"},
		},
	}

	spec := makeSpec("agent-1", 1000)
	childReg := tool.NewRegistry()
	agentLimits := agent.Limits{MaxTurns: 5, MaxTokens: 0}
	sink := output.NoopSink{}
	req := delegation.BuildChildRunRequest(spec, prov, childReg, agentLimits, sink)

	runner := agent.NewRunner()
	result, err := delegation.SpawnDelegate(context.Background(), spec, req, runner, sink)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.AgentID != "agent-1" {
		t.Errorf("AgentID: got %q, want %q", result.AgentID, "agent-1")
	}
	if result.Status != delegation.StatusComplete {
		t.Errorf("Status: got %q, want %q", result.Status, delegation.StatusComplete)
	}
	if result.Output != "done" {
		t.Errorf("Output: got %q, want %q", result.Output, "done")
	}
	if result.TurnCount != 1 {
		t.Errorf("TurnCount: got %d, want 1", result.TurnCount)
	}
}

func TestDelegationEvents(t *testing.T) {
	prov := &fakeProvider{
		responses: []provider.ChatResponse{
			{Message: provider.Message{Content: "done"}, FinishReason: "stop"},
		},
	}

	spec := makeSpec("agent-2", 1000)
	childReg := tool.NewRegistry()
	agentLimits := agent.Limits{MaxTurns: 5, MaxTokens: 0}
	sink := &collectingSink{}
	req := delegation.BuildChildRunRequest(spec, prov, childReg, agentLimits, sink)

	runner := agent.NewRunner()
	_, err := delegation.SpawnDelegate(context.Background(), spec, req, runner, sink)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var hasStarted, hasComplete bool
	for _, ev := range sink.events {
		if ev.Type == output.EventTypeDelegationStarted {
			hasStarted = true
		}
		if ev.Type == output.EventTypeDelegationComplete {
			hasComplete = true
		}
	}
	if !hasStarted {
		t.Error("expected at least one DelegationStarted event")
	}
	if !hasComplete {
		t.Error("expected at least one DelegationComplete event")
	}
}

func TestChildRegistryExcludesDelegate(t *testing.T) {
	parentReg := tool.NewRegistry(tool.ToolDef{
		Name:        "delegate",
		Description: "delegate tool",
	})

	childReg := delegation.BuildChildToolRegistry(parentReg, "delegate")

	_, ok := childReg.Get("delegate")
	if ok {
		t.Error("child registry should not contain delegate tool")
	}
}

func TestOversizedOutputTriggersSummarisation(t *testing.T) {
	longContent := strings.Repeat("x", 5000) // ~1250 tokens by 4-char estimate

	prov := &fakeProvider{
		responses: []provider.ChatResponse{
			{Message: provider.Message{Content: longContent}, FinishReason: "stop"},
			{Message: provider.Message{Content: "short summary"}, FinishReason: "stop"},
		},
	}

	spec := delegation.DelegationSpec{
		Task:    "test task",
		AgentID: "agent-4",
		Limits: delegation.DelegationLimits{
			MaxTurns:          5,
			OutputLimitTokens: 100, // 5000/4=1250 > 100 → oversized
		},
	}

	childReg := tool.NewRegistry()
	agentLimits := agent.Limits{MaxTurns: 5, MaxTokens: 0}
	sink := output.NoopSink{}
	req := delegation.BuildChildRunRequest(spec, prov, childReg, agentLimits, sink)

	runner := agent.NewRunner()
	result, err := delegation.SpawnDelegate(context.Background(), spec, req, runner, sink)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Summary != "short summary" {
		t.Errorf("Summary: got %q, want %q", result.Summary, "short summary")
	}
	if prov.callCount != 2 {
		t.Errorf("callCount: got %d, want 2", prov.callCount)
	}
}

func TestDelegateHandlerTaskRequired(t *testing.T) {
	prov := &fakeProvider{
		responses: []provider.ChatResponse{
			{Message: provider.Message{Content: "done"}, FinishReason: "stop"},
		},
	}

	deps := delegation.DelegateHandlerDeps{
		Provider:    prov,
		ParentReg:   tool.NewRegistry(),
		SubAgentCfg: config.SubAgentConfig{MaxTurns: 5, MaxTokens: 10000},
		Events:      output.NoopSink{},
		Runner:      agent.NewRunner(),
	}

	handler := delegation.NewDelegateHandler(deps)
	_, err := handler(context.Background(), map[string]any{})
	if err == nil {
		t.Error("expected error when task is missing")
	}
}

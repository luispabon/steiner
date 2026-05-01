package delegation

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

// fakeProvider is an in-memory provider that returns pre-configured responses.
type fakeProvider struct {
	responses []provider.ChatResponse
	callCount int
	requests  []provider.ChatRequest
}

func (f *fakeProvider) ChatCompletion(ctx context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
	f.requests = append(f.requests, req)
	resp := f.responses[f.callCount%len(f.responses)]
	f.callCount++
	return resp, nil
}

func (f *fakeProvider) StreamChatCompletion(ctx context.Context, req provider.ChatRequest) (<-chan provider.ChatChunk, error) {
	f.requests = append(f.requests, req)
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

// testBuildPrompt is a test helper that builds prompt options for a spec.
func testBuildPrompt(spec DelegationSpec) prompt.AssemblyOptions {
	p, err := buildChildPrompt(spec)
	if err != nil {
		panic("testBuildPrompt: " + err.Error())
	}
	return p
}

// testChildRegistries is a test helper that builds visible and execution registries.
func testChildRegistries(parent *tool.Registry) (*tool.Registry, *tool.Registry) {
	return buildChildRegistries(parent, "delegate")
}

func makeSpec(agentID string, outputLimitTokens int) DelegationSpec {
	return DelegationSpec{
		Task:    "test task",
		AgentID: agentID,
		Limits: DelegationLimits{
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
	agentLimits := agent.Limits{MaxTurns: 5, MaxTokens: 0}
	sink := output.NoopSink{}
	visibleReg, execReg := testChildRegistries(tool.NewRegistry())
	req := buildChildRunRequest("/tmp/work", spec, prov, visibleReg, execReg, agentLimits, sink, testBuildPrompt(spec))

	runner := agent.NewRunner()
	result, err := SpawnDelegate(context.Background(), spec, req, runner, sink)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.AgentID != "agent-1" {
		t.Errorf("AgentID: got %q, want %q", result.AgentID, "agent-1")
	}
	if result.Status != StatusComplete {
		t.Errorf("Status: got %q, want %q", result.Status, StatusComplete)
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
	agentLimits := agent.Limits{MaxTurns: 5, MaxTokens: 0}
	sink := &collectingSink{}
	visibleReg, execReg := testChildRegistries(tool.NewRegistry())
	req := buildChildRunRequest("/tmp/work", spec, prov, visibleReg, execReg, agentLimits, sink, testBuildPrompt(spec))

	runner := agent.NewRunner()
	_, err := SpawnDelegate(context.Background(), spec, req, runner, sink)
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

func TestOversizedOutputTriggersSummarisation(t *testing.T) {
	longContent := strings.Repeat("x", 5000)
	prov := &fakeProvider{
		responses: []provider.ChatResponse{
			{Message: provider.Message{Content: longContent}, FinishReason: "stop"},
			{Message: provider.Message{Content: "short summary"}, FinishReason: "stop"},
		},
	}

	spec := DelegationSpec{
		Task:    "test task",
		AgentID: "agent-4",
		Limits: DelegationLimits{
			MaxTurns:          5,
			OutputLimitTokens: 100,
		},
	}

	agentLimits := agent.Limits{MaxTurns: 5, MaxTokens: 0}
	sink := output.NoopSink{}
	visibleReg, execReg := testChildRegistries(tool.NewRegistry())
	req := buildChildRunRequest("/tmp/work", spec, prov, visibleReg, execReg, agentLimits, sink, testBuildPrompt(spec))

	runner := agent.NewRunner()
	result, err := SpawnDelegate(context.Background(), spec, req, runner, sink)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Summary != "short summary" {
		t.Errorf("Summary: got %q, want %q", result.Summary, "short summary")
	}
	if prov.callCount != 2 {
		t.Errorf("callCount: got %d, want 2", prov.callCount)
	}
	if len(prov.requests) != 2 {
		t.Fatalf("requests: got %d, want 2", len(prov.requests))
	}

	summaryReq := prov.requests[1]
	var sawOversizedAnswer, sawLimitInstruction bool
	for _, msg := range summaryReq.Messages {
		if msg.Content == longContent {
			sawOversizedAnswer = true
		}
		if strings.Contains(msg.Content, "approximately 100 tokens") {
			sawLimitInstruction = true
		}
	}
	if !sawOversizedAnswer {
		t.Error("expected summary retry to include the oversized assistant response")
	}
	if !sawLimitInstruction {
		t.Error("expected summary retry to instruct the model to stay within approximately the output limit")
	}
}

func TestOversizedOutputReturnedOutputIsBounded(t *testing.T) {
	longContent := strings.Repeat("x", 5000)
	overlongSummary := strings.Repeat("y", 5000)
	prov := &fakeProvider{
		responses: []provider.ChatResponse{
			{Message: provider.Message{Content: longContent}, FinishReason: "stop"},
			{Message: provider.Message{Content: overlongSummary}, FinishReason: "stop"},
		},
	}

	spec := DelegationSpec{
		Task:    "test task",
		AgentID: "agent-5",
		Limits: DelegationLimits{
			MaxTurns:          5,
			OutputLimitTokens: 100,
		},
	}

	agentLimits := agent.Limits{MaxTurns: 5, MaxTokens: 0}
	sink := output.NoopSink{}
	visibleReg, execReg := testChildRegistries(tool.NewRegistry())
	req := buildChildRunRequest("/tmp/work", spec, prov, visibleReg, execReg, agentLimits, sink, testBuildPrompt(spec))

	runner := agent.NewRunner()
	result, err := SpawnDelegate(context.Background(), spec, req, runner, sink)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Output) > 100*4 {
		t.Fatalf("Output length %d exceeds bound", len(result.Output))
	}
	if len(result.Summary) > 100*4 {
		t.Fatalf("Summary length %d exceeds bound", len(result.Summary))
	}
}

func TestChildToolSurfaceAllowsToolsAndRejectsDelegate(t *testing.T) {
	parentReg := tool.NewRegistry(
		tool.ToolDef{
			Name:        "helper",
			Description: "helper tool",
			Handler: func(ctx context.Context, input map[string]any) (any, error) {
				return "helper-result", nil
			},
		},
		tool.ToolDef{
			Name:        "delegate",
			Description: "delegate tool",
			Handler: func(ctx context.Context, input map[string]any) (any, error) {
				return "should not be reachable", nil
			},
		},
	)

	spec := makeSpec("agent-6", 1000)
	visibleReg, execReg := buildChildRegistries(parentReg, "delegate")
	req := buildChildRunRequest("/tmp/work", spec, &fakeProvider{responses: []provider.ChatResponse{{Message: provider.Message{Content: "done"}, FinishReason: "stop"}}}, visibleReg, execReg, agent.Limits{MaxTurns: 5, MaxTokens: 0}, output.NoopSink{}, testBuildPrompt(spec))

	if len(req.Tools) != 1 {
		t.Fatalf("Tools length = %d, want 1", len(req.Tools))
	}
	if req.Tools[0].Function.Name != "helper" {
		t.Fatalf("Tools[0] = %q, want helper", req.Tools[0].Function.Name)
	}

	value, err := req.Executor.Execute(context.Background(), "helper", map[string]any{})
	if err != nil {
		t.Fatalf("helper tool execution failed: %v", err)
	}
	if value != "helper-result" {
		t.Fatalf("helper tool result = %v, want helper-result", value)
	}

	if _, err := req.Executor.Execute(context.Background(), "delegate", map[string]any{}); err == nil {
		t.Fatal("expected delegate tool execution to fail in child context")
	} else if !strings.Contains(err.Error(), "delegate") {
		t.Fatalf("delegate error = %v, want it to mention delegate", err)
	}
}

type blockingRunner struct {
	calls     int
	secondErr error
}

func (r *blockingRunner) Run(ctx context.Context, req agent.RunRequest) (agent.RunState, error) {
	r.calls++
	if r.calls == 1 {
		return agent.RunState{
			TurnCount: 1,
			Conversation: []agent.Message{
				{Role: agent.MessageRoleAssistant, Content: strings.Repeat("z", 5000)},
			},
		}, nil
	}
	<-ctx.Done()
	r.secondErr = ctx.Err()
	return agent.RunState{
		StopReason: agent.StopReasonCancelled,
	}, nil
}

func TestTimeoutEnforcedAcrossSummaryRetry(t *testing.T) {
	prov := &fakeProvider{
		responses: []provider.ChatResponse{
			{Message: provider.Message{Content: strings.Repeat("z", 5000)}, FinishReason: "stop"},
			{Message: provider.Message{Content: "unused"}, FinishReason: "stop"},
		},
	}

	spec := DelegationSpec{
		Task:    "test task",
		AgentID: "agent-7",
		Limits: DelegationLimits{
			MaxTurns:          5,
			OutputLimitTokens: 100,
			Timeout:           25 * time.Millisecond,
		},
	}

	visibleReg, execReg := testChildRegistries(tool.NewRegistry())
	req := buildChildRunRequest("/tmp/work", spec, prov, visibleReg, execReg, agent.Limits{MaxTurns: 5, MaxTokens: 0}, output.NoopSink{}, testBuildPrompt(spec))
	runner := &blockingRunner{}
	start := time.Now()
	result, err := SpawnDelegate(context.Background(), spec, req, runner, output.NoopSink{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if runner.calls != 2 {
		t.Fatalf("calls = %d, want 2", runner.calls)
	}
	if runner.secondErr == nil {
		t.Fatal("expected summary retry to observe deadline cancellation")
	}
	if time.Since(start) < spec.Limits.Timeout {
		t.Fatalf("timeout path returned too quickly: %v", time.Since(start))
	}
	if len(result.Output) > 100*4 {
		t.Fatalf("Output length %d exceeds bound after timeout path", len(result.Output))
	}
}

func TestDelegateHandlerTaskRequired(t *testing.T) {
	prov := &fakeProvider{
		responses: []provider.ChatResponse{
			{Message: provider.Message{Content: "done"}, FinishReason: "stop"},
		},
	}

	deps := DelegateHandlerDeps{
		Provider:    prov,
		ParentReg:   tool.NewRegistry(),
		SubAgentCfg: config.SubAgentConfig{MaxTurns: 5, MaxTokens: 10000},
		Events:      output.NoopSink{},
		Runner:      agent.NewRunner(),
		WorkDir:     "/tmp/work",
	}

	handler := NewDelegateHandler(deps)
	_, err := handler(context.Background(), map[string]any{})
	if err == nil {
		t.Error("expected error when task is missing")
	}
}

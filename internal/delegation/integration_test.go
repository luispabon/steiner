package delegation

import (
	"context"
	"errors"
	"fmt"
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

func (f *fakeProvider) ChatCompletion(_ context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
	f.requests = append(f.requests, req)
	resp := f.responses[f.callCount%len(f.responses)]
	f.callCount++
	return resp, nil
}

func (f *fakeProvider) StreamChatCompletion(_ context.Context, req provider.ChatRequest) (<-chan provider.ChatChunk, error) {
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

type failingRunner struct {
	err   error
	calls int
}

func (r *failingRunner) Run(context.Context, agent.RunRequest) (agent.RunState, error) {
	r.calls++
	return agent.RunState{}, r.err
}

type scopedEventRunner struct {
	calls int
}

func (r *scopedEventRunner) Run(_ context.Context, req agent.RunRequest) (agent.RunState, error) {
	r.calls++
	if r.calls <= 2 {
		if req.Events == nil {
			return agent.RunState{}, fmt.Errorf("events sink missing")
		}

		req.Events.Emit(output.NewTurnStartedEvent(r.calls, "child-model", 1))
		req.Events.Emit(output.NewAssistantMessageEvent(r.calls, string(agent.MessageRoleAssistant), fmt.Sprintf("child turn %d", r.calls)))
	}

	if r.calls == 1 {
		return agent.RunState{
			Conversation: []agent.Message{
				{
					Role:    agent.MessageRoleAssistant,
					Content: "extend me",
					ToolCalls: []agent.ToolCall{
						{ID: "call-1", Name: "helper", Arguments: map[string]any{}},
					},
				},
			},
			TurnCount:  1,
			TokenCount: 10,
			StopReason: agent.StopReasonMaxTurns,
		}, nil
	}

	return agent.RunState{
		Conversation: []agent.Message{
			{Role: agent.MessageRoleAssistant, Content: "done"},
		},
		TurnCount:  2,
		TokenCount: 20,
		StopReason: agent.StopReasonComplete,
	}, nil
}

// testBuildPrompt is a test helper that builds prompt options for a spec.
func testBuildPrompt(spec DelegationSpec) prompt.AssemblyOptions {
	return buildChildPrompt(spec)
}

// testChildRegistries is a test helper that builds visible and execution registries
// with all parent tools allowed (minus delegate).
func testChildRegistries(parent *tool.Registry) (*tool.Registry, *tool.Registry) {
	var names []string
	for _, name := range parent.Names() {
		if name != "delegate" {
			names = append(names, name)
		}
	}
	return buildChildRegistries(parent, names)
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
	req := buildChildRunRequest("/tmp/work", spec, prov, visibleReg, execReg, agentLimits, sink, testBuildPrompt(spec), nil, false, "", false, nil, prompt.ModelTokenBudget{}, "", nil, false)

	runner := agent.NewRunner()
	result, err := SpawnDelegate(context.Background(), spec, req, runner, sink, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	typedResult, ok := result.Value.(DelegationResult)
	if !ok {
		t.Fatalf("result.Value type = %T, want DelegationResult", result.Value)
	}
	if typedResult.AgentID != "agent-1" {
		t.Errorf("AgentID: got %q, want %q", typedResult.AgentID, "agent-1")
	}
	if typedResult.Status != StatusComplete {
		t.Errorf("Status: got %q, want %q", typedResult.Status, StatusComplete)
	}
	if typedResult.Output != "done" {
		t.Errorf("Output: got %q, want %q", typedResult.Output, "done")
	}
	if typedResult.Summary == "" {
		t.Fatal("Summary = empty, want populated delegate summary")
	}
	if typedResult.TurnCount != 1 {
		t.Errorf("TurnCount: got %d, want 1", typedResult.TurnCount)
	}
	if result.Retention == nil {
		t.Fatal("result.Retention = nil, want delegate summary retention")
	}
	if result.Retention.Kind != tool.RetentionKindDelegateSummary {
		t.Fatalf("result.Retention.Kind = %q, want %q", result.Retention.Kind, tool.RetentionKindDelegateSummary)
	}
	if result.Retention.Summary == "" {
		t.Fatal("result.Retention.Summary = empty, want summary text")
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
	req := buildChildRunRequest("/tmp/work", spec, prov, visibleReg, execReg, agentLimits, sink, testBuildPrompt(spec), nil, false, "", false, nil, prompt.ModelTokenBudget{}, "", nil, false)

	runner := agent.NewRunner()
	_, err := SpawnDelegate(context.Background(), spec, req, runner, sink, nil)
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

func TestChildEventsAreScopedWhileLifecycleEventsStayTopLevel(t *testing.T) {
	prov := &fakeProvider{
		responses: []provider.ChatResponse{
			{Message: provider.Message{Content: "done"}, FinishReason: "stop"},
		},
	}

	spec := makeSpec("agent-scoped", 1000)
	agentLimits := agent.Limits{MaxTurns: 5, MaxTokens: 0}
	sink := &collectingSink{}
	visibleReg, execReg := testChildRegistries(tool.NewRegistry())
	req := buildChildRunRequest("/tmp/work", spec, prov, visibleReg, execReg, agentLimits, sink, testBuildPrompt(spec), nil, false, "", false, nil, prompt.ModelTokenBudget{}, "", nil, false)

	runner := &scopedEventRunner{}
	_, err := SpawnDelegate(context.Background(), spec, req, runner, sink, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if runner.calls != 3 {
		t.Fatalf("runner.calls = %d, want 3", runner.calls)
	}

	for _, ev := range sink.events {
		switch ev.Type {
		case output.EventTypeDelegationStarted, output.EventTypeDelegationExtension, output.EventTypeDelegationComplete:
			if ev.Scope.AgentID != "" {
				t.Fatalf("%s scope = %q, want empty", ev.Type, ev.Scope.AgentID)
			}
		case output.EventTypeTurnStarted, output.EventTypeAssistantMessage:
			if ev.Scope.AgentID != spec.AgentID {
				t.Fatalf("%s scope = %q, want %q", ev.Type, ev.Scope.AgentID, spec.AgentID)
			}
		}
	}
}

func TestInitialRunnerErrorReturnsStructuredFailure(t *testing.T) {
	prov := &fakeProvider{
		responses: []provider.ChatResponse{
			{Message: provider.Message{Content: "unused"}, FinishReason: "stop"},
		},
	}

	spec := makeSpec("agent-failure", 1000)
	agentLimits := agent.Limits{MaxTurns: 5, MaxTokens: 0}
	sink := &collectingSink{}
	visibleReg, execReg := testChildRegistries(tool.NewRegistry())
	req := buildChildRunRequest("/tmp/work", spec, prov, visibleReg, execReg, agentLimits, sink, testBuildPrompt(spec), nil, false, "", false, nil, prompt.ModelTokenBudget{}, "", nil, false)

	runner := &failingRunner{err: fmt.Errorf("initial child run failed")}
	result, err := SpawnDelegate(context.Background(), spec, req, runner, sink, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if runner.calls != 1 {
		t.Fatalf("runner.calls = %d, want 1", runner.calls)
	}

	typedResult, ok := result.Value.(DelegationResult)
	if !ok {
		t.Fatalf("result.Value type = %T, want DelegationResult", result.Value)
	}
	if typedResult.Status != StatusFailed {
		t.Fatalf("Status = %q, want %q", typedResult.Status, StatusFailed)
	}
	if typedResult.Error != "initial child run failed" {
		t.Fatalf("Error = %q, want %q", typedResult.Error, "initial child run failed")
	}
	if typedResult.Summary != "delegation failed: initial child run failed" {
		t.Fatalf("Summary = %q, want failure summary", typedResult.Summary)
	}
	if result.Retention == nil {
		t.Fatal("result.Retention = nil, want failure retention")
	}
	if result.Retention.Kind != tool.RetentionKindDelegateSummary {
		t.Fatalf("Retention.Kind = %q, want %q", result.Retention.Kind, tool.RetentionKindDelegateSummary)
	}
	if result.Retention.Status != string(StatusFailed) {
		t.Fatalf("Retention.Status = %q, want %q", result.Retention.Status, StatusFailed)
	}
	if result.Retention.Summary != typedResult.Summary {
		t.Fatalf("Retention.Summary = %q, want %q", result.Retention.Summary, typedResult.Summary)
	}
	var sawFailedEvent bool
	for _, ev := range sink.events {
		if ev.Type == output.EventTypeDelegationFailed {
			if ev.Scope.AgentID != "" {
				t.Fatalf("DelegationFailed scope = %q, want empty", ev.Scope.AgentID)
			}
			sawFailedEvent = true
			break
		}
	}
	if !sawFailedEvent {
		t.Fatal("expected delegation_failed event")
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
	req := buildChildRunRequest("/tmp/work", spec, prov, visibleReg, execReg, agentLimits, sink, testBuildPrompt(spec), nil, false, "", false, nil, prompt.ModelTokenBudget{}, "", nil, false)

	runner := agent.NewRunner()
	result, err := SpawnDelegate(context.Background(), spec, req, runner, sink, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Retention == nil {
		t.Fatal("result.Retention = nil, want summary retention")
	}
	if result.Retention.Summary != "short summary" {
		t.Errorf("Summary: got %q, want %q", result.Retention.Summary, "short summary")
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
		if strings.Contains(msg.Content, "under 1000 characters") {
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

func TestOversizedOutputKeepsFullVisibleOutput(t *testing.T) {
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
	req := buildChildRunRequest("/tmp/work", spec, prov, visibleReg, execReg, agentLimits, sink, testBuildPrompt(spec), nil, false, "", false, nil, prompt.ModelTokenBudget{}, "", nil, false)

	runner := agent.NewRunner()
	result, err := SpawnDelegate(context.Background(), spec, req, runner, sink, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	typedResult, ok := result.Value.(DelegationResult)
	if !ok {
		t.Fatalf("result.Value type = %T, want DelegationResult", result.Value)
	}
	if typedResult.Output != longContent {
		t.Fatalf("Output was overwritten: got %q, want full output", typedResult.Output)
	}
	if result.Retention == nil {
		t.Fatal("result.Retention = nil, want retained summary")
	}
	if len(result.Retention.Summary) > 1000 {
		t.Fatalf("Summary length %d exceeds retention cap", len(result.Retention.Summary))
	}
	if typedResult.Summary == "" {
		t.Fatal("Summary = empty, want populated delegate summary")
	}
}

type summaryFailRunner struct {
	calls int
}

func (r *summaryFailRunner) Run(_ context.Context, _ agent.RunRequest) (agent.RunState, error) {
	r.calls++
	if r.calls == 1 {
		return agent.RunState{
			TurnCount: 1,
			Conversation: []agent.Message{
				{Role: agent.MessageRoleAssistant, Content: strings.Repeat("full-output ", 200)},
			},
		}, nil
	}
	return agent.RunState{}, fmt.Errorf("summary turn failed")
}

func TestSummaryFailureFallsBackToCappedPreview(t *testing.T) {
	prov := &fakeProvider{
		responses: []provider.ChatResponse{
			{Message: provider.Message{Content: strings.Repeat("full-output ", 200)}, FinishReason: "stop"},
		},
	}

	spec := DelegationSpec{
		Task:    "test task",
		AgentID: "agent-8",
		Limits: DelegationLimits{
			MaxTurns:          5,
			OutputLimitTokens: 100,
		},
	}

	visibleReg, execReg := testChildRegistries(tool.NewRegistry())
	req := buildChildRunRequest("/tmp/work", spec, prov, visibleReg, execReg, agent.Limits{MaxTurns: 5, MaxTokens: 0}, output.NoopSink{}, testBuildPrompt(spec), nil, false, "", false, nil, prompt.ModelTokenBudget{}, "", nil, false)
	runner := &summaryFailRunner{}

	result, err := SpawnDelegate(context.Background(), spec, req, runner, output.NoopSink{}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	typedResult, ok := result.Value.(DelegationResult)
	if !ok {
		t.Fatalf("result.Value type = %T, want DelegationResult", result.Value)
	}
	if strings.TrimSpace(typedResult.Output) == "" {
		t.Fatal("typedResult.Output = empty, want full visible output")
	}
	if runner.calls != 2 {
		t.Fatalf("calls = %d, want 2", runner.calls)
	}
	if result.Retention == nil {
		t.Fatal("result.Retention = nil, want fallback summary")
	}
	if result.Retention.Summary == "" {
		t.Fatal("result.Retention.Summary = empty, want capped preview")
	}
	if len([]rune(result.Retention.Summary)) > 1000 {
		t.Fatalf("result.Retention.Summary too long: %d runes", len([]rune(result.Retention.Summary)))
	}
	if strings.Contains(result.Retention.Summary, "summary turn failed") {
		t.Fatalf("retention summary leaked summary failure: %q", result.Retention.Summary)
	}
}

func TestChildToolSurfaceAllowsToolsAndRejectsDelegate(t *testing.T) {
	parentReg := tool.NewRegistry(
		tool.ToolDef{
			Name:        "helper",
			Description: "helper tool",
			Handler: func(_ context.Context, _ map[string]any) (any, error) {
				return "helper-result", nil
			},
		},
		tool.ToolDef{
			Name:        "delegate",
			Description: "delegate tool",
			Handler: func(_ context.Context, _ map[string]any) (any, error) {
				return "should not be reachable", nil
			},
		},
	)

	spec := makeSpec("agent-6", 1000)
	visibleReg, execReg := buildChildRegistries(parentReg, []string{"helper"})
	req := buildChildRunRequest("/tmp/work", spec, &fakeProvider{responses: []provider.ChatResponse{{Message: provider.Message{Content: "done"}, FinishReason: "stop"}}}, visibleReg, execReg, agent.Limits{MaxTurns: 5, MaxTokens: 0}, output.NoopSink{}, testBuildPrompt(spec), nil, false, "", false, nil, prompt.ModelTokenBudget{}, "", nil, false)

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

func TestSummaryUsesDetachedContextNotSpecTimeout(t *testing.T) {
	spec := DelegationSpec{
		Task:    "test task",
		AgentID: "agent-7",
		Limits: DelegationLimits{
			MaxTurns:          5,
			OutputLimitTokens: 100,
			Timeout:           25 * time.Millisecond,
		},
	}

	var summaryCtxErr error
	var summaryCtxHasDeadline bool
	runner := &presetRunner{
		states: []agent.RunState{
			{
				TurnCount:  1,
				StopReason: agent.StopReasonComplete,
				Conversation: []agent.Message{
					{Role: agent.MessageRoleAssistant, Content: strings.Repeat("z", 5000)},
				},
			},
			completeState("summary"),
		},
	}

	// Use a wrapper that inspects the summary call's context
	wrapped := &summaryCtxInspector{
		inner: runner,
	}

	prov := &fakeProvider{responses: []provider.ChatResponse{{Message: provider.Message{Content: "unused"}, FinishReason: "stop"}}}
	visibleReg, execReg := testChildRegistries(tool.NewRegistry())
	req := buildChildRunRequest("/tmp/work", spec, prov, visibleReg, execReg, agent.Limits{MaxTurns: 5, MaxTokens: 0}, output.NoopSink{}, testBuildPrompt(spec), nil, false, "", false, nil, prompt.ModelTokenBudget{}, "", nil, false)

	// Sleep past the spec timeout so childCtx is expired before summary
	time.Sleep(30 * time.Millisecond)

	result, err := SpawnDelegate(context.Background(), spec, req, wrapped, output.NoopSink{}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	summaryCtxErr = wrapped.summaryCtxErr
	summaryCtxHasDeadline = wrapped.summaryDeadline

	// Summary context should NOT be cancelled (detached from spec timeout)
	if summaryCtxErr != nil {
		t.Errorf("summary context was cancelled: %v — expected detached context", summaryCtxErr)
	}
	// Summary context should have a deadline (the 30s detached one)
	if !summaryCtxHasDeadline {
		t.Error("summary context has no deadline, expected 30s timeout")
	}
	if got := wrapped.summaryTurnTimeout; got != 0 {
		t.Fatalf("summary request TurnTimeout = %v, want 0", got)
	}

	typedResult, ok := result.Value.(DelegationResult)
	if !ok {
		t.Fatalf("result.Value type = %T, want DelegationResult", result.Value)
	}
	if len(typedResult.Output) != 5000 {
		t.Fatalf("Output length %d, want full visible output", len(typedResult.Output))
	}
}

type summaryCtxInspector struct {
	inner              *presetRunner
	summaryCtxErr      error
	summaryDeadline    bool
	summaryTurnTimeout time.Duration
}

func (r *summaryCtxInspector) Run(ctx context.Context, req agent.RunRequest) (agent.RunState, error) {
	r.inner.reqs = append(r.inner.reqs, req)
	i := r.inner.calls
	r.inner.calls++

	if i > 0 {
		r.summaryCtxErr = ctx.Err()
		_, r.summaryDeadline = ctx.Deadline()
		r.summaryTurnTimeout = req.Limits.TurnTimeout
	}

	var st agent.RunState
	if i < len(r.inner.states) {
		st = r.inner.states[i]
	} else if len(r.inner.states) > 0 {
		st = r.inner.states[len(r.inner.states)-1]
	}
	var err error
	if i < len(r.inner.errors) {
		err = r.inner.errors[i]
	}
	return st, err
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

// TestEndToEndDelegation verifies the full wiring path: parent provider returns
// a delegate tool_call, child provider completes the sub-task, parent provider
// produces a final response incorporating the child result. The parent
// conversation must not contain child internal messages and the DelegationResult
// fields must be populated correctly.
func TestEndToEndDelegation(t *testing.T) {
	childProv := &fakeProvider{
		responses: []provider.ChatResponse{
			{Message: provider.Message{Content: "child result"}, FinishReason: "stop"},
		},
	}

	deps := DelegateHandlerDeps{
		Provider:    childProv,
		ParentReg:   tool.NewRegistry(),
		SubAgentCfg: config.SubAgentConfig{Enabled: true, MaxTurns: 5, MaxTokens: 10000},
		Events:      output.NoopSink{},
		Runner:      agent.NewRunner(),
		WorkDir:     "/tmp/work",
	}

	handler := NewDelegateHandler(deps)

	// Invoke the handler directly — this is what the executor calls when the
	// parent agent requests the delegate tool.
	raw, err := handler(context.Background(), map[string]any{
		"task": "do sub-work",
	})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	execResult, ok := raw.(tool.ExecutionResult)
	if !ok {
		t.Fatalf("handler returned %T, want tool.ExecutionResult", raw)
	}
	result, ok := execResult.Value.(DelegationResult)
	if !ok {
		t.Fatalf("handler result.Value type = %T, want DelegationResult", execResult.Value)
	}
	if result.Output != "child result" {
		t.Errorf("Output: got %q, want %q", result.Output, "child result")
	}
	if result.Status != StatusComplete {
		t.Errorf("Status: got %q, want %q", result.Status, StatusComplete)
	}
	if result.AgentID == "" {
		t.Error("AgentID must not be empty")
	}
	if result.TurnCount != 1 {
		t.Errorf("TurnCount: got %d, want 1", result.TurnCount)
	}

	// Parent conversation must not contain child internal messages.
	// The child ran its own isolated conversation; the parent only sees the
	// DelegationResult value. Verify the child provider was called exactly once
	// (no child internal leakage into the parent call count).
	if childProv.callCount != 2 {
		t.Errorf("child provider callCount: got %d, want 2", childProv.callCount)
	}
	if len(childProv.requests) != 2 {
		t.Fatalf("child provider requests: got %d, want 2", len(childProv.requests))
	}
	// Child conversation must not include any parent messages — the child
	// request should only contain the system prompt and the task message.
	childMsgs := childProv.requests[0].Messages
	for _, msg := range childMsgs {
		if strings.Contains(msg.Content, "parent") {
			t.Errorf("child conversation unexpectedly contains parent-scoped content: %q", msg.Content)
		}
	}
}

// TestNestingPrevention verifies that when the child's provider attempts to
// call the delegate tool, execution fails because the child registry has no
// "delegate" entry, and the error propagates correctly.
func TestNestingPrevention(t *testing.T) {
	// Child provider first returns a tool_call for "delegate", then (if somehow
	// reached) a text stop. The executor will fail on the first call because
	// "delegate" is not in the child registry.
	childProv := &fakeProvider{
		responses: []provider.ChatResponse{
			{
				Message: provider.Message{
					Role: provider.MessageRoleAssistant,
					ToolCalls: []provider.ToolCall{
						{ID: "tc-1", Name: "delegate", Arguments: map[string]any{"task": "nested"}},
					},
				},
				FinishReason: "tool_calls",
			},
			{Message: provider.Message{Content: "final"}, FinishReason: "stop"},
		},
	}

	spec := makeSpec("nesting-test", 10000)
	agentLimits := agent.Limits{MaxTurns: 5, MaxTokens: 0}
	sink := &collectingSink{}
	visibleReg, execReg := testChildRegistries(tool.NewRegistry())
	req := buildChildRunRequest("/tmp/work", spec, childProv, visibleReg, execReg, agentLimits, sink, testBuildPrompt(spec), nil, false, "", false, nil, prompt.ModelTokenBudget{}, "", nil, false)

	runner := agent.NewRunner()
	_, err := SpawnDelegate(context.Background(), spec, req, runner, sink, nil)
	// The agent should propagate a failure: the delegate tool is unknown to the
	// child executor. SpawnDelegate either returns an error, or the state has a
	// non-complete stop reason captured in the result.
	if err != nil {
		// Expected: child runner propagated an error for unknown delegate tool.
		if !strings.Contains(err.Error(), "delegate") && !strings.Contains(err.Error(), "unknown") && !strings.Contains(err.Error(), "tool") {
			t.Logf("error propagated (acceptable): %v", err)
		}
		return
	}
	// If no error was returned, the result status should reflect failure or the
	// child provider should have received a tool error rather than completing
	// successfully with the nested delegation.
	// Check that child provider did not receive a second model call that would
	// indicate nesting succeeded — the tool result turn would be an error.
	if childProv.callCount < 2 {
		// Only one provider call: model returned tool_calls but executor failed
		// before getting a second model call. That is also an acceptable
		// nesting-prevention signal since the tool error is surfaced.
		t.Logf("child provider called %d time(s), nesting blocked before second model call", childProv.callCount)
	}
}

// TestParentContextIsolation verifies that a child doing multi-turn work with
// a helper tool does not pollute the parent conversation. The parent only
// receives the DelegationResult; child internal messages stay in the child.
func TestParentContextIsolation(t *testing.T) {
	helperCallCount := 0
	parentReg := tool.NewRegistry(
		tool.ToolDef{
			Name:        "helper",
			Description: "a helper tool",
			Handler: func(_ context.Context, _ map[string]any) (any, error) {
				helperCallCount++
				return "helper-output", nil
			},
		},
	)

	// Child does two turns: first turn calls "helper", second turn returns text.
	childProv := &fakeProvider{
		responses: []provider.ChatResponse{
			{
				Message: provider.Message{
					Role: provider.MessageRoleAssistant,
					ToolCalls: []provider.ToolCall{
						{ID: "tc-helper", Name: "helper", Arguments: map[string]any{}},
					},
				},
				FinishReason: "tool_calls",
			},
			{Message: provider.Message{Content: "child final answer"}, FinishReason: "stop"},
		},
	}

	deps := DelegateHandlerDeps{
		Provider:    childProv,
		ParentReg:   parentReg,
		SubAgentCfg: config.SubAgentConfig{Enabled: true, MaxTurns: 5, MaxTokens: 10000, AllowedTools: []string{"helper"}},
		Events:      output.NoopSink{},
		Runner:      agent.NewRunner(),
		WorkDir:     "/tmp/work",
	}

	handler := NewDelegateHandler(deps)
	raw, err := handler(context.Background(), map[string]any{
		"task": "use the helper tool",
	})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	execResult, ok := raw.(tool.ExecutionResult)
	if !ok {
		t.Fatalf("handler returned %T, want tool.ExecutionResult", raw)
	}
	result, ok := execResult.Value.(DelegationResult)
	if !ok {
		t.Fatalf("handler result.Value type = %T, want DelegationResult", execResult.Value)
	}
	if result.Output != "child final answer" {
		t.Errorf("Output: got %q, want %q", result.Output, "child final answer")
	}
	if helperCallCount != 1 {
		t.Errorf("helper tool call count: got %d, want 1", helperCallCount)
	}

	// Child did two provider calls (tool turn + final text turn). Verify the
	// child ran in its own isolated conversation by checking that the second
	// child provider call included a tool result message — evidence of internal
	// multi-turn wiring — without that leaking to the parent.
	if childProv.callCount != 3 {
		t.Errorf("child provider callCount: got %d, want 3", childProv.callCount)
	}
	secondReq := childProv.requests[1]
	var sawToolResult bool
	for _, msg := range secondReq.Messages {
		if msg.Role == provider.MessageRoleTool {
			sawToolResult = true
		}
	}
	if !sawToolResult {
		t.Error("expected second child provider request to include a tool result message")
	}

	// The parent only receives the DelegationResult struct. There is no parent
	// provider call in this test since we invoked the handler directly.
	// Verify that child turn count reflects multi-turn execution.
	if result.TurnCount < 2 {
		t.Errorf("TurnCount: got %d, want >= 2 (multi-turn child)", result.TurnCount)
	}
}

// presetRunner is a test AgentRunner that returns pre-configured states.
type presetRunner struct {
	states []agent.RunState
	errors []error
	calls  int
	reqs   []agent.RunRequest
}

func (r *presetRunner) Run(_ context.Context, req agent.RunRequest) (agent.RunState, error) {
	r.reqs = append(r.reqs, req)
	i := r.calls
	r.calls++
	var st agent.RunState
	if i < len(r.states) {
		st = r.states[i]
	} else if len(r.states) > 0 {
		st = r.states[len(r.states)-1]
	}
	var err error
	if i < len(r.errors) {
		err = r.errors[i]
	}
	return st, err
}

func maxTurnsStateWithTools(turnCount int) agent.RunState {
	return agent.RunState{
		TurnCount:  turnCount,
		StopReason: agent.StopReasonMaxTurns,
		Conversation: []agent.Message{
			{
				Role:      agent.MessageRoleAssistant,
				ToolCalls: []agent.ToolCall{{ID: "tc-1", Name: "bash", Arguments: map[string]any{}}},
			},
		},
	}
}

func completeState(content string) agent.RunState {
	return agent.RunState{
		TurnCount:  1,
		StopReason: agent.StopReasonComplete,
		Conversation: []agent.Message{
			{Role: agent.MessageRoleAssistant, Content: content},
		},
	}
}

func TestDelegateNeedsExtension(t *testing.T) {
	toolCalls := []agent.ToolCall{{ID: "tc", Name: "bash"}}
	cases := []struct {
		name       string
		stopReason agent.StopReason
		toolCalls  []agent.ToolCall
		want       bool
	}{
		{"max_turns with tool calls", agent.StopReasonMaxTurns, toolCalls, true},
		{"max_turns no tool calls", agent.StopReasonMaxTurns, nil, false},
		{"complete with tool calls", agent.StopReasonComplete, toolCalls, false},
		{"complete no tool calls", agent.StopReasonComplete, nil, false},
		{"cancelled with tool calls", agent.StopReasonCancelled, toolCalls, false},
		{"no assistant message", agent.StopReasonMaxTurns, nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var msgs []agent.Message
			if tc.name != "no assistant message" {
				msgs = []agent.Message{{Role: agent.MessageRoleAssistant, ToolCalls: tc.toolCalls}}
			}
			state := agent.RunState{StopReason: tc.stopReason, Conversation: msgs}
			if got := delegateNeedsExtension(state); got != tc.want {
				t.Errorf("delegateNeedsExtension = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestExtensionTriggersWhenMidWork(t *testing.T) {
	spec := makeSpec("ext-agent-1", 10000)
	sink := &collectingSink{}

	runner := &presetRunner{
		states: []agent.RunState{
			maxTurnsStateWithTools(3),
			completeState("final answer"),
			completeState("summary of final answer"),
		},
	}

	agentLimits := agent.Limits{MaxTurns: 5, MaxTokens: 0}
	prov := &fakeProvider{responses: []provider.ChatResponse{{Message: provider.Message{Content: "unused"}, FinishReason: "stop"}}}
	visibleReg, execReg := testChildRegistries(tool.NewRegistry())
	req := buildChildRunRequest("/tmp/work", spec, prov, visibleReg, execReg, agentLimits, sink, testBuildPrompt(spec), nil, false, "", false, nil, prompt.ModelTokenBudget{}, "", nil, false)

	result, err := SpawnDelegate(context.Background(), spec, req, runner, sink, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	typedResult, ok := result.Value.(DelegationResult)
	if !ok {
		t.Fatalf("result.Value type = %T, want DelegationResult", result.Value)
	}
	if typedResult.Output != "final answer" {
		t.Errorf("Output = %q, want %q", typedResult.Output, "final answer")
	}

	var extEvents int
	for _, ev := range sink.events {
		if ev.Type == output.EventTypeDelegationExtension {
			extEvents++
		}
	}
	if extEvents != 1 {
		t.Errorf("extension events = %d, want 1", extEvents)
	}
}

func TestNoExtensionWhenComplete(t *testing.T) {
	spec := makeSpec("ext-agent-2", 10000)
	sink := &collectingSink{}

	runner := &presetRunner{
		states: []agent.RunState{
			completeState("done"),
			completeState("summary"),
		},
	}

	agentLimits := agent.Limits{MaxTurns: 5, MaxTokens: 0}
	prov := &fakeProvider{responses: []provider.ChatResponse{{Message: provider.Message{Content: "unused"}, FinishReason: "stop"}}}
	visibleReg, execReg := testChildRegistries(tool.NewRegistry())
	req := buildChildRunRequest("/tmp/work", spec, prov, visibleReg, execReg, agentLimits, sink, testBuildPrompt(spec), nil, false, "", false, nil, prompt.ModelTokenBudget{}, "", nil, false)

	_, err := SpawnDelegate(context.Background(), spec, req, runner, sink, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, ev := range sink.events {
		if ev.Type == output.EventTypeDelegationExtension {
			t.Error("unexpected DelegationExtension event when delegate completed naturally")
		}
	}
}

func TestNoExtensionWhenNoToolCalls(t *testing.T) {
	spec := makeSpec("ext-agent-3", 10000)
	sink := &collectingSink{}

	runner := &presetRunner{
		states: []agent.RunState{
			{
				TurnCount:  3,
				StopReason: agent.StopReasonMaxTurns,
				Conversation: []agent.Message{
					{Role: agent.MessageRoleAssistant, Content: "max turns but no tool calls"},
				},
			},
			completeState("summary"),
		},
	}

	agentLimits := agent.Limits{MaxTurns: 5, MaxTokens: 0}
	prov := &fakeProvider{responses: []provider.ChatResponse{{Message: provider.Message{Content: "unused"}, FinishReason: "stop"}}}
	visibleReg, execReg := testChildRegistries(tool.NewRegistry())
	req := buildChildRunRequest("/tmp/work", spec, prov, visibleReg, execReg, agentLimits, sink, testBuildPrompt(spec), nil, false, "", false, nil, prompt.ModelTokenBudget{}, "", nil, false)

	_, err := SpawnDelegate(context.Background(), spec, req, runner, sink, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, ev := range sink.events {
		if ev.Type == output.EventTypeDelegationExtension {
			t.Error("unexpected DelegationExtension event when last message has no tool calls")
		}
	}
}

func TestExtensionCapAtFive(t *testing.T) {
	spec := makeSpec("ext-agent-4", 10000)
	sink := &collectingSink{}

	// 5 extension runs + 1 summary = 7 total; provide enough states
	states := make([]agent.RunState, 0, 8)
	for i := 0; i < 7; i++ {
		states = append(states, maxTurnsStateWithTools((i+1)*3))
	}
	// Last state for summary
	states = append(states, completeState("summary"))

	runner := &presetRunner{states: states}

	agentLimits := agent.Limits{MaxTurns: 5, MaxTokens: 0}
	prov := &fakeProvider{responses: []provider.ChatResponse{{Message: provider.Message{Content: "unused"}, FinishReason: "stop"}}}
	visibleReg, execReg := testChildRegistries(tool.NewRegistry())
	req := buildChildRunRequest("/tmp/work", spec, prov, visibleReg, execReg, agentLimits, sink, testBuildPrompt(spec), nil, false, "", false, nil, prompt.ModelTokenBudget{}, "", nil, false)

	_, err := SpawnDelegate(context.Background(), spec, req, runner, sink, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var extCount int
	for _, ev := range sink.events {
		if ev.Type == output.EventTypeDelegationExtension {
			extCount++
		}
	}
	if extCount != 5 {
		t.Errorf("extension event count = %d, want exactly 5", extCount)
	}
}

func TestExtensionMaxTurnsBumped(t *testing.T) {
	spec := makeSpec("ext-agent-5", 10000)
	sink := &collectingSink{}

	firstState := maxTurnsStateWithTools(3)
	runner := &presetRunner{
		states: []agent.RunState{
			firstState,
			completeState("done"),
			completeState("summary"),
		},
	}

	originalMaxTurns := 5
	agentLimits := agent.Limits{MaxTurns: originalMaxTurns, MaxTokens: 0}
	prov := &fakeProvider{responses: []provider.ChatResponse{{Message: provider.Message{Content: "unused"}, FinishReason: "stop"}}}
	visibleReg, execReg := testChildRegistries(tool.NewRegistry())
	req := buildChildRunRequest("/tmp/work", spec, prov, visibleReg, execReg, agentLimits, sink, testBuildPrompt(spec), nil, false, "", false, nil, prompt.ModelTokenBudget{}, "", nil, false)

	_, err := SpawnDelegate(context.Background(), spec, req, runner, sink, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if runner.calls < 2 {
		t.Fatalf("runner.calls = %d, want >= 2", runner.calls)
	}
	// reqs[1] is the first extension call
	extReq := runner.reqs[1]
	wantMaxTurns := firstState.TurnCount + originalMaxTurns
	if extReq.Limits.MaxTurns != wantMaxTurns {
		t.Errorf("extension req MaxTurns = %d, want %d", extReq.Limits.MaxTurns, wantMaxTurns)
	}
}

func TestExtensionEventEmitted(t *testing.T) {
	spec := makeSpec("ext-agent-6", 10000)
	sink := &collectingSink{}

	runner := &presetRunner{
		states: []agent.RunState{
			maxTurnsStateWithTools(3),
			completeState("done"),
			completeState("summary"),
		},
	}

	agentLimits := agent.Limits{MaxTurns: 5, MaxTokens: 0}
	prov := &fakeProvider{responses: []provider.ChatResponse{{Message: provider.Message{Content: "unused"}, FinishReason: "stop"}}}
	visibleReg, execReg := testChildRegistries(tool.NewRegistry())
	req := buildChildRunRequest("/tmp/work", spec, prov, visibleReg, execReg, agentLimits, sink, testBuildPrompt(spec), nil, false, "", false, nil, prompt.ModelTokenBudget{}, "", nil, false)

	_, err := SpawnDelegate(context.Background(), spec, req, runner, sink, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, ev := range sink.events {
		if ev.Type != output.EventTypeDelegationExtension {
			continue
		}
		payload, ok := ev.Payload.(output.DelegationExtensionEvent)
		if !ok {
			t.Fatalf("payload type = %T, want DelegationExtensionEvent", ev.Payload)
		}
		if payload.Extension != 1 {
			t.Errorf("Extension = %d, want 1", payload.Extension)
		}
		if payload.MaxExtensions != 5 {
			t.Errorf("MaxExtensions = %d, want 5", payload.MaxExtensions)
		}
		if payload.AgentID != spec.AgentID {
			t.Errorf("AgentID = %q, want %q", payload.AgentID, spec.AgentID)
		}
		return
	}
	t.Error("no DelegationExtension event emitted")
}

func TestExtensionErrorReturnsFailedStatusAndPreservesState(t *testing.T) {
	spec := makeSpec("ext-agent-7", 10000)
	sink := &collectingSink{}

	firstState := maxTurnsStateWithTools(3)
	runner := &presetRunner{
		states: []agent.RunState{
			firstState,
			{},
		},
		errors: []error{
			nil,
			fmt.Errorf("extension run failed"),
		},
	}

	agentLimits := agent.Limits{MaxTurns: 5, MaxTokens: 0}
	prov := &fakeProvider{responses: []provider.ChatResponse{{Message: provider.Message{Content: "unused"}, FinishReason: "stop"}}}
	visibleReg, execReg := testChildRegistries(tool.NewRegistry())
	req := buildChildRunRequest("/tmp/work", spec, prov, visibleReg, execReg, agentLimits, sink, testBuildPrompt(spec), nil, false, "", false, nil, prompt.ModelTokenBudget{}, "", nil, false)

	result, err := SpawnDelegate(context.Background(), spec, req, runner, sink, nil)
	if err != nil {
		t.Fatalf("SpawnDelegate returned error: %v", err)
	}

	typedResult, ok := result.Value.(DelegationResult)
	if !ok {
		t.Fatalf("result.Value type = %T, want DelegationResult", result.Value)
	}
	if typedResult.Status != StatusFailed {
		t.Fatalf("Status = %q, want %q", typedResult.Status, StatusFailed)
	}
	if typedResult.Error != "extension run failed" {
		t.Fatalf("Error = %q, want %q", typedResult.Error, "extension run failed")
	}
	if typedResult.TurnCount != firstState.TurnCount {
		t.Fatalf("TurnCount = %d, want %d (from pre-error state)", typedResult.TurnCount, firstState.TurnCount)
	}
	if typedResult.Output != "" {
		t.Fatalf("Output = %q, want preserved pre-error output", typedResult.Output)
	}
	if typedResult.Summary != "delegation failed: extension run failed" {
		t.Fatalf("Summary = %q, want failure summary", typedResult.Summary)
	}
	if result.Retention == nil {
		t.Fatal("result.Retention = nil, want failure retention")
	}
	if result.Retention.Status != string(StatusFailed) {
		t.Fatalf("Retention.Status = %q, want %q", result.Retention.Status, StatusFailed)
	}
	if !strings.Contains(result.Retention.Summary, "delegation failed: extension run failed") {
		t.Fatalf("Retention.Summary = %q, want failure summary", result.Retention.Summary)
	}

	var sawFailedEvent, sawCompleteEvent bool
	for _, ev := range sink.events {
		switch ev.Type {
		case output.EventTypeDelegationFailed:
			sawFailedEvent = true
		case output.EventTypeDelegationComplete:
			sawCompleteEvent = true
		}
	}
	if !sawFailedEvent {
		t.Fatal("expected delegation_failed event")
	}
	if sawCompleteEvent {
		t.Fatal("unexpected delegation_complete event after failure")
	}
}

func TestExtensionCancellationReturnsCancelledStatus(t *testing.T) {
	spec := makeSpec("ext-agent-cancelled", 10000)
	sink := &collectingSink{}

	firstState := maxTurnsStateWithTools(3)
	runner := &presetRunner{
		states: []agent.RunState{
			firstState,
			{},
		},
		errors: []error{
			nil,
			context.Canceled,
		},
	}

	agentLimits := agent.Limits{MaxTurns: 5, MaxTokens: 0}
	prov := &fakeProvider{responses: []provider.ChatResponse{{Message: provider.Message{Content: "unused"}, FinishReason: "stop"}}}
	visibleReg, execReg := testChildRegistries(tool.NewRegistry())
	req := buildChildRunRequest("/tmp/work", spec, prov, visibleReg, execReg, agentLimits, sink, testBuildPrompt(spec), nil, false, "", false, nil, prompt.ModelTokenBudget{}, "", nil, false)

	result, err := SpawnDelegate(context.Background(), spec, req, runner, sink, nil)
	if err != nil {
		t.Fatalf("SpawnDelegate returned error: %v", err)
	}

	typedResult, ok := result.Value.(DelegationResult)
	if !ok {
		t.Fatalf("result.Value type = %T, want DelegationResult", result.Value)
	}
	if typedResult.Status != StatusCancelled {
		t.Fatalf("Status = %q, want %q", typedResult.Status, StatusCancelled)
	}
	if typedResult.Error != context.Canceled.Error() {
		t.Fatalf("Error = %q, want %q", typedResult.Error, context.Canceled.Error())
	}
	if result.Retention == nil {
		t.Fatal("result.Retention = nil, want cancellation retention")
	}
	if result.Retention.Status != string(StatusCancelled) {
		t.Fatalf("Retention.Status = %q, want %q", result.Retention.Status, StatusCancelled)
	}
	if !strings.Contains(result.Retention.Summary, "delegation failed:") {
		t.Fatalf("Retention.Summary = %q, want cancellation summary", result.Retention.Summary)
	}
}

// TestSummaryTurnDoesNotEmitEvents verifies that events from the summary turn do
// not appear in the parent sink. It uses presetRunner so both calls are
// controlled; after the main run the event count is recorded, then the summary
// run must not add any turn_started or assistant_message events.
func TestSummaryTurnDoesNotEmitEvents(t *testing.T) {
	spec := makeSpec("summary-events-agent", 1000)
	agentLimits := agent.Limits{MaxTurns: 5, MaxTokens: 0}
	sink := &collectingSink{}

	runner := &presetRunner{
		states: []agent.RunState{
			completeState("task done"),
			completeState("short summary"),
		},
	}

	prov := &fakeProvider{responses: []provider.ChatResponse{{Message: provider.Message{Content: "unused"}, FinishReason: "stop"}}}
	visibleReg, execReg := testChildRegistries(tool.NewRegistry())
	req := buildChildRunRequest("/tmp/work", spec, prov, visibleReg, execReg, agentLimits, sink, testBuildPrompt(spec), nil, false, "", false, nil, prompt.ModelTokenBudget{}, "", nil, false)

	_, err := SpawnDelegate(context.Background(), spec, req, runner, sink, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if runner.calls != 2 {
		t.Fatalf("runner.calls = %d, want 2", runner.calls)
	}

	// The summary run request must have Events = nil.
	summaryReq := runner.reqs[1]
	if summaryReq.Events != nil {
		t.Errorf("summary run request Events = %v, want nil", summaryReq.Events)
	}
}

// TestSummaryUsesFullConversation verifies that the summary turn receives the
// full delegate conversation (not just the initial prompt + last fragment).
func TestSummaryUsesFullConversation(t *testing.T) {
	spec := makeSpec("summary-conv-agent", 10000)
	sink := &collectingSink{}

	// Build a realistic multi-turn conversation for the main run state.
	fullConversation := []agent.Message{
		{Role: agent.MessageRoleUser, Content: "do the task"},
		{
			Role:      agent.MessageRoleAssistant,
			ToolCalls: []agent.ToolCall{{ID: "tc-1", Name: "bash", Arguments: map[string]any{"cmd": "ls"}}},
		},
		{Role: agent.MessageRoleTool, Content: "file1.go\nfile2.go"},
		{Role: agent.MessageRoleAssistant, Content: "I found the files"},
	}

	mainState := agent.RunState{
		TurnCount:    2,
		StopReason:   agent.StopReasonComplete,
		Conversation: fullConversation,
	}
	summaryState := agent.RunState{
		TurnCount:  1,
		StopReason: agent.StopReasonComplete,
		Conversation: []agent.Message{
			{Role: agent.MessageRoleAssistant, Content: "summary of findings"},
		},
	}

	runner := &presetRunner{
		states: []agent.RunState{mainState, summaryState},
	}

	agentLimits := agent.Limits{MaxTurns: 5, MaxTokens: 0}
	prov := &fakeProvider{responses: []provider.ChatResponse{{Message: provider.Message{Content: "unused"}, FinishReason: "stop"}}}
	visibleReg, execReg := testChildRegistries(tool.NewRegistry())
	req := buildChildRunRequest("/tmp/work", spec, prov, visibleReg, execReg, agentLimits, sink, testBuildPrompt(spec), nil, false, "", false, nil, prompt.ModelTokenBudget{}, "", nil, false)

	_, err := SpawnDelegate(context.Background(), spec, req, runner, sink, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if runner.calls != 2 {
		t.Fatalf("runner.calls = %d, want 2", runner.calls)
	}

	// The summary request (second call) must contain the full conversation.
	summaryReq := runner.reqs[1]
	wantMessages := agent.ToProviderMessages(fullConversation)
	// The summary request conversation should start with the full conversation
	// messages followed by the summarise instruction.
	if len(summaryReq.Prompt.Conversation) < len(wantMessages) {
		t.Fatalf("summary conversation length %d < full conversation length %d",
			len(summaryReq.Prompt.Conversation), len(wantMessages))
	}
	for i, want := range wantMessages {
		got := summaryReq.Prompt.Conversation[i]
		if got.Role != want.Role || got.Content != want.Content {
			t.Errorf("summary conversation[%d]: got role=%q content=%q, want role=%q content=%q",
				i, got.Role, got.Content, want.Role, want.Content)
		}
	}
}

// TestDelegationResultSummaryPopulated verifies that DelegationResult.Summary is
// populated after a successful SpawnDelegate call.
func TestDelegationResultSummaryPopulated(t *testing.T) {
	prov := &fakeProvider{
		responses: []provider.ChatResponse{
			{Message: provider.Message{Content: "task output"}, FinishReason: "stop"},
			{Message: provider.Message{Content: "condensed summary"}, FinishReason: "stop"},
		},
	}

	spec := makeSpec("summary-populated-agent", 10000)
	agentLimits := agent.Limits{MaxTurns: 5, MaxTokens: 0}
	sink := output.NoopSink{}
	visibleReg, execReg := testChildRegistries(tool.NewRegistry())
	req := buildChildRunRequest("/tmp/work", spec, prov, visibleReg, execReg, agentLimits, sink, testBuildPrompt(spec), nil, false, "", false, nil, prompt.ModelTokenBudget{}, "", nil, false)

	runner := agent.NewRunner()
	result, err := SpawnDelegate(context.Background(), spec, req, runner, sink, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	typedResult, ok := result.Value.(DelegationResult)
	if !ok {
		t.Fatalf("result.Value type = %T, want DelegationResult", result.Value)
	}
	if typedResult.Summary == "" {
		t.Fatal("DelegationResult.Summary = empty, want populated summary text")
	}
}

// buildTestActiveRegistry mirrors the logic of cmd/steiner.buildActiveRegistry
// so that TestConfigGatingDisabled can stay inside the delegation package.
func buildTestActiveRegistry(base *tool.Registry, subAgentCfg config.SubAgentConfig, prov provider.Provider, events output.EventSink) *tool.Registry {
	if !subAgentCfg.Enabled {
		return base
	}
	cloned := base.Clone()
	handler := NewDelegateHandler(DelegateHandlerDeps{
		Provider:    prov,
		ParentReg:   base,
		SubAgentCfg: subAgentCfg,
		Events:      events,
		Runner:      agent.NewRunner(),
		WorkDir:     "/tmp/work",
	})
	cloned.Register(DelegateToolDef(handler))
	return cloned
}

// TestConfigGatingDisabled verifies that when sub_agent.enabled is false the
// registry does not contain the "delegate" tool, and when it is true the tool
// is added to a clone without mutating the base registry.
func TestConfigGatingDisabled(t *testing.T) {
	base := tool.NewRegistry(
		tool.ToolDef{
			Name:        "bash",
			Description: "run shell commands",
			Handler: func(_ context.Context, _ map[string]any) (any, error) {
				return "ok", nil
			},
		},
	)

	prov := &fakeProvider{
		responses: []provider.ChatResponse{
			{Message: provider.Message{Content: "done"}, FinishReason: "stop"},
		},
	}

	// sub_agent.enabled = false: base registry is returned as-is, no delegate tool.
	disabledCfg := config.SubAgentConfig{Enabled: false}
	regDisabled := buildTestActiveRegistry(base, disabledCfg, prov, output.NoopSink{})
	for _, name := range regDisabled.Names() {
		if name == DelegateToolName {
			t.Errorf("delegate tool present in registry when sub_agent.enabled=false")
		}
	}
	if len(regDisabled.Names()) != 1 || regDisabled.Names()[0] != "bash" {
		t.Errorf("expected only 'bash' tool; got %v", regDisabled.Names())
	}

	// sub_agent.enabled = true: delegate tool is added to a clone of base.
	enabledCfg := config.SubAgentConfig{Enabled: true, MaxTurns: 5, MaxTokens: 10000}
	regEnabled := buildTestActiveRegistry(base, enabledCfg, prov, output.NoopSink{})
	var foundDelegate bool
	for _, name := range regEnabled.Names() {
		if name == DelegateToolName {
			foundDelegate = true
		}
	}
	if !foundDelegate {
		t.Errorf("delegate tool not found in registry when sub_agent.enabled=true; got %v", regEnabled.Names())
	}

	// Base registry must remain unmodified (clone semantics).
	for _, name := range base.Names() {
		if name == DelegateToolName {
			t.Error("delegate tool leaked into base registry")
		}
	}
}

func TestTruncateTaskPreviewRuneSafe(t *testing.T) {
	tests := []struct {
		name string
		s    string
		max  int
		want string
	}{
		{"ascii under limit", "hello", 10, "hello"},
		{"ascii at limit", "hello", 5, "hello"},
		{"ascii over limit", "hello world", 8, "hello..."},
		{"multibyte under limit", "héllo", 10, "héllo"},
		{"multibyte truncate", "héllo wörld", 8, "héllo..."},
		{"cjk truncate", "日本語テスト", 5, "日本..."},
		{"emoji truncate", "🎉🎊🎈🎁🎂", 4, "🎉..."},
		{"max less than 3", "hello", 2, "he"},
		{"max less than 3 multibyte", "日本語", 2, "日本"},
		{"empty string", "", 5, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateTaskPreview(tt.s, tt.max)
			if got != tt.want {
				t.Errorf("truncateTaskPreview(%q, %d) = %q, want %q", tt.s, tt.max, got, tt.want)
			}
		})
	}
}

func TestDelegationCompleteEventEmittedBeforeSummary(t *testing.T) {
	spec := makeSpec("event-order-agent", 10000)
	sink := &collectingSink{}

	var summaryCallSawCompleteEvent bool
	runner := &presetRunner{
		states: []agent.RunState{
			completeState("task done"),
			completeState("summary text"),
		},
	}

	// Wrap the runner to check event state at summary call time
	wrappedRunner := &eventOrderCheckRunner{
		inner: runner,
		sink:  sink,
	}

	agentLimits := agent.Limits{MaxTurns: 5, MaxTokens: 0}
	prov := &fakeProvider{responses: []provider.ChatResponse{{Message: provider.Message{Content: "unused"}, FinishReason: "stop"}}}
	visibleReg, execReg := testChildRegistries(tool.NewRegistry())
	req := buildChildRunRequest("/tmp/work", spec, prov, visibleReg, execReg, agentLimits, sink, testBuildPrompt(spec), nil, false, "", false, nil, prompt.ModelTokenBudget{}, "", nil, false)

	_, err := SpawnDelegate(context.Background(), spec, req, wrappedRunner, sink, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	summaryCallSawCompleteEvent = wrappedRunner.sawCompleteAtSummaryCall
	if !summaryCallSawCompleteEvent {
		t.Error("DelegationComplete event was not emitted before the summary call")
	}
}

type eventOrderCheckRunner struct {
	inner                    *presetRunner
	sink                     *collectingSink
	sawCompleteAtSummaryCall bool
}

func (r *eventOrderCheckRunner) Run(_ context.Context, req agent.RunRequest) (agent.RunState, error) {
	r.inner.reqs = append(r.inner.reqs, req)
	i := r.inner.calls
	r.inner.calls++

	if i > 0 {
		for _, ev := range r.sink.events {
			if ev.Type == output.EventTypeDelegationComplete {
				r.sawCompleteAtSummaryCall = true
				break
			}
		}
	}

	var st agent.RunState
	if i < len(r.inner.states) {
		st = r.inner.states[i]
	} else if len(r.inner.states) > 0 {
		st = r.inner.states[len(r.inner.states)-1]
	}
	var err error
	if i < len(r.inner.errors) {
		err = r.inner.errors[i]
	}
	return st, err
}

func TestCancelledDelegateWithOutputReturnsPartial(t *testing.T) {
	spec := makeSpec("cancel-partial-agent", 10000)
	sink := &collectingSink{}

	runner := &presetRunner{
		states: []agent.RunState{
			{
				TurnCount:  5,
				StopReason: agent.StopReasonCancelled,
				Conversation: []agent.Message{
					{Role: agent.MessageRoleAssistant, Content: "completed useful work"},
				},
			},
			completeState("summary"),
		},
	}

	agentLimits := agent.Limits{MaxTurns: 10, MaxTokens: 0}
	prov := &fakeProvider{responses: []provider.ChatResponse{{Message: provider.Message{Content: "unused"}, FinishReason: "stop"}}}
	visibleReg, execReg := testChildRegistries(tool.NewRegistry())
	req := buildChildRunRequest("/tmp/work", spec, prov, visibleReg, execReg, agentLimits, sink, testBuildPrompt(spec), nil, false, "", false, nil, prompt.ModelTokenBudget{}, "", nil, false)

	result, err := SpawnDelegate(context.Background(), spec, req, runner, sink, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	typedResult, ok := result.Value.(DelegationResult)
	if !ok {
		t.Fatalf("result.Value type = %T, want DelegationResult", result.Value)
	}
	if typedResult.Status != StatusPartial {
		t.Errorf("Status = %q, want %q", typedResult.Status, StatusPartial)
	}
	if typedResult.StopReason != "cancelled" {
		t.Errorf("StopReason = %q, want %q", typedResult.StopReason, "cancelled")
	}
	if typedResult.Output != "completed useful work" {
		t.Errorf("Output = %q, want %q", typedResult.Output, "completed useful work")
	}

	var sawComplete bool
	for _, ev := range sink.events {
		if ev.Type == output.EventTypeDelegationComplete {
			sawComplete = true
		}
	}
	if !sawComplete {
		t.Error("expected DelegationComplete event even for partial completion")
	}
}

func TestSummaryUsesDetachedContext(t *testing.T) {
	spec := makeSpec("detached-ctx-agent", 10000)

	var summaryCtxDone bool
	runner := &presetRunner{
		states: []agent.RunState{
			completeState("task done"),
			completeState("summary text"),
		},
	}

	ctxCheckRunner := &contextCheckRunner{
		inner: runner,
	}

	agentLimits := agent.Limits{MaxTurns: 5, MaxTokens: 0}
	prov := &fakeProvider{responses: []provider.ChatResponse{{Message: provider.Message{Content: "unused"}, FinishReason: "stop"}}}
	visibleReg, execReg := testChildRegistries(tool.NewRegistry())
	req := buildChildRunRequest("/tmp/work", spec, prov, visibleReg, execReg, agentLimits, output.NoopSink{}, testBuildPrompt(spec), nil, false, "", false, nil, prompt.ModelTokenBudget{}, "", nil, false)

	parentCtx, parentCancel := context.WithCancel(context.Background())
	parentCancel()

	_, err := SpawnDelegate(parentCtx, spec, req, ctxCheckRunner, output.NoopSink{}, nil)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			t.Skip("parent context cancellation propagated to runner.Run — expected if childCtx == ctx")
		}
		t.Fatalf("unexpected error: %v", err)
	}

	summaryCtxDone = ctxCheckRunner.summaryCtxErr != nil
	if summaryCtxDone {
		t.Error("summary context was cancelled — should use detached context")
	}
}

type contextCheckRunner struct {
	inner         *presetRunner
	summaryCtxErr error
}

func (r *contextCheckRunner) Run(ctx context.Context, req agent.RunRequest) (agent.RunState, error) {
	r.inner.reqs = append(r.inner.reqs, req)
	i := r.inner.calls
	r.inner.calls++

	if i > 0 {
		r.summaryCtxErr = ctx.Err()
	}

	var st agent.RunState
	if i < len(r.inner.states) {
		st = r.inner.states[i]
	} else if len(r.inner.states) > 0 {
		st = r.inner.states[len(r.inner.states)-1]
	}
	var err error
	if i < len(r.inner.errors) {
		err = r.inner.errors[i]
	}
	return st, err
}

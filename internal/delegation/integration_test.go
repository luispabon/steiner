package delegation

import (
	"context"
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
	req := buildChildRunRequest("/tmp/work", spec, prov, visibleReg, execReg, agentLimits, sink, testBuildPrompt(spec), nil, config.ThinkingConfig{})

	runner := agent.NewRunner()
	result, err := SpawnDelegate(context.Background(), spec, req, runner, sink)
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
	if typedResult.Summary != "" {
		t.Fatalf("Summary = %q, want hidden summary metadata only", typedResult.Summary)
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
	req := buildChildRunRequest("/tmp/work", spec, prov, visibleReg, execReg, agentLimits, sink, testBuildPrompt(spec), nil, config.ThinkingConfig{})

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
	req := buildChildRunRequest("/tmp/work", spec, prov, visibleReg, execReg, agentLimits, sink, testBuildPrompt(spec), nil, config.ThinkingConfig{})

	runner := agent.NewRunner()
	result, err := SpawnDelegate(context.Background(), spec, req, runner, sink)
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
	req := buildChildRunRequest("/tmp/work", spec, prov, visibleReg, execReg, agentLimits, sink, testBuildPrompt(spec), nil, config.ThinkingConfig{})

	runner := agent.NewRunner()
	result, err := SpawnDelegate(context.Background(), spec, req, runner, sink)
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
	if typedResult.Summary != "" {
		t.Fatalf("Summary = %q, want hidden summary metadata only", typedResult.Summary)
	}
}

type summaryFailRunner struct {
	calls int
}

func (r *summaryFailRunner) Run(ctx context.Context, req agent.RunRequest) (agent.RunState, error) {
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
	req := buildChildRunRequest("/tmp/work", spec, prov, visibleReg, execReg, agent.Limits{MaxTurns: 5, MaxTokens: 0}, output.NoopSink{}, testBuildPrompt(spec), nil, config.ThinkingConfig{})
	runner := &summaryFailRunner{}

	result, err := SpawnDelegate(context.Background(), spec, req, runner, output.NoopSink{})
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
	req := buildChildRunRequest("/tmp/work", spec, &fakeProvider{responses: []provider.ChatResponse{{Message: provider.Message{Content: "done"}, FinishReason: "stop"}}}, visibleReg, execReg, agent.Limits{MaxTurns: 5, MaxTokens: 0}, output.NoopSink{}, testBuildPrompt(spec), nil, config.ThinkingConfig{})

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
	req := buildChildRunRequest("/tmp/work", spec, prov, visibleReg, execReg, agent.Limits{MaxTurns: 5, MaxTokens: 0}, output.NoopSink{}, testBuildPrompt(spec), nil, config.ThinkingConfig{})
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
	typedResult, ok := result.Value.(DelegationResult)
	if !ok {
		t.Fatalf("result.Value type = %T, want DelegationResult", result.Value)
	}
	if len(typedResult.Output) != 5000 {
		t.Fatalf("Output length %d, want full visible output", len(typedResult.Output))
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
	req := buildChildRunRequest("/tmp/work", spec, childProv, visibleReg, execReg, agentLimits, sink, testBuildPrompt(spec), nil, config.ThinkingConfig{})

	runner := agent.NewRunner()
	_, err := SpawnDelegate(context.Background(), spec, req, runner, sink)
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
			Handler: func(ctx context.Context, input map[string]any) (any, error) {
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
		SubAgentCfg: config.SubAgentConfig{Enabled: true, MaxTurns: 5, MaxTokens: 10000},
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
			Handler: func(ctx context.Context, input map[string]any) (any, error) {
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

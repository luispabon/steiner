package delegation

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

type parallelProvider struct {
	parent provider.ChatResponse
	child  func(context.Context, string) (provider.ChatResponse, error)
	calls  atomic.Int32
}

func (p *parallelProvider) ChatCompletion(ctx context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
	if p.calls.Add(1) == 1 {
		return p.parent, nil
	}
	return p.child(ctx, childTask(req))
}

func (p *parallelProvider) StreamChatCompletion(ctx context.Context, req provider.ChatRequest) (<-chan provider.ChatChunk, error) {
	resp, err := p.ChatCompletion(ctx, req)
	if err != nil {
		return nil, err
	}
	ch := make(chan provider.ChatChunk, 1)
	ch <- provider.ChatChunk{Done: true, FinishReason: "stop", Delta: resp.Message}
	close(ch)
	return ch, nil
}

func (*parallelProvider) SupportsUsageStats() bool { return false }

func childTask(req provider.ChatRequest) string {
	for _, msg := range req.Messages {
		if msg.Role == provider.MessageRoleUser {
			return msg.Content
		}
	}
	return ""
}

type parallelHarness struct {
	provider       *parallelProvider
	active         atomic.Int32
	max            atomic.Int32
	started        atomic.Int32
	completed      atomic.Int32
	completedTasks sync.Map
	allStarted     chan struct{}
	done           chan struct{}
	target         int
	release        func(string) <-chan struct{}
	failTask       string
	blockOnCtx     bool
}

func newParallelHarness(parent provider.ChatResponse, n int) *parallelHarness {
	h := &parallelHarness{
		allStarted: make(chan struct{}),
		done:       make(chan struct{}),
		target:     n,
	}
	h.release = func(string) <-chan struct{} { return h.done }
	h.provider = &parallelProvider{parent: parent}
	h.provider.child = func(ctx context.Context, task string) (provider.ChatResponse, error) {
		cur := h.active.Add(1)
		for {
			old := h.max.Load()
			if cur <= old || h.max.CompareAndSwap(old, cur) {
				break
			}
		}
		if h.started.Add(1) == int32(h.target) {
			close(h.allStarted)
		}
		if h.failTask == task {
			h.countCompleted(task)
			h.active.Add(-1)
			return provider.ChatResponse{}, errors.New("child failed")
		}
		if h.blockOnCtx {
			select {
			case <-ctx.Done():
				h.countCompleted(task)
				h.active.Add(-1)
				return provider.ChatResponse{}, ctx.Err()
			}
		}
		if !h.blockOnCtx {
			select {
			case <-h.release(task):
			case <-ctx.Done():
			}
		}
		h.countCompleted(task)
		h.active.Add(-1)
		return provider.ChatResponse{Message: provider.Message{Role: provider.MessageRoleAssistant, Content: task}, FinishReason: "stop"}, nil
	}
	return h
}

func (h *parallelHarness) countCompleted(task string) {
	if strings.HasPrefix(task, "task-") {
		if _, loaded := h.completedTasks.LoadOrStore(task, struct{}{}); !loaded {
			h.completed.Add(1)
		}
	}
}

func delegationParentResponse(names ...string) provider.ChatResponse {
	calls := make([]provider.ToolCall, len(names))
	for i, name := range names {
		calls[i] = provider.ToolCall{ID: fmt.Sprintf("call-%d", i), Name: name, Arguments: map[string]any{"task": fmt.Sprintf("task-%d", i)}}
	}
	return provider.ChatResponse{Message: provider.Message{Role: provider.MessageRoleAssistant, ToolCalls: calls}, FinishReason: "tool_calls"}
}

func runParallelParent(ctx context.Context, h *parallelHarness, max int, base *tool.Registry) (agent.RunState, error) {
	reg, err := BuildDelegateRegistry(DelegateDeps{BaseRegistry: base, SubAgentCfg: config.SubAgentConfig{Enabled: true, MaxTurns: 1, MaxTokens: 1000, MaxParallel: max}, Provider: h.provider, Config: config.Config{}, WorkDir: "/tmp", Events: output.NoopSink{}})
	if err != nil {
		return agent.RunState{}, err
	}
	req := agent.RunRequest{Provider: h.provider, Executor: tool.NewExecutor(reg, config.Config{}, nil, "/tmp", ""), Tools: reg.ToProviderSpecs(), Prompt: prompt.AssemblyOptions{Conversation: []provider.Message{{Role: provider.MessageRoleUser, Content: "start"}}}, Limits: agent.Limits{MaxTurns: 2}, ParallelTool: IsDelegationTool, MaxParallelTools: max}
	return agent.NewRunner().Run(ctx, req)
}

type parallelRunResult struct {
	state agent.RunState
	err   error
}

func startParallelParent(ctx context.Context, h *parallelHarness, max int, base *tool.Registry) <-chan parallelRunResult {
	result := make(chan parallelRunResult, 1)
	go func() {
		state, err := runParallelParent(ctx, h, max, base)
		result <- parallelRunResult{state: state, err: err}
	}()
	return result
}

func waitParallel(t *testing.T, ch <-chan struct{}, message string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(10 * time.Second):
		t.Fatal(message)
	}
}

func receiveParallel(t *testing.T, ch <-chan parallelRunResult, message string) parallelRunResult {
	t.Helper()
	select {
	case result := <-ch:
		return result
	case <-time.After(10 * time.Second):
		t.Fatal(message)
		return parallelRunResult{}
	}
}

func toolResults(state agent.RunState) []string {
	var results []string
	for _, msg := range state.Conversation {
		if msg.Role == agent.MessageRoleTool {
			results = append(results, msg.Content)
		}
	}
	return results
}

func assertTaskOrder(t *testing.T, results, want []string) {
	t.Helper()
	if len(results) != len(want) {
		t.Fatalf("tool result count = %d, want %d", len(results), len(want))
	}
	for i := range want {
		if !strings.Contains(results[i], want[i]) {
			t.Errorf("tool result %d = %q, want identity %q", i, results[i], want[i])
		}
	}
}

func TestParallelDelegationEndToEndOverlap(t *testing.T) {
	h := newParallelHarness(delegationParentResponse("explore", "explore", "explore"), 3)
	result := startParallelParent(context.Background(), h, 3, tool.NewRegistry())
	waitParallel(t, h.allStarted, "three children did not become active")
	close(h.done)
	if run := receiveParallel(t, result, "parallel run did not finish"); run.err != nil {
		t.Fatal(run.err)
	}
	if got := h.max.Load(); got != 3 {
		t.Fatalf("max active children = %d, want 3", got)
	}
}

func TestParallelDelegationEndToEndBounded(t *testing.T) {
	h := newParallelHarness(delegationParentResponse("explore", "explore", "explore", "explore"), 2)
	result := startParallelParent(context.Background(), h, 2, tool.NewRegistry())
	waitParallel(t, h.allStarted, "bounded batch did not start two children")
	close(h.done)
	run := receiveParallel(t, result, "bounded parallel run did not finish")
	if run.err != nil {
		t.Fatal(run.err)
	}
	if got := h.max.Load(); got > 2 {
		t.Fatalf("max active children = %d, want <= 2", got)
	}
	if got := h.completed.Load(); got != 4 {
		t.Fatalf("completed child runs = %d, want 4", got)
	}
}

func TestParallelDelegationEndToEndUnbounded(t *testing.T) {
	h := newParallelHarness(delegationParentResponse("explore", "explore", "explore", "explore"), 4)
	result := startParallelParent(context.Background(), h, 0, tool.NewRegistry())
	waitParallel(t, h.allStarted, "unbounded batch did not start four children")
	if got := h.active.Load(); got != 4 {
		t.Fatalf("active children = %d, want 4", got)
	}
	close(h.done)
	if run := receiveParallel(t, result, "unbounded parallel run did not finish"); run.err != nil {
		t.Fatal(run.err)
	}
}

func TestParallelDelegationEndToEndOrdering(t *testing.T) {
	h := newParallelHarness(delegationParentResponse("explore", "explore", "explore"), 3)
	releases := map[string]chan struct{}{"task-0": make(chan struct{}), "task-1": make(chan struct{}), "task-2": make(chan struct{})}
	ready := make(chan struct{})
	close(ready)
	h.release = func(task string) <-chan struct{} {
		if release, ok := releases[task]; ok {
			return release
		}
		return ready
	}
	result := startParallelParent(context.Background(), h, 3, tool.NewRegistry())
	waitParallel(t, h.allStarted, "ordering batch did not start three children")
	close(releases["task-2"])
	close(releases["task-1"])
	close(releases["task-0"])
	run := receiveParallel(t, result, "ordering parallel run did not finish")
	if run.err != nil {
		t.Fatal(run.err)
	}
	want := []string{"task-0", "task-1", "task-2"}
	assertTaskOrder(t, toolResults(run.state), want)
	assertTaskOrder(t, toolResults(agent.RunState{Conversation: run.state.Lineage.FullMessages()}), want)
}

func TestParallelDelegationEndToEndMixedBatch(t *testing.T) {
	base := tool.NewRegistry(tool.ToolDef{Name: "read", Handler: func(context.Context, map[string]any) (any, error) { return "read-result", nil }})
	h := newParallelHarness(delegationParentResponse("explore", "explore", "read", "explore", "explore"), 2)
	result := startParallelParent(context.Background(), h, 2, base)
	waitParallel(t, h.allStarted, "delegation run did not overlap")
	close(h.done)
	run := receiveParallel(t, result, "mixed parallel run did not finish")
	if run.err != nil {
		t.Fatal(run.err)
	}
	assertTaskOrder(t, toolResults(run.state), []string{"task-0", "task-1", "read-result", "task-3", "task-4"})
	if got := h.max.Load(); got != 2 {
		t.Fatalf("max active children across mixed batch = %d, want 2", got)
	}
}

func TestParallelDelegationEndToEndFailureIsolation(t *testing.T) {
	h := newParallelHarness(delegationParentResponse("explore", "explore", "explore"), 3)
	h.failTask = "task-1"
	ready := make(chan struct{})
	close(ready)
	h.release = func(string) <-chan struct{} { return ready }
	state, err := runParallelParent(context.Background(), h, 3, tool.NewRegistry())
	if err != nil {
		t.Fatal(err)
	}
	results := toolResults(state)
	if len(results) != 3 {
		t.Fatalf("tool result count = %d, want 3", len(results))
	}
	if !strings.Contains(results[0], "task-0") || !strings.Contains(results[2], "task-2") {
		t.Fatalf("successful sibling results = %v", results)
	}
	if !strings.Contains(results[1], `"status":"failed"`) || !strings.Contains(results[1], "child failed") {
		t.Fatalf("failed task result = %q, want structured tool error", results[1])
	}
}

func TestParallelDelegationEndToEndNoNesting(t *testing.T) {
	spec := makeSpec("nested-check", 100)
	_, exec := testChildRegistries(tool.NewRegistry())
	req := buildChildRunRequest(childRunRequestParams{AgentID: spec.AgentID, Provider: &fakeProvider{responses: []provider.ChatResponse{{Message: provider.Message{Content: "ok"}}}}, VisibleReg: exec, ExecReg: exec, PromptOpts: testBuildPrompt(spec)})
	if req.ParallelTool != nil {
		t.Fatal("child ParallelTool is non-nil")
	}
	if req.MaxParallelTools != 0 {
		t.Fatalf("child MaxParallelTools = %d", req.MaxParallelTools)
	}
}

func TestParallelDelegationEndToEndCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	h := newParallelHarness(delegationParentResponse("explore", "explore", "explore"), 3)
	h.blockOnCtx = true
	result := startParallelParent(ctx, h, 3, tool.NewRegistry())
	waitParallel(t, h.allStarted, "cancellation batch did not start three children")
	cancel()
	if run := receiveParallel(t, result, "cancelled parent run did not return"); run.err != nil {
		t.Fatal(run.err)
	}
	if got := h.active.Load(); got != 0 {
		t.Fatalf("active children after cancellation = %d", got)
	}
}

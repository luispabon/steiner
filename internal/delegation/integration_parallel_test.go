package delegation

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

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
	allStarted     chan struct{}
	done           chan struct{}
	cancelChildren bool
	target         int
	mu             sync.Mutex
	notified       bool
}

func newParallelHarness(parent provider.ChatResponse, n int) *parallelHarness {
	h := &parallelHarness{allStarted: make(chan struct{}), done: make(chan struct{}), target: n}
	h.provider = &parallelProvider{parent: parent}
	h.provider.child = func(ctx context.Context, task string) (provider.ChatResponse, error) {
		cur := h.active.Add(1)
		for {
			old := h.max.Load()
			if cur <= old || h.max.CompareAndSwap(old, cur) {
				break
			}
		}
		if h.started.Add(1) >= int32(h.target) {
			h.mu.Lock()
			if !h.notified {
				close(h.allStarted)
				h.notified = true
			}
			h.mu.Unlock()
		}
		select {
		case <-h.done:
		case <-ctx.Done():
			if h.cancelChildren {
				<-ctx.Done()
			}
		}
		h.active.Add(-1)
		return provider.ChatResponse{Message: provider.Message{Role: provider.MessageRoleAssistant, Content: task}, FinishReason: "stop"}, nil
	}
	return h
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

func TestParallelDelegationEndToEndOverlap(t *testing.T) {
	h := newParallelHarness(delegationParentResponse("explore", "explore", "explore"), 3)
	result := make(chan error, 1)
	go func() { _, err := runParallelParent(context.Background(), h, 3, tool.NewRegistry()); result <- err }()
	select {
	case <-h.allStarted:
		close(h.done)
	case err := <-result:
		t.Fatalf("run ended before overlap: %v", err)
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if got := h.max.Load(); got != 3 {
		t.Fatalf("max active children = %d, want 3", got)
	}
}

func TestParallelDelegationEndToEndBounded(t *testing.T) {
	h := newParallelHarness(delegationParentResponse("explore", "explore", "explore", "explore"), 2)
	result := make(chan error, 1)
	go func() { _, err := runParallelParent(context.Background(), h, 2, tool.NewRegistry()); result <- err }()
	<-h.allStarted
	close(h.done)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if got := h.max.Load(); got > 2 {
		t.Fatalf("max active children = %d, want <= 2", got)
	}
}

func TestParallelDelegationEndToEndUnbounded(t *testing.T) {
	h := newParallelHarness(delegationParentResponse("explore", "explore", "explore", "explore"), 4)
	result := make(chan error, 1)
	go func() { _, err := runParallelParent(context.Background(), h, 0, tool.NewRegistry()); result <- err }()
	<-h.allStarted
	if got := h.active.Load(); got != 4 {
		t.Fatalf("active children = %d, want 4", got)
	}
	close(h.done)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}

func TestParallelDelegationEndToEndOrdering(t *testing.T) {
	h := newParallelHarness(delegationParentResponse("explore", "explore", "explore"), 3)
	stateCh := make(chan agent.RunState, 1)
	go func() {
		state, _ := runParallelParent(context.Background(), h, 3, tool.NewRegistry())
		stateCh <- state
	}()
	<-h.allStarted
	close(h.done)
	state := <-stateCh
	var got []string
	for _, msg := range state.Conversation {
		if msg.Role == agent.MessageRoleTool {
			got = append(got, msg.Content)
		}
	}
	want := []string{"task-0", "task-1", "task-2"}
	if len(got) != len(want) {
		t.Fatalf("tool result count = %d, want %d", len(got), len(want))
	}
	for i := range got {
		if !strings.Contains(got[i], want[i]) {
			t.Fatalf("tool order = %v, want tasks %v", got, want)
		}
	}
	if len(state.Lineage.FullMessages()) != len(state.Conversation) {
		t.Fatal("lineage and conversation lengths differ")
	}
}

func TestParallelDelegationEndToEndMixedBatch(t *testing.T) {
	base := tool.NewRegistry(tool.ToolDef{Name: "read", Handler: func(context.Context, map[string]any) (any, error) { return "read", nil }})
	h := newParallelHarness(delegationParentResponse("explore", "explore", "read", "explore", "explore"), 2)
	result := make(chan error, 1)
	go func() { _, err := runParallelParent(context.Background(), h, 2, base); result <- err }()
	<-h.allStarted
	if got := h.active.Load(); got != 2 {
		t.Fatalf("delegations did not overlap: %d", got)
	}
	close(h.done)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}

func TestParallelDelegationEndToEndFailureIsolation(t *testing.T) {
	var calls atomic.Int32
	h := newParallelHarness(delegationParentResponse("explore", "explore", "explore"), 3)
	h.provider.child = func(_ context.Context, task string) (provider.ChatResponse, error) {
		if calls.Add(1) == 2 {
			return provider.ChatResponse{}, errors.New("child failed")
		}
		return provider.ChatResponse{Message: provider.Message{Role: provider.MessageRoleAssistant, Content: task}, FinishReason: "stop"}, nil
	}
	state, err := runParallelParent(context.Background(), h, 3, tool.NewRegistry())
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Conversation) == 0 {
		t.Fatal("missing conversation after sibling failure")
	}
	if !strings.Contains(fmt.Sprint(state.Conversation), "task-0") || !strings.Contains(fmt.Sprint(state.Conversation), "task-2") {
		t.Fatal("successful siblings missing after child failure")
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
	h.cancelChildren = true
	result := make(chan error, 1)
	go func() { _, err := runParallelParent(ctx, h, 3, tool.NewRegistry()); result <- err }()
	<-h.allStarted
	cancel()
	close(h.done)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if got := h.active.Load(); got != 0 {
		t.Fatalf("active children after cancellation = %d", got)
	}
}

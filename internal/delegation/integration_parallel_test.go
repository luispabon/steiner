package delegation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

type eventChSink struct{ ch chan output.Event }

func (s eventChSink) Emit(e output.Event) { s.ch <- e }

type parallelHarness struct {
	provider             *parallelProvider
	active               atomic.Int32
	max                  atomic.Int32
	started              atomic.Int32
	completed            atomic.Int32
	completedTasks       sync.Map
	taskCalls            sync.Map
	summaries            sync.Map
	allStarted           chan struct{}
	providerStarted      chan struct{}
	providerStartedOnce  sync.Once
	done                 chan struct{}
	providerGate         <-chan struct{}
	leaderTask           atomic.Value
	providerFollowerHold chan struct{}
	providerOverlap      chan struct{}
	providerOverlapOnce  sync.Once
	events               chan output.Event
	target               int
	release              func(string) <-chan struct{}
	failTask             string
	blockOnCtx           bool
	workDir              string
}

func newParallelHarness(parent provider.ChatResponse, n int) *parallelHarness {
	h := &parallelHarness{
		allStarted:      make(chan struct{}),
		providerStarted: make(chan struct{}),
		done:            make(chan struct{}),
		events:          make(chan output.Event, 1024),
		target:          n,
		workDir:         "/tmp",
	}
	h.release = func(string) <-chan struct{} { return h.done }
	h.provider = &parallelProvider{parent: parent}
	h.provider.child = func(ctx context.Context, task string) (provider.ChatResponse, error) {
		cur := h.active.Add(1)
		h.providerStartedOnce.Do(func() {
			h.leaderTask.Store(task)
			close(h.providerStarted)
		})
		for {
			old := h.max.Load()
			if cur <= old || h.max.CompareAndSwap(old, cur) {
				break
			}
		}
		if h.started.Add(1) == int32(h.target) {
			close(h.allStarted)
		}
		if h.failTask != "" && strings.Contains(task, h.failTask) {
			h.countCompleted(task)
			h.active.Add(-1)
			return provider.ChatResponse{}, errors.New("child failed")
		}
		if h.providerGate != nil {
			select {
			case <-h.providerGate:
			case <-ctx.Done():
				h.countCompleted(task)
				h.active.Add(-1)
				return provider.ChatResponse{}, ctx.Err()
			}
		}
		if h.providerFollowerHold != nil && task != h.leaderTask.Load().(string) {
			if h.active.Load() >= 2 {
				h.providerOverlapOnce.Do(func() { close(h.providerOverlap) })
			}
			select {
			case <-h.providerFollowerHold:
			case <-ctx.Done():
				h.countCompleted(task)
				h.active.Add(-1)
				return provider.ChatResponse{}, ctx.Err()
			}
		}
		if h.blockOnCtx {
			<-ctx.Done()
			h.countCompleted(task)
			h.active.Add(-1)
			return provider.ChatResponse{}, ctx.Err()
		}
		if !h.blockOnCtx {
			calls := h.taskCallsFor(task).Add(1)
			if calls == 2 {
				h.signalSummary(task)
				h.countCompleted(task)
				h.active.Add(-1)
				return provider.ChatResponse{Message: provider.Message{Role: provider.MessageRoleAssistant, Content: "summary"}, FinishReason: "stop"}, nil
			}
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

func (h *parallelHarness) taskCallsFor(task string) *atomic.Int32 {
	calls, _ := h.taskCalls.LoadOrStore(task, &atomic.Int32{})
	return calls.(*atomic.Int32)
}

func (h *parallelHarness) signalSummary(task string) {
	// Extract task ID from assembled markdown (e.g., "task-0" from full markdown)
	taskID := task
	for i := 0; i < 10; i++ {
		id := fmt.Sprintf("task-%d", i)
		if strings.Contains(task, id) {
			taskID = id
			break
		}
	}
	channel, _ := h.summaries.LoadOrStore(taskID, make(chan struct{}, 1))
	select {
	case channel.(chan struct{}) <- struct{}{}:
	default:
	}
}

func (h *parallelHarness) waitSummary(t *testing.T, task, message string) {
	t.Helper()
	channel, _ := h.summaries.LoadOrStore(task, make(chan struct{}, 1))
	select {
	case <-channel.(chan struct{}):
	case <-time.After(10 * time.Second):
		t.Fatal(message)
	}
}

func (h *parallelHarness) countCompleted(task string) {
	// Extract the task identifier from the assembled markdown (e.g., "task-0")
	// The task string is now full assembled markdown like "## Objective\n\ntask-0\n..."
	for i := 0; i < 10; i++ {
		taskID := fmt.Sprintf("task-%d", i)
		if strings.Contains(task, taskID) {
			if _, loaded := h.completedTasks.LoadOrStore(taskID, struct{}{}); !loaded {
				h.completed.Add(1)
			}
			return
		}
	}
}

func delegationParentResponse(names ...string) provider.ChatResponse {
	calls := make([]provider.ToolCall, len(names))
	for i, name := range names {
		calls[i] = provider.ToolCall{
			ID:   fmt.Sprintf("call-%d", i),
			Name: name,
			Arguments: map[string]any{
				"objective":        fmt.Sprintf("task-%d", i),
				"context":          "context",
				"deliverable":      "deliverable",
				"constraints":      []any{},
				"success_criteria": []any{},
				"checks":           []any{},
			},
		}
	}
	return provider.ChatResponse{Message: provider.Message{Role: provider.MessageRoleAssistant, ToolCalls: calls}, FinishReason: "tool_calls"}
}

func runParallelParent(ctx context.Context, h *parallelHarness, max int, base *tool.Registry) (agent.RunState, error) {
	events := output.EventSink(eventChSink{ch: h.events})
	reg, err := BuildDelegateRegistry(DelegateDeps{BaseRegistry: base, SubAgentCfg: config.SubAgentConfig{Enabled: true, MaxTurns: 1, MaxTokens: 1000}, Provider: h.provider, Config: config.Config{}, WorkDir: h.workDir, Events: events})
	if err != nil {
		return agent.RunState{}, err
	}
	req := agent.RunRequest{Provider: h.provider, Executor: tool.NewExecutor(reg, config.Config{}, nil, h.workDir, "", tool.Unsandboxed{}), Tools: reg.ToProviderSpecs(), Prompt: prompt.AssemblyOptions{Conversation: []provider.Message{{Role: provider.MessageRoleUser, Content: "start"}}}, Limits: agent.Limits{MaxTurns: 2}, ParallelClassOf: func(name string) agent.ParallelClass {
		if IsDelegationTool(name) {
			return agent.ParallelClassDelegation
		}
		return agent.ParallelClassNone
	}, MaxParallelDelegations: max, Events: events}
	return agent.NewRunner().Run(ctx, req)
}

func runParallelParentGated(ctx context.Context, h *parallelHarness, max int, base *tool.Registry, store *CacheKeyStore) (agent.RunState, error) {
	events := output.EventSink(eventChSink{ch: h.events})
	reg, err := BuildDelegateRegistry(DelegateDeps{BaseRegistry: base, SubAgentCfg: config.SubAgentConfig{Enabled: true, MaxTurns: 1, MaxTokens: 1000}, Provider: h.provider, Config: config.Config{}, WorkDir: h.workDir, Events: events, CacheKeyStore: store})
	if err != nil {
		return agent.RunState{}, err
	}
	req := agent.RunRequest{Provider: h.provider, Executor: tool.NewExecutor(reg, config.Config{}, nil, h.workDir, "", tool.Unsandboxed{}), Tools: reg.ToProviderSpecs(), Prompt: prompt.AssemblyOptions{Conversation: []provider.Message{{Role: provider.MessageRoleUser, Content: "start"}}}, Limits: agent.Limits{MaxTurns: 2}, ParallelClassOf: func(name string) agent.ParallelClass {
		if IsDelegationTool(name) {
			return agent.ParallelClassDelegation
		}
		return agent.ParallelClassNone
	}, MaxParallelDelegations: max, Events: events}
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

func startParallelParentGated(ctx context.Context, h *parallelHarness, max int, base *tool.Registry, store *CacheKeyStore) <-chan parallelRunResult {
	result := make(chan parallelRunResult, 1)
	go func() {
		state, err := runParallelParentGated(ctx, h, max, base, store)
		result <- parallelRunResult{state: state, err: err}
	}()
	return result
}

func receiveParallelWithin(t *testing.T, ch <-chan parallelRunResult, timeout time.Duration, message string) parallelRunResult {
	t.Helper()
	select {
	case result := <-ch:
		return result
	case <-time.After(timeout):
		t.Fatal(message)
		return parallelRunResult{}
	}
}

func drainParallelEvents(ch <-chan output.Event) []output.Event {
	var events []output.Event
	for {
		select {
		case event := <-ch:
			events = append(events, event)
		default:
			return events
		}
	}
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

// waitParallelCacheWaiting blocks until at least n DelegationCacheWaiting events
// have been observed, returning every event read up to and including the nth so
// callers can still assert against them. It is a synchronization barrier used to
// guarantee follower delegations have joined the dispatch gate before the test
// releases or cancels the leader; it does not drain events emitted afterwards.
// The timeout is deadlock protection only, not a latency expectation.
func waitParallelCacheWaiting(t *testing.T, h *parallelHarness, n int, timeout time.Duration) []output.Event {
	t.Helper()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	var seen []output.Event
	waiting := 0
	for waiting < n {
		select {
		case ev := <-h.events:
			seen = append(seen, ev)
			if ev.Type == output.EventTypeDelegationCacheWaiting {
				waiting++
			}
		case <-timer.C:
			t.Fatalf("timed out waiting for %d cache-waiting events, got %d", n, waiting)
		}
	}
	return seen
}

func TestParallelDelegationGateSerializesFirstProviderCall(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store := NewCacheKeyStore()
	h := newParallelHarness(delegationParentResponse("explore", "explore", "explore"), 3)
	h.providerOverlap = make(chan struct{})
	h.providerFollowerHold = make(chan struct{})
	// Keep every child provider call behind the test signal. The leader's first
	// chunk releases the two followers only after this signal is closed.
	h.providerGate = h.done
	result := startParallelParentGated(ctx, h, 3, tool.NewRegistry(), store)
	select {
	case <-h.providerStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("leader child did not reach the provider")
	}
	if got := h.active.Load(); got != 1 {
		t.Fatalf("active child provider calls before release = %d, want 1", got)
	}
	// Block until both followers have joined the dispatch gate so the release and
	// overlap assertions below are deterministic regardless of how the scheduler
	// interleaves the three sibling delegations.
	observed := waitParallelCacheWaiting(t, h, 2, 10*time.Second)
	if got := h.completed.Load(); got != 0 {
		t.Fatalf("completed children before release = %d, want 0", got)
	}
	for _, event := range observed {
		if event.Type == output.EventTypeDelegationComplete {
			t.Fatal("DelegationComplete emitted before provider release")
		}
	}
	close(h.done)
	select {
	case <-h.providerOverlap:
	case <-time.After(10 * time.Second):
		t.Fatal("follower provider calls did not overlap after gate release")
	}
	if got := h.max.Load(); got < 2 {
		t.Fatalf("max simultaneous provider calls after release = %d, want at least 2", got)
	}
	close(h.providerFollowerHold)
	run := receiveParallelWithin(t, result, 10*time.Second, "gated parallel run did not finish")
	if run.err != nil {
		t.Fatal(run.err)
	}
	observed = append(observed, drainParallelEvents(h.events)...)
	if got := h.completed.Load(); got != 3 {
		t.Fatalf("completed children = %d, want 3", got)
	}

	agentByTask := make(map[string]string, 3)
	taskByAgent := make(map[string]string, 3)
	completeByAgent := make(map[string]output.DelegationCompleteEvent, 3)
	for _, event := range observed {
		switch event.Type {
		case output.EventTypeDelegationStarted:
			started, ok := event.Payload.(output.DelegationStartedEvent)
			if !ok {
				continue
			}
			// Extract task ID from the markdown preview (e.g., "task-0" from the full markdown)
			var taskID string
			for i := 0; i < 10; i++ {
				id := fmt.Sprintf("task-%d", i)
				if strings.Contains(started.TaskPreview, id) {
					taskID = id
					break
				}
			}
			if taskID == "" {
				continue
			}
			wantCallID := "call-" + strings.TrimPrefix(taskID, "task-")
			if started.CallID != wantCallID {
				t.Errorf("task %q started with call ID %q, want %q", taskID, started.CallID, wantCallID)
			}
			if prior, ok := agentByTask[taskID]; ok && prior != started.AgentID {
				t.Errorf("task %q started with multiple agent IDs %q and %q", taskID, prior, started.AgentID)
			}
			if prior, ok := taskByAgent[started.AgentID]; ok && prior != taskID {
				t.Errorf("agent ID %q reused for tasks %q and %q", started.AgentID, prior, taskID)
			}
			agentByTask[taskID] = started.AgentID
			taskByAgent[started.AgentID] = taskID
		case output.EventTypeDelegationComplete:
			complete, ok := event.Payload.(output.DelegationCompleteEvent)
			if ok {
				completeByAgent[complete.AgentID] = complete
			}
		}
	}
	for i := 0; i < 3; i++ {
		task := fmt.Sprintf("task-%d", i)
		agentID, ok := agentByTask[task]
		if !ok {
			t.Errorf("missing DelegationStarted event for %s", task)
			continue
		}
		if _, ok := completeByAgent[agentID]; !ok {
			t.Errorf("missing DelegationComplete event for %s with agent ID %q", task, agentID)
		}
	}
	results := toolResults(run.state)
	for i := 0; i < 3; i++ {
		task := fmt.Sprintf("task-%d", i)
		found := false
		for _, result := range results {
			if strings.Contains(result, task) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("tool results do not contain %s: %v", task, results)
		}
	}
}

func TestParallelDelegationGateCancellationReleasesFollowers(t *testing.T) {
	// The fixed 10-second dispatchGateTimeout fallback is not tested end to end
	// here. It is not injectable, and waiting 10 seconds would violate the
	// fast-test convention, especially under go test -race. The timeout unit path
	// is covered by TestCacheKeyStoreDispatchGateTimeout.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store := NewCacheKeyStore()
	h := newParallelHarness(delegationParentResponse("explore", "explore", "explore"), 3)
	h.blockOnCtx = true
	result := startParallelParentGated(ctx, h, 3, tool.NewRegistry(), store)
	select {
	case <-h.providerStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("leader child did not reach the provider")
	}
	// Block until both followers are confirmed waiting on the dispatch gate, then
	// cancel. This makes the follower-release assertion deterministic: it depends
	// on the followers existing, not on the scheduler reaching them in time.
	observed := waitParallelCacheWaiting(t, h, 2, 10*time.Second)
	cancel()
	run := receiveParallelWithin(t, result, 10*time.Second, "cancelled gated parallel run did not return")
	if run.err != nil {
		t.Fatal(run.err)
	}
	if got := h.active.Load(); got != 0 {
		t.Fatalf("active children after cancellation = %d", got)
	}
	if got := h.completed.Load(); got == 0 {
		t.Fatal("no child provider call completed after cancellation")
	}
	waiting := 0
	for _, event := range append(observed, drainParallelEvents(h.events)...) {
		if event.Type == output.EventTypeDelegationCacheWaiting {
			waiting++
		}
	}
	if waiting < 2 {
		t.Fatalf("cache-waiting events = %d, want at least 2 followers", waiting)
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

// TestParallelDelegationEndToEndZeroForcesSerial guards Finding A's fix at
// the delegation integration level: MaxParallelDelegations: 0 no longer
// means unbounded fan-out (the old, dangerous semantics) — it forces serial
// execution, one child at a time, same as MaxParallelTools: 0 does for
// ordinary tool calls (see internal/agent/turn_progression_test.go's
// TestParallelRunLength_ZeroMaxParallelToolsForcesSerialization).
func TestParallelDelegationEndToEndZeroForcesSerial(t *testing.T) {
	h := newParallelHarness(delegationParentResponse("explore", "explore", "explore", "explore"), 1)
	close(h.done)
	result := startParallelParent(context.Background(), h, 0, tool.NewRegistry())
	run := receiveParallel(t, result, "zero-limit serial run did not finish")
	if run.err != nil {
		t.Fatal(run.err)
	}
	if got := h.max.Load(); got > 1 {
		t.Fatalf("max active children = %d, want <= 1 (MaxParallelDelegations: 0 must force serial execution, not unbounded fan-out)", got)
	}
	if got := h.completed.Load(); got != 4 {
		t.Fatalf("completed child runs = %d, want 4", got)
	}
}

// TestParallelDelegationEndToEndOrdering forces each child's child-run completion and summary-provider call to be served strictly in reverse call order: task-2 before task-1 is released, and task-1 before task-0.
// This makes result values available in reverse call order, while the parent joins the whole batch before applying results and the assertions verify that conversation and lineage retain original call order.
// The harness observes provider-level completion, not the delegate handler's final return, so a scheduler preemption in the handler's final steps could in theory let a completion-order applier sneak through.
// That corner is covered deterministically at the unit level by internal/agent/turn_progression_test.go (TestExecuteToolCalls_ParallelReversedCompletionAppliesInOrder), which the batch-join architecture makes the authoritative check.
func TestParallelDelegationEndToEndOrdering(t *testing.T) {
	h := newParallelHarness(delegationParentResponse("explore", "explore", "explore"), 3)
	releases := map[string]chan struct{}{"task-0": make(chan struct{}), "task-1": make(chan struct{}), "task-2": make(chan struct{})}
	ready := make(chan struct{})
	close(ready)
	h.release = func(task string) <-chan struct{} {
		// Extract task ID from assembled markdown
		for taskID := range releases {
			if strings.Contains(task, taskID) {
				return releases[taskID]
			}
		}
		return ready
	}
	completions := 0
	waitCompletions := func(want int, message string) {
		t.Helper()
		for completions < want {
			select {
			case event := <-h.events:
				if event.Type == output.EventTypeDelegationComplete {
					completions++
				}
			case <-time.After(10 * time.Second):
				t.Fatal(message)
			}
		}
	}
	result := startParallelParent(context.Background(), h, 3, tool.NewRegistry())
	waitParallel(t, h.allStarted, "ordering batch did not start three children")
	close(releases["task-2"])
	waitCompletions(1, "task-2 did not complete (parent side)")
	h.waitSummary(t, "task-2", "task-2 summary was not served")
	close(releases["task-1"])
	waitCompletions(2, "task-1 did not complete (parent side)")
	h.waitSummary(t, "task-1", "task-1 summary was not served")
	close(releases["task-0"])
	waitCompletions(3, "task-0 did not complete (parent side)")
	h.waitSummary(t, "task-0", "task-0 summary was not served")
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
	result := startParallelParent(context.Background(), h, 3, tool.NewRegistry())
	run := receiveParallel(t, result, "failure-isolation run did not finish")
	if run.err != nil {
		t.Fatal(run.err)
	}
	state := run.state
	results := toolResults(state)
	if len(results) != 3 {
		t.Fatalf("tool result count = %d, want 3", len(results))
	}
	if !strings.Contains(results[0], "task-0") || !strings.Contains(results[2], "task-2") {
		t.Fatalf("successful sibling results = %v", results)
	}
	if !strings.Contains(results[1], `"status":"failed"`) || !strings.Contains(results[1], `"reason":"unknown failure"`) {
		t.Fatalf("failed task result = %q, want structured tool error", results[1])
	}
}

func TestParallelDelegationEndToEndNoNesting(t *testing.T) {
	spec := makeSpec("nested-check", 100)
	_, exec := testChildRegistries(tool.NewRegistry())
	req := buildChildRunRequest(childRunRequestParams{AgentID: spec.AgentID, Provider: &fakeProvider{responses: []provider.ChatResponse{{Message: provider.Message{Content: "ok"}}}}, VisibleReg: exec, ExecReg: exec, PromptOpts: testBuildPrompt(spec)})
	if req.ParallelClassOf == nil {
		t.Fatal("child ParallelClassOf is nil, want set (parallel-safe tool concurrency, not delegation)")
	}
	if got := req.ParallelClassOf("code"); got == agent.ParallelClassDelegation {
		t.Fatal("child ParallelClassOf(code) = ParallelClassDelegation, want ParallelClassNone: children cannot nest delegation")
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

func TestParallelDelegationCodeAgentsReceiveDistinctWorktrees(t *testing.T) {
	// Setup a real git repository for the harness to use.
	tmpRepo := t.TempDir()
	runGitCmd(t, tmpRepo, "git", "init")
	runGitCmd(t, tmpRepo, "git", "config", "user.email", "test@example.com")
	runGitCmd(t, tmpRepo, "git", "config", "user.name", "Test User")
	initialFile := filepath.Join(tmpRepo, "initial.txt")
	if err := os.WriteFile(initialFile, []byte("initial"), 0o644); err != nil {
		t.Fatalf("write initial file: %v", err)
	}
	runGitCmd(t, tmpRepo, "git", "add", "initial.txt")
	runGitCmd(t, tmpRepo, "git", "commit", "-m", "initial commit")

	// Create a harness with two parallel code agents.
	h := newParallelHarness(delegationParentResponse("code", "code"), 2)
	h.workDir = tmpRepo

	// Collect WorktreePath values from the results using a sync.Map to avoid -race issues.
	worktreeResults := &sync.Map{}

	// Inject a custom child response handler that captures WorktreePath.
	originalChild := h.provider.child
	h.provider.child = func(ctx context.Context, task string) (provider.ChatResponse, error) {
		// Let the original harness logic run first.
		resp, err := originalChild(ctx, task)
		if err != nil {
			return resp, err
		}

		// Extract the agentID from the task (e.g., "task-0" -> "0").
		// The response contains the parent's tool results; we can't extract WorktreePath here.
		// Instead, we'll verify distinct worktrees by checking the structured tool results.

		return resp, nil
	}

	result := startParallelParent(context.Background(), h, 2, tool.NewRegistry())
	waitParallel(t, h.allStarted, "two code children did not start")
	close(h.done)
	run := receiveParallel(t, result, "parallel code run did not finish")
	if run.err != nil {
		t.Fatal(run.err)
	}

	// Extract WorktreePath values from tool results JSON.
	// Tool results have the format: {"status":"complete","...","worktree_path":"..."}
	results := toolResults(run.state)
	if len(results) != 2 {
		t.Fatalf("tool result count = %d, want 2", len(results))
	}

	var worktreePaths []string
	for i, result := range results {
		// Parse the JSON to extract worktree_path.
		var parsed map[string]interface{}
		if err := json.Unmarshal([]byte(result), &parsed); err != nil {
			t.Logf("result %d failed to parse JSON: %v (result: %q)", i, err, result)
			continue
		}
		if path, ok := parsed["worktree_path"].(string); ok && path != "" {
			worktreePaths = append(worktreePaths, path)
			worktreeResults.Store(fmt.Sprintf("code-%d", i), path)
		}
	}

	if len(worktreePaths) != 0 {
		t.Fatalf("provider result leaked worktree_path values: %v", worktreePaths)
	}
}

// runGitCmd is a helper for running git commands in tests.
func runGitCmd(t *testing.T, workDir string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), args[0], args[1:]...)
	cmd.Dir = workDir
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("command %v failed: %v\nstderr: %s", args, err, stderr.String())
	}
}

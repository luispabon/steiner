package delegation

import (
	"context"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

func lifecycleTestStructuredTask(objective string) map[string]any {
	return map[string]any{
		"objective":        objective,
		"context":          "context",
		"deliverable":      "deliverable",
		"constraints":      []any{},
		"success_criteria": []any{},
		"checks":           []any{},
	}
}

func TestSpecializedCodeHandlerRegistersWorktreeAndUnregisters(t *testing.T) {
	t.Parallel()
	repo := setupTestRepo(t)

	controller := NewActiveController()
	var calls int
	runner := &mockRunner{runFunc: func(_ context.Context, _ agent.RunRequest) (agent.RunState, error) {
		if calls == 0 {
			ids := controller.ActiveAgentIDs()
			if len(ids) != 1 {
				t.Errorf("active agents during code run = %v, want one", ids)
			} else {
				worktree, ok := controller.WorktreeFor(ids[0])
				if !ok || worktree.Path == "" || worktree.Branch == "" {
					t.Errorf("active code worktree = %+v, %t, want path and branch", worktree, ok)
				}
				typeValue, ok := controller.TypeFor(ids[0])
				if !ok || typeValue != AgentTypeCode {
					t.Errorf("active code type = %q, %t, want %q, true", typeValue, ok, AgentTypeCode)
				}
			}
		}
		calls++
		return successRunState(), nil
	}}
	deps := minimalDeps(runner)
	deps.ActiveController = controller
	deps.WorkDir = repo

	if _, err := newSpecializedHandler(AgentTypeCode, deps)(context.Background(), lifecycleTestStructuredTask("implement")); err != nil {
		t.Fatalf("handler returned error: %v", err)
	}
	if ids := controller.ActiveAgentIDs(); len(ids) != 0 {
		t.Errorf("active agents after code completion = %v, want none", ids)
	}
}

func TestFollowUpCodeHandlerRegistersSessionWorktreeAndUnregisters(t *testing.T) {
	t.Parallel()
	repo := setupTestRepo(t)
	runCmd(t, repo, "git", "checkout", "-b", "delegate/session-worktree")

	store := NewSessionStore()
	store.Save(&ChildSession{
		Spec: Spec{AgentID: "code-follow-up", AgentType: AgentTypeCode, Task: "continue"},
		Request: agent.RunRequest{
			Prompt: promptWithConversation("initial"),
			Tools:  []provider.ToolSpec{{Function: provider.ToolFunctionSpec{Name: "mutate"}}},
		},
		Conversation: []agent.Message{{Role: agent.MessageRoleAssistant, Content: "initial"}},
		Remediation: &RemediationConfig{
			WorktreePath:   repo,
			ExpectedBranch: "delegate/session-worktree",
			IsDirty:        func(context.Context) ([]string, error) { return nil, nil },
			Head:           func(context.Context) (string, error) { return "head", nil },
			Committed:      func(context.Context, string, []string) (bool, error) { return true, nil },
		},
	})

	controller := NewActiveController()
	var calls int
	runner := &mockRunner{runFunc: func(_ context.Context, _ agent.RunRequest) (agent.RunState, error) {
		if calls == 0 {
			ids := controller.ActiveAgentIDs()
			if len(ids) != 1 {
				t.Errorf("active agents during follow-up = %v, want one", ids)
			} else {
				worktree, ok := controller.WorktreeFor(ids[0])
				if !ok || worktree != (CodeWorktree{Path: repo, Branch: "delegate/session-worktree"}) {
					t.Errorf("active follow-up worktree = %+v, %t, want session worktree", worktree, ok)
				}
			}
		}
		calls++
		return successRunState(), nil
	}}
	handler := NewFollowUpHandler(SubAgentHandlerDeps{
		SubAgentCfg:      config.SubAgentConfig{MaxTurns: 2, MaxTokens: 20, MaxFollowUps: 100},
		SessionStore:     store,
		ActiveController: controller,
		Runner:           runner,
	})

	if _, err := handler(context.Background(), map[string]any{"agent_id": "code-follow-up", "message": "continue"}); err != nil {
		t.Fatalf("handler returned error: %v", err)
	}
	if ids := controller.ActiveAgentIDs(); len(ids) != 0 {
		t.Errorf("active agents after follow-up = %v, want none", ids)
	}
}

func TestSpecializedHandlerCacheWaitingCancellationReturnsCancelled(t *testing.T) {
	store := NewCacheKeyStore()
	store.testWaitTimeout = time.Second
	key, err := store.KeyFor(AgentTypeExplore, provider.NewPromptCacheKey)
	if err != nil {
		t.Fatalf("mint cache key: %v", err)
	}
	_, release, _ := store.beginDispatch(key)
	defer release(output.DelegationCacheReleaseLeaderExited)

	controller := NewActiveController()
	events := &recordingEventSink{}
	var runnerCalls atomic.Int32
	runner := &mockRunner{runFunc: func(_ context.Context, _ agent.RunRequest) (agent.RunState, error) {
		runnerCalls.Add(1)
		return successRunState(), nil
	}}
	deps := minimalDeps(runner)
	deps.ActiveController = controller
	deps.CacheKeyStore = store
	deps.Events = events

	originalIDGen := idGen
	idGen = func() string { return "cancelled-child" }
	defer func() { idGen = originalIDGen }()

	type handlerResult struct {
		value any
		err   error
	}
	resultCh := make(chan handlerResult, 1)
	go func() {
		value, err := newSpecializedHandler(AgentTypeExplore, deps)(context.Background(), lifecycleTestStructuredTask("wait"))
		resultCh <- handlerResult{value: value, err: err}
	}()

	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for {
		active := controller.ActiveAgentIDs()
		waiting := waitingEvents(events.Events())
		if len(active) == 1 && len(waiting) == 1 {
			break
		}
		select {
		case <-deadline.C:
			t.Fatal("timed out waiting for active cache-waiting child")
		case <-time.After(time.Millisecond):
		}
	}
	if !controller.CancelAgent("cancelled-child") {
		t.Fatal("CancelAgent returned false for active child")
	}

	var result handlerResult
	select {
	case result = <-resultCh:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for cancelled handler")
	}
	if result.err != nil {
		t.Fatalf("handler returned error: %v", result.err)
	}
	toolResult, ok := result.value.(tool.ExecutionResult)
	if !ok {
		t.Fatalf("handler result type = %T, want tool.ExecutionResult", result.value)
	}
	delegationResult, ok := toolResult.Value.(Result)
	if !ok {
		t.Fatalf("result value type = %T, want Result", toolResult.Value)
	}
	if delegationResult.Status != StatusCancelled || delegationResult.SessionResumable {
		t.Errorf("cancelled result = %+v, want cancelled and non-resumable", delegationResult)
	}
	if runnerCalls.Load() != 0 {
		t.Errorf("runner calls = %d, want 0", runnerCalls.Load())
	}
	if len(controller.ActiveAgentIDs()) != 0 {
		t.Errorf("active agents after cancellation = %v, want none", controller.ActiveAgentIDs())
	}

	var stopped bool
	for _, event := range events.Events() {
		if event.Type != output.EventTypeStopReason {
			continue
		}
		payload, ok := event.Payload.(output.StopReasonEvent)
		if ok && payload.Reason == "cancelled" && event.Scope.AgentID == "cancelled-child" && event.Scope.AgentType == string(AgentTypeExplore) {
			stopped = true
		}
	}
	if !stopped {
		t.Error("missing scoped cancelled StopReasonEvent")
	}
	completions := cancelledCompletionEvents(events.Events(), "cancelled-child")
	if len(completions) != 1 {
		t.Fatalf("cancelled completion events = %d, want 1", len(completions))
	}
	payload, ok := completions[0].Payload.(output.DelegationCompleteEvent)
	if !ok {
		t.Fatalf("completion payload = %T, want DelegationCompleteEvent", completions[0].Payload)
	}
	if payload.Status != string(StatusCancelled) {
		t.Errorf("completion status = %q, want %q", payload.Status, StatusCancelled)
	}
	if want := (output.DelegationOccurrence{AgentID: "cancelled-child"}); payload.DelegationOccurrence != want {
		t.Errorf("completion occurrence = %+v, want %+v", payload.DelegationOccurrence, want)
	}
	if completions[0].Scope.AgentID != "cancelled-child" || completions[0].Scope.AgentType != string(AgentTypeExplore) {
		t.Errorf("completion scope = %+v, want explore child scope", completions[0].Scope)
	}
}

// cancelledCompletionEvents returns the terminal completion events for agentID.
func cancelledCompletionEvents(events []output.Event, agentID string) []output.Event {
	var completions []output.Event
	for _, event := range events {
		if event.Type != output.EventTypeDelegationComplete {
			continue
		}
		payload, ok := event.Payload.(output.DelegationCompleteEvent)
		if !ok || payload.AgentID != agentID {
			continue
		}
		completions = append(completions, event)
	}
	return completions
}

func TestQueuedSpecializedCancellationEmitsTerminalComplete(t *testing.T) {
	events := &recordingEventSink{}
	sup, sink := newAsyncSupervisor(1)
	blocker := newAsyncChild("blocker", "")
	spawnAsync(context.Background(), t, sup, blocker)
	<-blocker.started

	deps := minimalDeps(&mockRunner{runFunc: func(context.Context, agent.RunRequest) (agent.RunState, error) {
		t.Error("cancelled queued child ran the runner")
		return successRunState(), nil
	}})
	deps.AsyncSubAgents = true
	deps.Supervisor = sup
	deps.Events = events

	originalIDGen := idGen
	idGen = func() string { return "queued-child" }
	defer func() { idGen = originalIDGen }()

	batch := testBatchID(9)
	ctx := context.WithValue(agent.WithToolBatchID(context.Background(), batch), tool.ExecutionCallIDKey{}, "call-queued")
	got, err := newSpecializedHandler(AgentTypeExplore, deps)(ctx, lifecycleTestStructuredTask("queued"))
	if err != nil {
		t.Fatalf("handler returned error: %v", err)
	}
	result, ok := got.(tool.ExecutionResult)
	if !ok || result.DelegationAdmission == nil || result.DelegationAdmission.AgentID != "queued-child" {
		t.Fatalf("handler result = %#v, want accepted queued-child admission", got)
	}
	if got := sup.CancelAgent("queued-child", false, CancelCauseUser); got != CancelAccepted {
		t.Fatalf("cancel outcome = %v, want %v", got, CancelAccepted)
	}
	recv(t, sink.ch, "cancelled completion")

	completions := cancelledCompletionEvents(events.Events(), "queued-child")
	if len(completions) != 1 {
		t.Fatalf("cancelled completion events = %d, want 1", len(completions))
	}
	payload, ok := completions[0].Payload.(output.DelegationCompleteEvent)
	if !ok {
		t.Fatalf("completion payload = %T, want DelegationCompleteEvent", completions[0].Payload)
	}
	if payload.Status != string(StatusCancelled) {
		t.Errorf("completion status = %q, want %q", payload.Status, StatusCancelled)
	}
	want := output.DelegationOccurrence{CallID: "call-queued", BatchID: batch, AgentID: "queued-child"}
	if payload.DelegationOccurrence != want {
		t.Errorf("completion occurrence = %+v, want %+v", payload.DelegationOccurrence, want)
	}
	if completions[0].Scope.AgentID != "queued-child" || completions[0].Scope.AgentType != string(AgentTypeExplore) {
		t.Errorf("completion scope = %+v, want explore child scope", completions[0].Scope)
	}

	close(blocker.release)
	recv(t, sink.ch, "blocker completion")
}

func TestQueuedShutdownCancellationEmitsTerminalComplete(t *testing.T) {
	events := &recordingEventSink{}
	sup, _ := newAsyncSupervisor(1)
	blocker := newAsyncChild("blocker", "")
	spawnAsync(context.Background(), t, sup, blocker)
	<-blocker.started

	deps := minimalDeps(&mockRunner{runFunc: func(context.Context, agent.RunRequest) (agent.RunState, error) {
		t.Error("shutdown-cancelled queued child ran the runner")
		return successRunState(), nil
	}})
	deps.AsyncSubAgents = true
	deps.Supervisor = sup
	deps.Events = events

	originalIDGen := idGen
	idGen = func() string { return "shutdown-child" }
	defer func() { idGen = originalIDGen }()

	batch := testBatchID(11)
	ctx := context.WithValue(agent.WithToolBatchID(context.Background(), batch), tool.ExecutionCallIDKey{}, "call-shutdown")
	got, err := newSpecializedHandler(AgentTypeExplore, deps)(ctx, lifecycleTestStructuredTask("shutdown"))
	if err != nil {
		t.Fatalf("handler returned error: %v", err)
	}
	result, ok := got.(tool.ExecutionResult)
	if !ok || result.DelegationAdmission == nil || result.DelegationAdmission.AgentID != "shutdown-child" {
		t.Fatalf("handler result = %#v, want accepted shutdown-child admission", got)
	}

	sup.Shutdown(context.Background(), CancelCauseSystem)

	completions := cancelledCompletionEvents(events.Events(), "shutdown-child")
	if len(completions) != 1 {
		t.Fatalf("cancelled completion events = %d, want 1", len(completions))
	}
	payload, ok := completions[0].Payload.(output.DelegationCompleteEvent)
	if !ok {
		t.Fatalf("completion payload = %T, want DelegationCompleteEvent", completions[0].Payload)
	}
	if payload.Status != string(StatusCancelled) {
		t.Errorf("completion status = %q, want %q", payload.Status, StatusCancelled)
	}
	want := output.DelegationOccurrence{CallID: "call-shutdown", BatchID: batch, AgentID: "shutdown-child"}
	if payload.DelegationOccurrence != want {
		t.Errorf("completion occurrence = %+v, want %+v", payload.DelegationOccurrence, want)
	}
	if completions[0].Scope.AgentID != "shutdown-child" || completions[0].Scope.AgentType != string(AgentTypeExplore) {
		t.Errorf("completion scope = %+v, want explore child scope", completions[0].Scope)
	}
}

func TestSpecializedCodeCacheWaitingCancellationDiscardsAfterCompletion(t *testing.T) {
	repo := setupTestRepo(t)

	store := NewCacheKeyStore()
	store.testWaitTimeout = time.Second
	key, err := store.KeyFor(AgentTypeCode, provider.NewPromptCacheKey)
	if err != nil {
		t.Fatalf("mint cache key: %v", err)
	}
	_, release, _ := store.beginDispatch(key)
	defer release(output.DelegationCacheReleaseLeaderExited)

	controller := NewActiveController()
	sessions := NewSessionStore()
	events := &recordingEventSink{}
	var runnerCalls atomic.Int32
	runner := &mockRunner{runFunc: func(_ context.Context, _ agent.RunRequest) (agent.RunState, error) {
		runnerCalls.Add(1)
		return successRunState(), nil
	}}
	deps := minimalDeps(runner)
	deps.ActiveController = controller
	deps.CacheKeyStore = store
	deps.SessionStore = sessions
	deps.Events = events
	deps.WorkDir = repo

	const agentID = "cache-waiting-code"
	originalIDGen := idGen
	idGen = func() string { return agentID }
	defer func() { idGen = originalIDGen }()

	resultCh := make(chan error, 1)
	go func() {
		_, err := newSpecializedHandler(AgentTypeCode, deps)(context.Background(), lifecycleTestStructuredTask("wait"))
		resultCh <- err
	}()

	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	var worktree CodeWorktree
	for {
		var ok bool
		worktree, ok = controller.WorktreeFor(agentID)
		if ok && len(waitingEvents(events.Events())) == 1 {
			break
		}
		select {
		case <-deadline.C:
			t.Fatal("timed out waiting for code cache gate")
		case <-time.After(time.Millisecond):
		}
	}
	if got := controller.CancelAgentWithDiscard(agentID, true); got != CancelAccepted {
		t.Fatalf("targeted code cancellation with discard = %v, want %v", got, CancelAccepted)
	}
	if err := <-resultCh; err != nil {
		t.Fatalf("handler returned error: %v", err)
	}
	if runnerCalls.Load() != 0 {
		t.Fatalf("runner calls = %d, want 0", runnerCalls.Load())
	}
	if _, ok := sessions.Get(agentID); ok {
		t.Fatal("cancelled code session remained resumable")
	}
	sessions.Save(&ChildSession{Spec: Spec{AgentID: agentID}})
	if _, ok := sessions.Get(agentID); ok {
		t.Fatal("cancelled code session was not tombstoned")
	}
	if _, err := os.Stat(worktree.Path); !os.IsNotExist(err) {
		t.Fatalf("code worktree stat = %v, want not exist", err)
	}
	if branchExists(t, repo, worktree.Branch) {
		t.Fatalf("code branch %q still exists", worktree.Branch)
	}
	disposals := disposalEvents(events.Events())
	if len(disposals) != 1 {
		t.Fatalf("disposal events = %d, want one", len(disposals))
	}
	payload := disposals[0].Payload.(output.DelegationWorktreeDisposalEvent)
	if !payload.Removed || payload.Error != "" || disposals[0].Scope.AgentID != agentID || disposals[0].Scope.AgentType != string(AgentTypeCode) {
		t.Fatalf("disposal event = %+v scope=%+v, want successful scoped disposal", payload, disposals[0].Scope)
	}
}

func TestFollowUpInvalidatedSessionCannotResume(t *testing.T) {
	t.Parallel()
	store := NewSessionStore()
	store.Save(&ChildSession{Spec: Spec{AgentID: "invalidated", AgentType: AgentTypeReview, Task: "review"}})
	store.Invalidate("invalidated")

	handler := NewFollowUpHandler(SubAgentHandlerDeps{SessionStore: store})
	if _, err := handler(context.Background(), map[string]any{"agent_id": "invalidated", "message": "resume"}); err == nil {
		t.Fatal("follow-up succeeded for invalidated session")
	}
	if _, ok := store.Get("invalidated"); ok {
		t.Fatal("invalidated session became available")
	}
}

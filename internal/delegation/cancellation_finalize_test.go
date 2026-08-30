package delegation

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/output"
)

func TestFinalizeDelegateCancellationKeepsSessionAndWorktree(t *testing.T) {
	repo, cleanup := setupTestRepo(t)
	defer cleanup()
	wt, err := ProvisionCodeWorktree(context.Background(), repo, "keep-child")
	if err != nil {
		t.Fatalf("ProvisionCodeWorktree: %v", err)
	}
	controller := NewActiveController()
	if _, err := controller.Register("keep-child", context.Background(), AgentTypeCode, wt); err != nil {
		t.Fatalf("Register: %v", err)
	}
	store := NewSessionStore()
	store.Save(&ChildSession{Spec: Spec{AgentID: "keep-child"}})
	events := &recordingEventSink{}
	result := Result{AgentID: "keep-child", Status: StatusPartial, StopReason: "cancelled", SessionResumable: true}

	finalizeDelegateCancellation(events, store, controller, repo, "keep-child", &result)

	if _, ok := store.Get("keep-child"); !ok {
		t.Fatal("session was changed without a discard request")
	}
	if !result.SessionResumable {
		t.Fatal("result became non-resumable without a discard request")
	}
	if _, err := os.Stat(wt.Path); err != nil {
		t.Fatalf("kept worktree stat: %v", err)
	}
	if got := len(disposalEvents(events.Events())); got != 0 {
		t.Fatalf("disposal events = %d, want 0", got)
	}
}

func TestFinalizeDelegateCancellationDiscardsSelectedWorktree(t *testing.T) {
	repo, cleanup := setupTestRepo(t)
	defer cleanup()
	selected, err := ProvisionCodeWorktree(context.Background(), repo, "discard-child")
	if err != nil {
		t.Fatalf("Provision selected worktree: %v", err)
	}
	sibling, err := ProvisionCodeWorktree(context.Background(), repo, "sibling-child")
	if err != nil {
		t.Fatalf("Provision sibling worktree: %v", err)
	}
	controller := NewActiveController()
	if _, err := controller.Register("discard-child", context.Background(), AgentTypeCode, selected); err != nil {
		t.Fatalf("Register selected: %v", err)
	}
	if _, err := controller.Register("sibling-child", context.Background(), AgentTypeCode, sibling); err != nil {
		t.Fatalf("Register sibling: %v", err)
	}
	if !controller.RequestDiscard("discard-child") {
		t.Fatal("RequestDiscard returned false")
	}
	store := NewSessionStore()
	store.Save(&ChildSession{Spec: Spec{AgentID: "discard-child"}})
	events := &recordingEventSink{}
	result := Result{
		AgentID:          "discard-child",
		Status:           StatusPartial,
		StopReason:       "cancelled",
		SessionResumable: true,
		Output:           "work; " + cancelledSessionRetentionPhrase,
		Summary:          "summary; " + cancelledSessionRetentionPhrase,
	}

	finalizeDelegateCancellation(events, store, controller, repo, "discard-child", &result)

	if _, ok := store.Get("discard-child"); ok {
		t.Fatal("discarded session is still available")
	}
	store.Save(&ChildSession{Spec: Spec{AgentID: "discard-child"}})
	store.Update("discard-child", SessionUpdateParams{TurnCount: 1})
	if _, ok := store.Get("discard-child"); ok {
		t.Fatal("discarded session was resurrected")
	}
	if result.SessionResumable {
		t.Fatal("discarded result is resumable")
	}
	if strings.Contains(result.Output, cancelledSessionRetentionPhrase) || strings.Contains(result.Summary, cancelledSessionRetentionPhrase) {
		t.Fatalf("discard retention phrase remains in result: %+v", result)
	}
	if _, err := os.Stat(selected.Path); !os.IsNotExist(err) {
		t.Fatalf("selected worktree stat = %v, want not exist", err)
	}
	if _, err := os.Stat(sibling.Path); err != nil {
		t.Fatalf("sibling worktree was changed: %v", err)
	}
	if branchExists(t, repo, selected.Branch) {
		t.Fatalf("selected branch %q still exists", selected.Branch)
	}
	if !branchExists(t, repo, sibling.Branch) {
		t.Fatalf("sibling branch %q was removed", sibling.Branch)
	}
	disposals := disposalEvents(events.Events())
	if len(disposals) != 1 {
		t.Fatalf("disposal events = %d, want 1", len(disposals))
	}
	payload := disposals[0].Payload.(output.DelegationWorktreeDisposalEvent)
	if !payload.Removed || payload.Error != "" || disposals[0].Scope.AgentID != "discard-child" || disposals[0].Scope.AgentType != string(AgentTypeCode) {
		t.Fatalf("disposal event = %+v scope=%+v, want successful scoped disposal", payload, disposals[0].Scope)
	}
}

func TestFinalizeDelegateCancellationIgnoresCompletedResult(t *testing.T) {
	repo, cleanup := setupTestRepo(t)
	defer cleanup()
	wt, err := ProvisionCodeWorktree(context.Background(), repo, "complete-child")
	if err != nil {
		t.Fatalf("ProvisionCodeWorktree: %v", err)
	}
	controller := NewActiveController()
	if _, err := controller.Register("complete-child", context.Background(), AgentTypeCode, wt); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if !controller.RequestDiscard("complete-child") {
		t.Fatal("RequestDiscard returned false")
	}
	store := NewSessionStore()
	session := &ChildSession{Spec: Spec{AgentID: "complete-child"}}
	store.Save(session)
	events := &recordingEventSink{}
	result := Result{AgentID: "complete-child", Status: StatusComplete, SessionResumable: true}

	finalizeDelegateCancellation(events, store, controller, repo, "complete-child", &result)

	if got, ok := store.Get("complete-child"); !ok || got != session {
		t.Fatal("completed result changed session")
	}
	if _, err := os.Stat(wt.Path); err != nil {
		t.Fatalf("completed worktree stat: %v", err)
	}
	if len(disposalEvents(events.Events())) != 0 {
		t.Fatal("completed result emitted disposal event")
	}
}

func TestFinalizeDelegateCancellationReportsPruneFailure(t *testing.T) {
	repo, cleanup := setupTestRepo(t)
	defer cleanup()
	path := filepath.Join(repo, ".steiner", "worktrees", "foreign-child")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	runCmd(t, repo, "git", "worktree", "add", "-b", "foreign-child", path, "HEAD")
	controller := NewActiveController()
	if _, err := controller.Register("failed-child", context.Background(), AgentTypeCode, CodeWorktree{Path: path, Branch: "foreign-child"}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if !controller.RequestDiscard("failed-child") {
		t.Fatal("RequestDiscard returned false")
	}
	store := NewSessionStore()
	store.Save(&ChildSession{Spec: Spec{AgentID: "failed-child"}})
	events := &recordingEventSink{}
	result := Result{AgentID: "failed-child", Status: StatusCancelled, SessionResumable: true, Summary: cancelledSessionRetentionPhrase}

	finalizeDelegateCancellation(events, store, controller, repo, "failed-child", &result)

	if _, ok := store.Get("failed-child"); ok {
		t.Fatal("failed discard session is still available")
	}
	if result.SessionResumable || strings.Contains(result.Summary, cancelledSessionRetentionPhrase) {
		t.Fatalf("failed discard result = %+v", result)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("foreign worktree stat = %v, want present", err)
	}
	disposals := disposalEvents(events.Events())
	if len(disposals) != 1 {
		t.Fatalf("disposal events = %d, want 1", len(disposals))
	}
	payload := disposals[0].Payload.(output.DelegationWorktreeDisposalEvent)
	if payload.Removed || !strings.Contains(payload.Error, ErrWorktreeNotDelegation.Error()) {
		t.Fatalf("failure disposal payload = %+v, want ErrWorktreeNotDelegation", payload)
	}
}

func TestSpecializedCodeDiscardWaitsForRunner(t *testing.T) {
	repo, cleanup := setupTestRepo(t)
	defer cleanup()
	controller := NewActiveController()
	store := NewSessionStore()
	events := &recordingEventSink{}
	entered := make(chan struct{})
	release := make(chan struct{})
	var runCalls atomic.Int32
	runner := &mockRunner{runFunc: func(ctx context.Context, req agent.RunRequest) (agent.RunState, error) {
		if _, ok := req.Executor.(summaryOnlyExecutor); ok {
			return agent.RunState{Conversation: []agent.Message{{Role: agent.MessageRoleAssistant, Content: "summary"}}, StopReason: agent.StopReasonComplete}, nil
		}
		runCalls.Add(1)
		close(entered)
		<-release
		return agent.RunState{TurnCount: 1, StopReason: agent.StopReasonCancelled}, ctx.Err()
	}}
	deps := minimalDeps(runner)
	deps.ActiveController = controller
	deps.SessionStore = store
	deps.Events = events
	deps.WorkDir = repo
	originalIDGen := idGen
	idGen = func() string { return "ordered-child" }
	defer func() { idGen = originalIDGen }()

	resultCh := make(chan error, 1)
	go func() {
		_, err := newSpecializedHandler(AgentTypeCode, deps)(context.Background(), map[string]any{"task": "wait"})
		resultCh <- err
	}()

	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("runner did not start")
	}
	if !controller.RequestDiscard("ordered-child") || !controller.CancelAgent("ordered-child") {
		t.Fatal("failed to request cancellation and discard")
	}
	wt, ok := controller.WorktreeFor("ordered-child")
	if !ok {
		t.Fatal("active worktree missing while runner is blocked")
	}
	if _, err := os.Stat(wt.Path); err != nil {
		t.Fatalf("worktree disappeared before runner returned: %v", err)
	}
	close(release)

	select {
	case err := <-resultCh:
		if err != nil {
			t.Fatalf("handler returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for handler")
	}
	if runCalls.Load() != 1 {
		t.Fatalf("runner calls = %d, want 1", runCalls.Load())
	}
	if _, err := os.Stat(wt.Path); !os.IsNotExist(err) {
		t.Fatalf("worktree stat after runner returned = %v, want not exist", err)
	}
}

func disposalEvents(events []output.Event) []output.Event {
	var result []output.Event
	for _, event := range events {
		if event.Type == output.EventTypeDelegationWorktreeDisposal {
			result = append(result, event)
		}
	}
	return result
}

func branchExists(t *testing.T, repo, branch string) bool {
	t.Helper()
	return exec.CommandContext(context.Background(), "git", "-C", repo, "show-ref", "--verify", "--quiet", "refs/heads/"+branch).Run() == nil
}

var _ output.EventSink = (*recordingEventSink)(nil)

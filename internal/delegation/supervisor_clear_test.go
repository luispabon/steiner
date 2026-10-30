package delegation

import (
	"context"
	"sync"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/tool"
)

// spawnHeldGroupChild spawns a grouped async child in the first test batch,
// completes it, and leaves its completion held until the group is sealed.
func spawnHeldGroupChild(t *testing.T, s *Supervisor, id, scope string) {
	t.Helper()
	child := newAsyncChild(id, "g")
	child.job.GroupScope = scope
	spawnAsync(batchCtx(testBatchID(1)), t, s, child)
	waitClosed(t, child.started, id+" started")
	close(child.release)
	waitFinished(t, s, id)
}

func groupMemberCount(s *Supervisor, scope string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	group := s.groups[groupKey{scope: scope, batch: testBatchID(1), label: "g"}]
	if group == nil {
		return 0
	}
	return len(group.members)
}

func TestSupervisorClearFinishedSubAgentsDiscardsHeldGroup(t *testing.T) {
	s, sink := newAsyncSupervisor(2)
	scope := testGroupScope(s)
	spawnHeldGroupChild(t, s, "a", scope)
	spawnHeldGroupChild(t, s, "b", scope)
	sink.none(t)

	if got := s.ClearFinishedSubAgents(scope); got != 0 {
		t.Fatalf("ClearFinishedSubAgents = %d, want 0", got)
	}
	if got := s.Pending(); len(got) != 0 {
		t.Fatalf("Pending after clear = %+v, want none", got)
	}
	if s.HasPending() || s.IsPending("a") || s.IsPending("b") {
		t.Fatal("cleared agents still pending")
	}
	if hasJob(s, "a") || hasJob(s, "b") {
		t.Fatal("cleared agents still tracked")
	}
	if got := groupMemberCount(s, scope); got != 0 {
		t.Fatalf("group members after clear = %d, want 0", got)
	}

	s.SealGroupBatch(scope, testBatchID(1))
	sink.none(t)
	s.MarkDelivered([]string{"call-a", "call-b"})
	if s.HasPending() {
		t.Fatal("MarkDelivered after clear left pending state")
	}

	s.releaseGroupScope(scope)
	s.mu.Lock()
	_, retained := s.scopes[scope]
	s.mu.Unlock()
	if retained {
		t.Fatal("released scope retained after its jobs were cleared")
	}
}

func TestSupervisorClearFinishedSubAgentsRefusesOutsideScope(t *testing.T) {
	s, sink := newAsyncSupervisor(2)
	scope := testGroupScope(s)
	other := s.newGroupScope(agent.DelegationGroupLedger{Version: agent.DelegationGroupLedgerVersion})
	spawnHeldGroupChild(t, s, "a", scope)

	if got := s.ClearFinishedSubAgents(other); got != 1 {
		t.Fatalf("ClearFinishedSubAgents(other) = %d, want 1", got)
	}
	if !s.IsPending("a") {
		t.Fatal("outside-scope refusal dropped a pending agent")
	}
	if got := groupMemberCount(s, scope); got != 1 {
		t.Fatalf("group members after refused clear = %d, want 1", got)
	}

	s.SealGroupBatch(scope, testBatchID(1))
	batch := recv(t, sink.ch, "held completion after refused clear")
	if len(batch) != 1 || batch[0].AgentID != "a" {
		t.Fatalf("posted = %+v, want a", batch)
	}
}

func TestSupervisorClearFinishedSubAgentsRefusesUnfinished(t *testing.T) {
	s, sink := newAsyncSupervisor(2)
	scope := testGroupScope(s)
	spawnHeldGroupChild(t, s, "a", scope)
	running := newAsyncChild("b", "g")
	running.job.GroupScope = scope
	spawnAsync(batchCtx(testBatchID(1)), t, s, running)
	waitClosed(t, running.started, "b started")
	sink.none(t)

	if got := s.ClearFinishedSubAgents(scope); got != 2 {
		t.Fatalf("ClearFinishedSubAgents = %d, want 2", got)
	}
	if !s.IsPending("a") || !s.IsPending("b") {
		t.Fatal("refused clear dropped a pending agent")
	}
	if got := groupMemberCount(s, scope); got != 2 {
		t.Fatalf("group members after refused clear = %d, want 2", got)
	}

	close(running.release)
	waitFinished(t, s, "b")
	sink.none(t)
	if got := s.ClearFinishedSubAgents(scope); got != 0 {
		t.Fatalf("ClearFinishedSubAgents after finish = %d, want 0", got)
	}
	s.SealGroupBatch(scope, testBatchID(1))
	sink.none(t)
}

func TestSupervisorClearFinishedSubAgentsRefusesCancellingJob(t *testing.T) {
	s, sink := newAsyncSupervisor(1)
	running := newAsyncChild("running", "")
	spawnAsync(context.Background(), t, s, running)
	waitClosed(t, running.started, "running started")

	queued := newAsyncChild("queued", "")
	entered, release := make(chan struct{}), make(chan struct{})
	queued.job.OnCancelledBeforeStart = func() tool.ExecutionResult {
		close(entered)
		<-release
		return tool.ExecutionResult{Value: Result{AgentID: "queued", Status: StatusCancelled}}
	}
	spawnAsync(context.Background(), t, s, queued)
	if got := s.CancelAgent("queued", false, CancelCauseSystem); got != CancelAccepted {
		t.Fatalf("CancelAgent = %v, want CancelAccepted", got)
	}
	waitClosed(t, entered, "queued cancellation callback")

	if got := s.ClearFinishedSubAgents(""); got != 2 {
		t.Fatalf("ClearFinishedSubAgents = %d, want 2", got)
	}
	state := jobFor(s, "queued")
	if state == nil || state.completion != nil || state.phase != phaseCancelling {
		t.Fatalf("queued state after refused clear = %+v, want phaseCancelling without completion", state)
	}

	close(release)
	waitFinished(t, s, "queued")
	recv(t, sink.ch, "queued cancellation completion")
	close(running.release)
	waitFinished(t, s, "running")
	recv(t, sink.ch, "running completion")

	if got := s.ClearFinishedSubAgents(""); got != 0 {
		t.Fatalf("ClearFinishedSubAgents after finish = %d, want 0", got)
	}
}

func TestSupervisorClearFinishedSubAgentsEmptyScopeIsNotWildcard(t *testing.T) {
	s, sink := newAsyncSupervisor(2)
	scope := s.newGroupScope(agent.DelegationGroupLedger{Version: agent.DelegationGroupLedgerVersion})
	child := newAsyncChild("a", "g")
	child.job.GroupScope = scope
	if _, _, err := s.Spawn(batchCtx(testBatchID(1)), child.job); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	waitClosed(t, child.started, "a started")
	close(child.release)
	waitFinished(t, s, "a")
	sink.none(t)

	if got := s.ClearFinishedSubAgents(""); got != 1 {
		t.Fatalf("ClearFinishedSubAgents(empty) = %d, want 1", got)
	}
	if !s.IsPending("a") {
		t.Fatal("empty-scope refusal dropped a scoped agent")
	}
	s.SealGroupBatch(scope, testBatchID(1))
	batch := recv(t, sink.ch, "scoped completion")
	if len(batch) != 1 || batch[0].AgentID != "a" {
		t.Fatalf("posted = %+v, want a", batch)
	}
}

func TestSupervisorClearFinishedSubAgentsMatchesEmptyScopeLiterally(t *testing.T) {
	s, sink := newAsyncSupervisor(1)
	child := newAsyncChild("a", "g")
	child.job.GroupScope = ""
	if _, _, err := s.Spawn(batchCtx(testBatchID(1)), child.job); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	waitClosed(t, child.started, "a started")
	close(child.release)
	waitFinished(t, s, "a")
	sink.none(t)

	if got := s.ClearFinishedSubAgents(""); got != 0 {
		t.Fatalf("ClearFinishedSubAgents(empty) = %d, want 0", got)
	}
	if hasJob(s, "a") {
		t.Fatal("empty-scope job still tracked after clear")
	}
	s.SealGroupBatch("", testBatchID(1))
	sink.none(t)
}

func TestSupervisorClearFinishedSubAgentsConcurrentSealAndAck(t *testing.T) {
	s, sink := newAsyncSupervisor(2)
	scope := testGroupScope(s)
	spawnHeldGroupChild(t, s, "a", scope)
	spawnHeldGroupChild(t, s, "b", scope)
	if got := s.ClearFinishedSubAgents(scope); got != 0 {
		t.Fatalf("ClearFinishedSubAgents = %d, want 0", got)
	}

	var wg sync.WaitGroup
	for range 4 {
		wg.Add(3)
		go func() { defer wg.Done(); s.SealGroupBatch(scope, testBatchID(1)) }()
		go func() { defer wg.Done(); s.MarkDelivered([]string{"call-a", "call-b"}) }()
		go func() { defer wg.Done(); s.ClearFinishedSubAgents(scope) }()
	}
	wg.Wait()
	sink.none(t)
	if got := s.ClearFinishedSubAgents(scope); got != 0 {
		t.Fatalf("ClearFinishedSubAgents after concurrency = %d, want 0", got)
	}
}

package delegation

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/tool"
)

// TestCancelDuringAcceptingPublicationSettlesSealedGroupExactlyOnce interleaves
// a group seal and a cancel while the Accepted event is still unpublished. An
// unpublished member cannot execute or post, so the sealed group releases its
// cancelled member exactly once after publication returns.
func TestCancelDuringAcceptingPublicationSettlesSealedGroupExactlyOnce(t *testing.T) {
	events := blockedAcceptedSink{entered: make(chan struct{}), release: make(chan struct{})}
	s, sink := newAsyncSupervisor(1)
	scope := testGroupScope(s)
	member := newAsyncChild("a", "g")
	member.job.GroupScope = scope
	member.job.Events = events
	spawnResult := make(chan error, 1)
	go func() {
		_, _, err := s.Spawn(batchCtx(testBatchID(1)), member.job)
		spawnResult <- err
	}()
	waitClosed(t, events.entered, "accepted publication")

	s.SealGroupBatch(scope, testBatchID(1))
	if got := s.CancelAgent("a", false, CancelCauseUser); got != CancelAccepted {
		t.Fatalf("CancelAgent = %v, want CancelAccepted", got)
	}
	state := jobFor(s, "a")
	s.mu.Lock()
	phase, completion := state.phase, state.completion
	s.mu.Unlock()
	if phase != phaseAccepting || completion != nil {
		t.Fatalf("during publication: phase = %v, completion = %+v, want accepting with no completion", phase, completion)
	}
	select {
	case <-member.started:
		t.Fatal("cancelled member executed before acceptance publication returned")
	default:
	}
	sink.none(t)

	close(events.release)
	if err := recv(t, spawnResult, "Spawn result"); err != nil {
		t.Fatal(err)
	}
	waitClosed(t, state.settled, "cancel finalization")
	batch := recv(t, sink.ch, "sealed group release")
	if len(batch) != 1 || batch[0].AgentID != "a" || batch[0].Status != string(StatusCancelled) || !batch[0].Quiet {
		t.Fatalf("released = %+v, want one quiet cancelled member", batch)
	}
	sink.none(t)
	select {
	case <-member.started:
		t.Fatal("cancelled member executed after publication")
	default:
	}
	if got := causeFor(s, "a"); got != CancelCauseUser {
		t.Fatalf("cause = %v, want CancelCauseUser", got)
	}
}

// TestShutdownWhileGroupedPublicationBlockedPostsLateCancellationExactlyOnce
// blocks a grouped member inside acceptance publication until after shutdown's
// join deadline. Shutdown must not post for an unpublished member, and the
// member's late publication must settle exactly once, sealed batch or not.
func TestShutdownWhileGroupedPublicationBlockedPostsLateCancellationExactlyOnce(t *testing.T) {
	tests := []struct {
		name string
		seal bool
	}{
		{name: "unsealed batch"},
		{name: "sealed batch", seal: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			events := blockedAcceptedSink{entered: make(chan struct{}), release: make(chan struct{})}
			s, sink := newAsyncSupervisor(1)
			s.joinTimeout = 20 * time.Millisecond
			scope := testGroupScope(s)
			var finalized atomic.Int32
			late := newAsyncChild("late", "g")
			late.job.GroupScope = scope
			late.job.Events = events
			late.job.OnCancelledBeforeStart = func() tool.ExecutionResult {
				finalized.Add(1)
				return tool.ExecutionResult{Value: Result{AgentID: "late", Status: StatusCancelled}}
			}
			spawnResult := make(chan error, 1)
			go func() {
				_, _, err := s.Spawn(batchCtx(testBatchID(1)), late.job)
				spawnResult <- err
			}()
			waitClosed(t, events.entered, "blocked accepted publication")
			state := jobFor(s, "late")
			if tt.seal {
				s.SealGroupBatch(scope, testBatchID(1))
			}

			report := s.Shutdown(context.Background(), CancelCauseSystem)
			if len(report.Unjoined) != 0 {
				t.Fatalf("unjoined = %+v, want none for an unpublished job", report.Unjoined)
			}
			s.mu.Lock()
			phase := state.phase
			s.mu.Unlock()
			if phase != phaseAccepting {
				t.Fatalf("phase after shutdown = %v, want accepting", phase)
			}
			sink.none(t)

			close(events.release)
			if err := recv(t, spawnResult, "late Spawn"); err != nil {
				t.Fatal(err)
			}
			batch := recv(t, sink.ch, "late cancellation completion")
			if len(batch) != 1 || batch[0].AgentID != "late" || batch[0].Status != string(StatusCancelled) {
				t.Fatalf("completion = %+v, want one cancelled late member", batch)
			}
			waitClosed(t, state.settled, "late finalizer settlement")
			if got := finalized.Load(); got != 1 {
				t.Fatalf("cancellation finalizer count = %d, want 1", got)
			}
			sink.none(t)
			s.MarkDelivered([]string{"call-late"})
			if s.IsPending("late") {
				t.Fatal("late member remains pending after acknowledgement")
			}
		})
	}
}

// TestShutdownAfterGroupReleasePostsNoDuplicate checks that a group released
// before shutdown is not posted again by shutdown's forced group release, and
// that sealing twice cannot repost it either.
func TestShutdownAfterGroupReleasePostsNoDuplicate(t *testing.T) {
	s, sink := newAsyncSupervisor(1)
	a := newAsyncChild("a", "g")
	spawnAsync(batchCtx(testBatchID(1)), t, s, a)
	waitClosed(t, a.started, "a started")
	close(a.release)
	waitFinished(t, s, "a")
	sink.none(t)

	s.SealGroupBatch(testGroupScope(s), testBatchID(1))
	batch := recv(t, sink.ch, "group release")
	if len(batch) != 1 || batch[0].AgentID != "a" {
		t.Fatalf("released = %+v, want a", batch)
	}
	s.SealGroupBatch(testGroupScope(s), testBatchID(1))

	if report := s.Shutdown(context.Background(), CancelCauseSystem); len(report.Unjoined) != 0 {
		t.Fatalf("unjoined after completed group = %+v, want none", report.Unjoined)
	}
	sink.none(t)
	s.MarkDelivered([]string{"call-a"})
	if s.IsPending("a") {
		t.Fatal("released and acknowledged group member remains pending")
	}
}

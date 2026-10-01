package delegation

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tool"
)

type blockedAcceptedSink struct {
	entered chan struct{}
	release chan struct{}
}

func (s blockedAcceptedSink) Emit(event output.Event) {
	if event.Type == output.EventTypeDelegationAccepted {
		close(s.entered)
		<-s.release
	}
}

func TestAcceptedPublicationPrecedesCancelFinalizeAndQueuePump(t *testing.T) {
	events := blockedAcceptedSink{entered: make(chan struct{}), release: make(chan struct{})}
	s := NewSupervisor(SupervisorOptions{MaxParallel: 1, Events: events})
	var finalized atomic.Int32
	a := newAsyncChild("a", "")
	a.job.OnCancelledBeforeStart = func() tool.ExecutionResult {
		finalized.Add(1)
		return tool.ExecutionResult{Value: Result{AgentID: "a", Status: StatusCancelled}}
	}
	spawnResult := make(chan error, 1)
	go func() { _, err := s.Spawn(context.Background(), a.job); spawnResult <- err }()
	select {
	case <-events.entered:
	case <-time.After(time.Second):
		t.Fatal("accepted publication did not enter")
	}
	outcome := make(chan CancelOutcome, 1)
	go func() { outcome <- s.CancelAgent("a", false, CancelCauseSystem) }()
	select {
	case got := <-outcome:
		if got != CancelAccepted {
			t.Fatalf("CancelAgent = %v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("CancelAgent blocked on publication")
	}
	if got := finalized.Load(); got != 0 {
		t.Fatalf("finalized before acceptance publication returned: %d", got)
	}
	close(events.release)
	if err := <-spawnResult; err != nil {
		t.Fatal(err)
	}
	waitFinished(t, s, "a")
	if got := finalized.Load(); got != 1 {
		t.Fatalf("finalizer count = %d, want 1", got)
	}
}

func TestShutdownPublicationTimeoutSettlesLateAcceptedJob(t *testing.T) {
	events := blockedAcceptedSink{entered: make(chan struct{}), release: make(chan struct{})}
	sink := newChannelSink()
	s := NewSupervisor(SupervisorOptions{MaxParallel: 1, Events: events, JoinTimeout: 20 * time.Millisecond})
	s.SetCompletionSink(sink)
	job := newAsyncChild("late", "")
	spawnResult := make(chan error, 1)
	go func() {
		_, err := s.Spawn(agent.WithToolBatchID(context.Background(), "batch"), job.job)
		spawnResult <- err
	}()
	waitClosed(t, events.entered, "accepted publication")
	shutdown := make(chan struct{})
	go func() { s.Shutdown(context.Background(), CancelCauseSystem); close(shutdown) }()
	waitClosed(t, shutdown, "bounded shutdown")
	if got := s.Pending(); len(got) != 1 || got[0].AgentID != "late" {
		t.Fatalf("pending before publication returns = %+v", got)
	}
	close(events.release)
	if err := recv(t, spawnResult, "spawn result"); err != nil {
		t.Fatal(err)
	}
	batch := recv(t, sink.ch, "late completion")
	if len(batch) != 1 || batch[0].AgentID != "late" || batch[0].Status != string(StatusCancelled) {
		t.Fatalf("completion = %+v", batch)
	}
	s.MarkDelivered([]string{"call-late"})
	if s.IsPending("late") {
		t.Fatal("late job remains pending after acknowledgement")
	}
	sink.none(t)
}

func TestPublicationBarrierShutdownWaitsAcceptance(t *testing.T) {
	events := blockedAcceptedSink{entered: make(chan struct{}), release: make(chan struct{})}
	sink := newChannelSink()
	s := NewSupervisor(SupervisorOptions{MaxParallel: 1, Events: events, JoinTimeout: time.Second})
	s.SetCompletionSink(sink)
	job := newAsyncChild("a", "")
	spawnResult := make(chan error, 1)
	go func() {
		_, err := s.Spawn(agent.WithToolBatchID(context.Background(), "batch"), job.job)
		spawnResult <- err
	}()
	select {
	case <-events.entered:
	case <-time.After(time.Second):
		t.Fatal("accepted publication did not enter")
	}
	shutdown := make(chan struct{})
	go func() { s.Shutdown(context.Background(), CancelCauseSystem); close(shutdown) }()
	select {
	case <-shutdown:
		t.Fatal("shutdown returned before acceptance publication")
	case <-time.After(20 * time.Millisecond):
	}
	close(events.release)
	if err := <-spawnResult; err != nil {
		t.Fatal(err)
	}
	select {
	case <-shutdown:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not settle")
	}
}

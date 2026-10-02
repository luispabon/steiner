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
	queued  chan output.Event
	enabled *atomic.Bool
}

func (s blockedAcceptedSink) Emit(event output.Event) {
	if event.Type == output.EventTypeDelegationQueued && s.queued != nil {
		s.queued <- event
	}
	if event.Type == output.EventTypeDelegationAccepted && (s.enabled == nil || s.enabled.Load()) {
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
	go func() { _, _, err := s.Spawn(context.Background(), a.job); spawnResult <- err }()
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

func TestCancelAllDefersQueuedFinalizerUntilBlockedAcceptancePublishes(t *testing.T) {
	events := blockedAcceptedSink{entered: make(chan struct{}), release: make(chan struct{})}
	s := NewSupervisor(SupervisorOptions{MaxParallel: 1, Events: events})
	sink := newChannelSink()
	s.SetCompletionSink(sink)
	var finalized atomic.Int32
	job := newAsyncChild("queued", "")
	job.job.OnCancelledBeforeStart = func() tool.ExecutionResult {
		finalized.Add(1)
		return tool.ExecutionResult{Value: Result{AgentID: "queued", Status: StatusCancelled}}
	}
	spawnResult := make(chan error, 1)
	go func() { _, _, err := s.Spawn(batchCtx("batch"), job.job); spawnResult <- err }()
	waitClosed(t, events.entered, "accepted publication")
	s.CancelAll(CancelCauseSystem)
	if got := finalized.Load(); got != 0 {
		t.Fatalf("finalizer count before acceptance release = %d, want 0", got)
	}
	if state := jobFor(s, "queued"); state == nil {
		t.Fatal("queued job missing during publication")
	} else if state.completion != nil {
		t.Fatalf("completion before acceptance release = %+v", state.completion)
	}
	close(events.release)
	if err := recv(t, spawnResult, "Spawn result"); err != nil {
		t.Fatal(err)
	}
	waitFinished(t, s, "queued")
	if got := finalized.Load(); got != 1 {
		t.Fatalf("finalizer count after acceptance release = %d, want 1", got)
	}
	batch := recv(t, sink.ch, "cancelled completion")
	if len(batch) != 1 || batch[0].AgentID != "queued" || batch[0].Status != string(StatusCancelled) {
		t.Fatalf("completion = %+v", batch)
	}
	sink.none(t)
	if got := finalized.Load(); got != 1 {
		t.Fatalf("finalizer count after completion = %d, want 1", got)
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
		_, _, err := s.Spawn(agent.WithToolBatchID(context.Background(), "batch"), job.job)
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

func TestShutdownLatePublicationSettlesGroupedJobsAndScopes(t *testing.T) {
	blockLate := &atomic.Bool{}
	events := blockedAcceptedSink{entered: make(chan struct{}), release: make(chan struct{}), enabled: blockLate}
	sink := newChannelSink()
	s := NewSupervisor(SupervisorOptions{MaxParallel: 1, Events: events, JoinTimeout: 20 * time.Millisecond})
	s.SetCompletionSink(sink)
	scope := s.NewGroupScope(agent.DelegationGroupLedger{Version: 1})
	ctx := agent.WithToolBatchID(context.Background(), "batch")
	sibling := newAsyncChild("sibling", "same")
	sibling.job.GroupScope = scope
	spawnAsync(ctx, t, s, sibling)
	waitClosed(t, sibling.started, "grouped sibling")
	close(sibling.release)
	waitFinished(t, s, "sibling")
	unrelated := newAsyncChild("unrelated", "other")
	unrelated.job.GroupScope = scope
	spawnAsync(ctx, t, s, unrelated)
	waitClosed(t, unrelated.started, "unrelated held job")
	close(unrelated.release)
	waitFinished(t, s, "unrelated")
	sink.none(t)

	blockLate.Store(true)
	late := newAsyncChild("late", "same")
	late.job.GroupScope = scope
	callbackEntered, callbackRelease := make(chan struct{}), make(chan struct{})
	late.job.OnCancelledBeforeStart = func() tool.ExecutionResult {
		close(callbackEntered)
		<-callbackRelease
		return tool.ExecutionResult{Value: Result{AgentID: "late", Status: StatusCancelled}}
	}
	spawnResult := make(chan error, 1)
	go func() { _, _, err := s.Spawn(ctx, late.job); spawnResult <- err }()
	waitClosed(t, events.entered, "blocked acceptance")
	shutdown := make(chan struct{})
	go func() { s.Shutdown(context.Background(), CancelCauseSystem); close(shutdown) }()
	waitClosed(t, shutdown, "shutdown deadline")
	if got := s.SnapshotGroupLedger(scope).Names; len(got) != 2 {
		t.Fatalf("scope ledger during outstanding publication = %v", got)
	}
	first := recv(t, sink.ch, "released held groups")
	if len(first) != 2 || first[0].AgentID != "sibling" || first[1].AgentID != "unrelated" {
		t.Fatalf("shutdown group batch = %+v", first)
	}
	close(events.release)
	if err := recv(t, spawnResult, "late Spawn"); err != nil {
		t.Fatal(err)
	}
	waitClosed(t, callbackEntered, "late cancellation callback")
	second := recv(t, sink.ch, "late grouped completion")
	if len(second) != 1 || second[0].AgentID != "late" || second[0].Status != string(StatusCancelled) {
		t.Fatalf("late completion = %+v", second)
	}
	select {
	case extra := <-sink.ch:
		t.Fatalf("duplicate late completion: %+v", extra)
	default:
	}
	state := jobFor(s, "late")
	if state == nil {
		t.Fatal("late job state missing before finalizer settlement")
	}
	if got := s.SnapshotGroupLedger(scope).Names; len(got) != 2 {
		t.Fatalf("scope ledger lost names before release: %v", got)
	}
	s.MarkDelivered([]string{"call-sibling", "call-unrelated", "call-late"})
	if s.IsPending("sibling") || s.IsPending("unrelated") || s.IsPending("late") {
		t.Fatal("acknowledged completions remain pending")
	}
	s.ReleaseGroupScope(scope)
	if got := s.SnapshotGroupLedger(scope).Names; len(got) != 2 {
		t.Fatalf("scope pruned before finalizer settlement: %v", got)
	}
	close(callbackRelease)
	waitClosed(t, state.settled, "late finalizer settlement")
	if s.IsPending("late") {
		t.Fatal("late job remains pending after acknowledgement")
	}
	if got := s.SnapshotGroupLedger(scope).Names; len(got) != 0 {
		t.Fatalf("released scope remains after safe prune: %v", got)
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
		_, _, err := s.Spawn(agent.WithToolBatchID(context.Background(), "batch"), job.job)
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

package delegation

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tool"
)

type channelSink struct {
	ch chan []agent.SubAgentCompletion
}

func newChannelSink() *channelSink {
	return &channelSink{ch: make(chan []agent.SubAgentCompletion, 32)}
}

func (s *channelSink) DeliverCompletions(batch []agent.SubAgentCompletion) { s.ch <- batch }

func (s *channelSink) none(t *testing.T) {
	t.Helper()
	select {
	case batch := <-s.ch:
		t.Fatalf("unexpected completions posted: %+v", batch)
	default:
	}
}

type queuedEventSink struct {
	mu     sync.Mutex
	events []output.Event
}

func (s *queuedEventSink) Emit(e output.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, e)
}

// asyncChild is a job that signals started, then blocks until release is
// closed or its context is cancelled, returning a delegation Result.
type asyncChild struct {
	job     ChildJob
	started chan struct{}
	release chan struct{}
}

func newAsyncChild(id, group string) *asyncChild {
	c := &asyncChild{started: make(chan struct{}), release: make(chan struct{})}
	c.job = ChildJob{
		AgentID:          id,
		AgentType:        AgentTypeExplore,
		ParentCallID:     "call-" + id,
		Group:            group,
		ObjectivePreview: "objective " + id,
		Execute: func(ctx context.Context) (tool.ExecutionResult, error) {
			close(c.started)
			select {
			case <-c.release:
				return tool.ExecutionResult{Value: Result{AgentID: id, Status: StatusComplete, Output: "out-" + id, TurnCount: 2, TokenCount: 7}}, nil
			case <-ctx.Done():
				return tool.ExecutionResult{Value: Result{AgentID: id, Status: StatusCancelled, Output: "partial-" + id}}, nil
			}
		},
		OnCancelledBeforeStart: func() tool.ExecutionResult {
			return tool.ExecutionResult{Value: Result{AgentID: id, Status: StatusCancelled}}
		},
	}
	return c
}

func spawnAsync(ctx context.Context, t *testing.T, s *Supervisor, c *asyncChild) SpawnTicket {
	t.Helper()
	ticket, err := s.Spawn(ctx, c.job)
	if err != nil {
		t.Fatalf("Spawn(%s): %v", c.job.AgentID, err)
	}
	return ticket
}

func newAsyncSupervisor(maxParallel int, events output.EventSink) (*Supervisor, *channelSink) {
	sink := newChannelSink()
	s := NewSupervisor(SupervisorOptions{MaxParallel: maxParallel, Events: events})
	s.SetCompletionSink(sink)
	return s, sink
}

func TestSupervisorPendingLifecycle(t *testing.T) {
	s, sink := newAsyncSupervisor(1, nil)
	a, b := newAsyncChild("a", ""), newAsyncChild("b", "")
	spawnAsync(context.Background(), t, s, a)
	<-a.started
	spawnAsync(context.Background(), t, s, b)

	want := []agent.PendingSubAgent{
		{AgentID: "a", AgentType: "explore", State: agent.SubAgentRunning},
		{AgentID: "b", AgentType: "explore", State: agent.SubAgentQueued},
	}
	if got := s.Pending(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Pending = %+v, want %+v", got, want)
	}

	close(a.release)
	batch := recv(t, sink.ch, "a completion")
	if len(batch) != 1 || batch[0].AgentID != "a" {
		t.Fatalf("posted = %+v, want a", batch)
	}
	want[0].State = agent.SubAgentFinished
	<-b.started
	want[1].State = agent.SubAgentRunning
	if got := s.Pending(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Pending after post = %+v, want %+v", got, want)
	}
	if !s.IsPending("a") || !s.HasPending() {
		t.Fatal("posted-but-undelivered agent must stay pending")
	}

	s.MarkDelivered([]string{"call-a"})
	if s.IsPending("a") {
		t.Fatal("a still pending after MarkDelivered")
	}
	if got := s.Pending(); len(got) != 1 || got[0].AgentID != "b" {
		t.Fatalf("Pending after MarkDelivered = %+v, want only b", got)
	}

	close(b.release)
	recv(t, sink.ch, "b completion")
	s.MarkDelivered([]string{"call-b"})
	if s.HasPending() {
		t.Fatal("HasPending after all delivered")
	}
}

func TestSupervisorCompletionRecord(t *testing.T) {
	s, sink := newAsyncSupervisor(1, nil)
	a := newAsyncChild("a", "")
	spawnAsync(context.Background(), t, s, a)
	close(a.release)

	c := recv(t, sink.ch, "completion")[0]
	if c.Seq == 0 || c.ParentCallID != "call-a" || c.AgentID != "a" || c.AgentType != "explore" ||
		c.Status != "complete" || c.Quiet || c.ObjectivePreview != "objective a" || c.TurnCount != 2 || c.TokenCount != 7 {
		t.Fatalf("completion = %+v", c)
	}
	if c.Body != `{"output":"out-a"}` {
		t.Fatalf("Body = %q", c.Body)
	}
}

func TestSupervisorQuietFollowsCause(t *testing.T) {
	tests := []struct {
		name      string
		cancel    func(s *Supervisor, id string) CancelOutcome
		finishFst bool
		wantQuiet bool
		wantState string
	}{
		{"user cancel is quiet", func(s *Supervisor, id string) CancelOutcome { return s.CancelAgent(id, false, CancelCauseUser) }, false, true, "cancelled"},
		{"system cancel is not quiet", func(s *Supervisor, id string) CancelOutcome { return s.CancelAgent(id, false, CancelCauseSystem) }, false, false, "cancelled"},
		{"lost race is a normal completion", func(s *Supervisor, id string) CancelOutcome { return s.CancelAgent(id, false, CancelCauseUser) }, true, false, "complete"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, sink := newAsyncSupervisor(1, nil)
			a := newAsyncChild("a", "")
			spawnAsync(context.Background(), t, s, a)
			<-a.started
			if tt.finishFst {
				close(a.release)
				c := recv(t, sink.ch, "completion")[0]
				if got := tt.cancel(s, "a"); got != CancelAlreadyFinished {
					t.Fatalf("late cancel outcome = %v, want CancelAlreadyFinished", got)
				}
				if c.Quiet || c.Status != tt.wantState {
					t.Fatalf("completion = %+v", c)
				}
				return
			}
			if got := tt.cancel(s, "a"); got != CancelAccepted {
				t.Fatalf("cancel outcome = %v", got)
			}
			c := recv(t, sink.ch, "completion")[0]
			if c.Quiet != tt.wantQuiet || c.Status != tt.wantState {
				t.Fatalf("completion = %+v, want quiet=%v status=%s", c, tt.wantQuiet, tt.wantState)
			}
		})
	}
}

func TestSupervisorQueuedCancelCompletionIsQuiet(t *testing.T) {
	s, sink := newAsyncSupervisor(1, nil)
	a, b := newAsyncChild("a", ""), newAsyncChild("b", "")
	spawnAsync(context.Background(), t, s, a)
	<-a.started
	spawnAsync(context.Background(), t, s, b)

	if got := s.CancelAgent("b", false, CancelCauseUser); got != CancelAccepted {
		t.Fatalf("cancel queued = %v", got)
	}
	c := recv(t, sink.ch, "queued completion")[0]
	if c.AgentID != "b" || c.Status != "cancelled" || !c.Quiet {
		t.Fatalf("completion = %+v", c)
	}
	close(a.release)
	recv(t, sink.ch, "a completion")
}

func TestSupervisorDelegationQueuedEvent(t *testing.T) {
	events := &queuedEventSink{}
	s, sink := newAsyncSupervisor(1, events)
	a, b := newAsyncChild("a", ""), newAsyncChild("b", "")

	if ticket := spawnAsync(context.Background(), t, s, a); ticket.Queued || ticket.AgentID != "a" {
		t.Fatalf("first ticket = %+v", ticket)
	}
	<-a.started
	if len(events.events) != 0 {
		t.Fatalf("event emitted for a started job: %+v", events.events)
	}
	if ticket := spawnAsync(context.Background(), t, s, b); !ticket.Queued {
		t.Fatalf("second ticket = %+v, want queued", ticket)
	}

	events.mu.Lock()
	got := append([]output.Event(nil), events.events...)
	events.mu.Unlock()
	if len(got) != 1 || got[0].Type != output.EventTypeDelegationQueued {
		t.Fatalf("events = %+v, want one delegation_queued", got)
	}
	payload, ok := got[0].Payload.(output.DelegationQueuedEvent)
	if !ok || payload.AgentID != "b" || payload.CallID != "call-b" || payload.AgentType != "explore" || payload.TaskPreview != "objective b" {
		t.Fatalf("payload = %#v", got[0].Payload)
	}
	close(a.release)
	close(b.release)
	recv(t, sink.ch, "a")
	recv(t, sink.ch, "b")
}

func TestSupervisorLedgerIncludesWorktreeAfterDequeue(t *testing.T) {
	s, sink := newAsyncSupervisor(1, nil)
	provisioned := make(chan struct{})
	release := make(chan struct{})
	prepared := make(chan struct{})
	job := ChildJob{
		AgentID: "w", AgentType: AgentTypeCode, ParentCallID: "call-w", Group: "g",
		Prepare: func(context.Context) (CodeWorktree, error) {
			<-prepared
			return CodeWorktree{Path: "/wt/w", Branch: "delegate/w"}, nil
		},
		Execute: func(context.Context) (tool.ExecutionResult, error) {
			close(provisioned)
			<-release
			return tool.ExecutionResult{Value: Result{AgentID: "w", Status: StatusComplete}}, nil
		},
	}
	if _, err := s.Spawn(context.Background(), job); err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	want := agent.SubAgentLedgerEntry{AgentID: "w", AgentType: "code", ParentCallID: "call-w", Group: "g"}
	if got := s.Ledger(); !reflect.DeepEqual(got, []agent.SubAgentLedgerEntry{want}) {
		t.Fatalf("Ledger before provisioning = %+v", got)
	}
	close(prepared)
	<-provisioned
	want.WorktreePath = "/wt/w"
	if got := s.Ledger(); !reflect.DeepEqual(got, []agent.SubAgentLedgerEntry{want}) {
		t.Fatalf("Ledger after provisioning = %+v, want %+v", got, want)
	}
	if paths := s.controller.ActiveAgentIDs(); len(paths) != 1 {
		t.Fatalf("controller active = %v, want the running job", paths)
	}
	close(release)
	if c := recv(t, sink.ch, "completion")[0]; c.Status != "complete" {
		t.Fatalf("completion = %+v", c)
	}
	s.MarkDelivered([]string{"call-w"})
	if len(s.Ledger()) != 0 {
		t.Fatalf("Ledger after delivery = %+v, want empty", s.Ledger())
	}
}

func TestSupervisorPrepareFailureProducesFinalResult(t *testing.T) {
	s, sink := newAsyncSupervisor(1, nil)
	boom := errors.New("provision blew up")
	job := ChildJob{
		AgentID: "p", AgentType: AgentTypeCode, ParentCallID: "call-p",
		Prepare: func(context.Context) (CodeWorktree, error) { return CodeWorktree{}, boom },
		Execute: func(context.Context) (tool.ExecutionResult, error) {
			t.Error("Execute ran after Prepare failed")
			return tool.ExecutionResult{}, nil
		},
	}
	if _, err := s.Spawn(context.Background(), job); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	c := recv(t, sink.ch, "completion")[0]
	if c.Status != "failed" || c.Quiet || c.AgentID != "p" {
		t.Fatalf("completion = %+v", c)
	}
	if !s.IsPending("p") {
		t.Fatal("failed agent must stay pending until delivered")
	}
}

func TestSupervisorSpawnAndWaitPostsNothing(t *testing.T) {
	s, sink := newAsyncSupervisor(1, nil)
	a := newAsyncChild("a", "g")
	done := spawn(context.Background(), s, a.job)
	<-a.started
	if !s.IsPending("a") {
		t.Fatal("running blocking job must be pending")
	}
	close(a.release)
	recv(t, done, "SpawnAndWait")
	if s.IsPending("a") || s.HasPending() {
		t.Fatal("blocking caller must mark delivered on return")
	}
	sink.none(t)
}

func TestSupervisorNilSinkPostsNothing(t *testing.T) {
	s := NewSupervisor(SupervisorOptions{MaxParallel: 1})
	a := newAsyncChild("a", "g")
	spawnAsync(agent.WithToolBatchID(context.Background(), "b1"), t, s, a)
	close(a.release)
	waitFinished(t, s, "a")
	s.SealBatch("b1")
	s.MarkDelivered([]string{"call-a"})
	if s.IsPending("a") {
		t.Fatal("ungrouped nil-sink completion should be ackable")
	}
}

func TestSupervisorShutdownPostsUnjoinedOnce(t *testing.T) {
	s := NewSupervisor(SupervisorOptions{MaxParallel: 1, JoinTimeout: 1})
	sink := newChannelSink()
	s.SetCompletionSink(sink)
	stuck := make(chan struct{})
	job := ChildJob{
		AgentID: "stuck", AgentType: AgentTypeCode, ParentCallID: "call-stuck", Worktree: CodeWorktree{Path: "/wt/stuck"},
		Execute: func(context.Context) (tool.ExecutionResult, error) {
			<-stuck
			return tool.ExecutionResult{Value: Result{Status: StatusComplete}}, nil
		},
	}
	if _, err := s.Spawn(context.Background(), job); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	report := s.Shutdown(context.Background(), CancelCauseSystem)
	if len(report.Unjoined) != 1 || report.Unjoined[0].WorktreePath != "/wt/stuck" {
		t.Fatalf("report = %+v", report)
	}
	batch := recv(t, sink.ch, "shutdown completion")
	if len(batch) != 1 || batch[0].AgentID != "stuck" || batch[0].Status != "cancelled" || !batch[0].Quiet {
		t.Fatalf("batch = %+v", batch)
	}
	s.Shutdown(context.Background(), CancelCauseSystem)
	s.mu.Lock()
	exited := s.jobs["stuck"].exited
	s.mu.Unlock()
	close(stuck)
	<-exited
	sink.none(t)
	if !s.IsPending("stuck") {
		t.Fatal("unjoined agent stays pending until delivered")
	}
}

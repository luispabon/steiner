package delegation

import (
	"context"
	"errors"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tool"
)

// delegationOccurrenceOf extracts the embedded occurrence of a lifecycle event.
func delegationOccurrenceOf(t *testing.T, e output.Event) output.DelegationOccurrence {
	t.Helper()
	switch p := e.Payload.(type) {
	case output.DelegationAcceptedEvent:
		return p.DelegationOccurrence
	case output.DelegationQueuedEvent:
		return p.DelegationOccurrence
	case output.DelegationStartedEvent:
		return p.DelegationOccurrence
	case output.DelegationCompleteEvent:
		return p.DelegationOccurrence
	case output.DelegationFailedEvent:
		return p.DelegationOccurrence
	case output.DelegationCacheWaitingEvent:
		return p.DelegationOccurrence
	}
	t.Fatalf("event %s (%T) is not a delegation lifecycle event", e.Type, e.Payload)
	return output.DelegationOccurrence{}
}

func (s *queuedEventSink) snapshot() []output.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]output.Event(nil), s.events...)
}

func countAccepted(events []output.Event, agentID string) int {
	n := 0
	for _, e := range events {
		if p, ok := e.Payload.(output.DelegationAcceptedEvent); ok && p.AgentID == agentID {
			n++
		}
	}
	return n
}

func TestLifecycleEventsCarryIdenticalOccurrence(t *testing.T) {
	tests := []struct {
		name      string
		async     bool
		queued    bool
		runErr    error
		wantTypes []string
	}{
		{"blocking complete", false, false, nil, []string{output.EventTypeDelegationAccepted, output.EventTypeDelegationStarted, output.EventTypeDelegationComplete}},
		{"blocking failed", false, false, errors.New("runner failed"), []string{output.EventTypeDelegationAccepted, output.EventTypeDelegationStarted, output.EventTypeDelegationFailed}},
		{"async queued complete", true, true, nil, []string{output.EventTypeDelegationAccepted, output.EventTypeDelegationQueued, output.EventTypeDelegationStarted, output.EventTypeDelegationComplete}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			events := &queuedEventSink{}
			deps := minimalDeps(&mockRunner{runFunc: func(context.Context, agent.RunRequest) (agent.RunState, error) {
				if tt.runErr != nil {
					return agent.RunState{}, tt.runErr
				}
				return successRunState(), nil
			}})
			deps.Events = events
			sup, sink := newAsyncSupervisor(1)
			deps.Supervisor = sup
			deps.AsyncSubAgents = tt.async
			blockerRelease := make(chan struct{})
			if tt.queued {
				blockerStarted := make(chan struct{})
				if _, _, err := sup.Spawn(context.Background(), ChildJob{AgentID: "blocker", Execute: func(context.Context) (tool.ExecutionResult, error) {
					close(blockerStarted)
					<-blockerRelease
					return tool.ExecutionResult{}, nil
				}}); err != nil {
					t.Fatal(err)
				}
				<-blockerStarted
			}

			batch := testBatchID(42)
			ctx := context.WithValue(agent.WithToolBatchID(context.Background(), batch), tool.ExecutionCallIDKey{}, "call-xyz")
			got, err := SubAgentToolDef(deps, nil).Handler(ctx, subAgentTask(AgentTypeExplore, "inspect"))
			if err != nil {
				t.Fatalf("handler: %v", err)
			}
			result, ok := got.(tool.ExecutionResult)
			if !ok || result.DelegationAdmission == nil || result.DelegationAdmission.AgentID == "" {
				t.Fatalf("handler result = %#v, want accepted admission", got)
			}
			agentID := result.DelegationAdmission.AgentID
			if tt.async {
				close(blockerRelease)
				recv(t, sink.ch, "completion")
				recv(t, sink.ch, "completion") // blocker completes first, then the child; order is irrelevant
			}

			want := output.DelegationOccurrence{CallID: "call-xyz", BatchID: batch, AgentID: agentID}
			var gotTypes []string
			for _, e := range events.snapshot() {
				switch e.Type {
				case output.EventTypeDelegationAccepted, output.EventTypeDelegationQueued, output.EventTypeDelegationStarted, output.EventTypeDelegationComplete, output.EventTypeDelegationFailed:
				default:
					continue
				}
				occ := delegationOccurrenceOf(t, e)
				if occ.AgentID != agentID {
					continue // the blocker's own events
				}
				gotTypes = append(gotTypes, e.Type)
				if occ != want {
					t.Errorf("%s occurrence = %+v, want %+v", e.Type, occ, want)
				}
			}
			if len(gotTypes) != len(tt.wantTypes) {
				t.Fatalf("lifecycle types = %v, want %v", gotTypes, tt.wantTypes)
			}
			for i := range gotTypes {
				if gotTypes[i] != tt.wantTypes[i] {
					t.Fatalf("lifecycle types = %v, want %v", gotTypes, tt.wantTypes)
				}
			}
		})
	}
}

func TestSupervisorEmitsAcceptedExactlyOncePerAcceptedCall(t *testing.T) {
	t.Run("blocking success", func(t *testing.T) {
		events := &queuedEventSink{}
		s := NewSupervisor(SupervisorOptions{MaxParallel: 1})
		c := newFakeChild("a", false)
		c.job.Events = events
		close(c.release)
		if _, err := s.SpawnAndWait(context.Background(), c.job); err != nil {
			t.Fatal(err)
		}
		if n := countAccepted(events.snapshot(), "a"); n != 1 {
			t.Fatalf("accepted events = %d, want 1", n)
		}
	})
	t.Run("async preparation failure", func(t *testing.T) {
		events := &queuedEventSink{}
		s, sink := newAsyncSupervisor(1)
		job := ChildJob{
			AgentID: "p", AgentType: AgentTypeCode, ParentCallID: "call-p", Events: events,
			Prepare: func(context.Context) (CodeWorktree, error) { return CodeWorktree{}, errors.New("boom") },
			Execute: func(context.Context) (tool.ExecutionResult, error) {
				t.Error("Execute ran after Prepare failed")
				return tool.ExecutionResult{}, nil
			},
		}
		if _, _, err := s.Spawn(context.Background(), job); err != nil {
			t.Fatal(err)
		}
		recv(t, sink.ch, "completion")
		if n := countAccepted(events.snapshot(), "p"); n != 1 {
			t.Fatalf("accepted events = %d, want 1", n)
		}
	})
	t.Run("cancelled before start", func(t *testing.T) {
		events := &queuedEventSink{}
		s, sink := newAsyncSupervisor(1)
		a, b := newAsyncChild("a", ""), newAsyncChild("b", "")
		a.job.Events, b.job.Events = events, events
		spawnAsync(context.Background(), t, s, a)
		<-a.started
		if ticket := spawnAsync(context.Background(), t, s, b); !ticket.Queued {
			t.Fatalf("ticket = %+v, want queued", ticket)
		}
		s.CancelAgent("b", false, CancelCauseUser)
		recv(t, sink.ch, "cancelled completion")
		close(a.release)
		recv(t, sink.ch, "a completion")
		got := events.snapshot()
		if n := countAccepted(got, "a"); n != 1 {
			t.Fatalf("accepted for a = %d, want 1", n)
		}
		if n := countAccepted(got, "b"); n != 1 {
			t.Fatalf("accepted for cancelled b = %d, want 1", n)
		}
	})
	t.Run("rejected calls emit none", func(t *testing.T) {
		events := &queuedEventSink{}
		s, sink := newAsyncSupervisor(1)
		a := newAsyncChild("a", "")
		a.job.Events = events
		spawnAsync(context.Background(), t, s, a)
		<-a.started
		dup := newAsyncChild("a", "")
		dup.job.Events = events
		if _, _, err := s.Spawn(context.Background(), dup.job); !errors.Is(err, ErrAgentAlreadyActive) {
			t.Fatalf("duplicate err = %v, want ErrAgentAlreadyActive", err)
		}
		b := newAsyncChild("b", "")
		b.job.Events = events
		spawnAsync(context.Background(), t, s, b) // fills the outstanding cap of 2
		over := newAsyncChild("over", "")
		over.job.Events = events
		if _, _, err := s.Spawn(context.Background(), over.job); !errors.Is(err, ErrOutstandingCap) {
			t.Fatalf("over err = %v, want ErrOutstandingCap", err)
		}
		got := events.snapshot()
		if n := countAccepted(got, "over"); n != 0 {
			t.Fatalf("accepted for rejected over = %d, want 0", n)
		}
		if n := countAccepted(got, "a"); n != 1 {
			t.Fatalf("accepted for a = %d, want 1 (duplicate must not add one)", n)
		}
		close(a.release)
		close(b.release)
		recv(t, sink.ch, "a")
		recv(t, sink.ch, "b")
	})
}

func TestSupervisorCompletionCarriesBatchID(t *testing.T) {
	s, sink := newAsyncSupervisor(1)
	c := newAsyncChild("a", "")
	ctx := agent.WithToolBatchID(context.Background(), testBatchID(7))
	spawnAsync(ctx, t, s, c)
	close(c.release)
	got := recv(t, sink.ch, "completion")[0]
	if got.BatchID != testBatchID(7) || got.ParentCallID != "call-a" {
		t.Fatalf("completion identity = %q/%q, want %q/call-a", got.BatchID, got.ParentCallID, testBatchID(7))
	}
	if led := s.Ledger(); len(led) != 1 || led[0].BatchID != testBatchID(7) {
		t.Fatalf("ledger = %+v, want batch %q", led, testBatchID(7))
	}
}

func TestFollowUpHandlerStampsCurrentBatchNotOriginal(t *testing.T) {
	store := NewSessionStore()
	store.Save(&ChildSession{
		Spec: Spec{AgentID: "child-b", AgentType: AgentTypeReview, Task: "inspect", ParentCallID: "original-call", BatchID: "original-batch"},
		Request: agent.RunRequest{
			Prompt: promptWithConversation("initial task"),
			Limits: agent.Limits{MaxTurns: 1, MaxTokens: 1},
		},
		Conversation: []agent.Message{
			{Role: agent.MessageRoleUser, Content: "initial task"},
			{Role: agent.MessageRoleAssistant, Content: "first answer"},
		},
		TurnCount: 1,
	})
	events := &queuedEventSink{}
	handler := NewFollowUpHandler(SubAgentHandlerDeps{
		SubAgentCfg:  config.SubAgentConfig{MaxTurns: 5, MaxTokens: 50, MaxFollowUps: 100},
		Events:       events,
		SessionStore: store,
		Runner: &mockRunner{runFunc: func(_ context.Context, req agent.RunRequest) (agent.RunState, error) {
			return agent.RunState{Conversation: providerToAgentMessages(req.Prompt.Conversation), TurnCount: 2, StopReason: agent.StopReasonComplete}, nil
		}},
	})
	ctx := context.WithValue(agent.WithToolBatchID(context.Background(), "current-batch"), tool.ExecutionCallIDKey{}, "follow-call")
	if _, err := handler(ctx, map[string]any{"agent_id": "child-b", "message": "continue"}); err != nil {
		t.Fatalf("handler: %v", err)
	}
	want := output.DelegationOccurrence{CallID: "follow-call", BatchID: "current-batch", AgentID: "child-b"}
	seen := 0
	for _, e := range events.snapshot() {
		switch e.Type {
		case output.EventTypeDelegationStarted, output.EventTypeDelegationComplete, output.EventTypeDelegationFailed:
			seen++
			if occ := delegationOccurrenceOf(t, e); occ != want {
				t.Errorf("%s occurrence = %+v, want %+v", e.Type, occ, want)
			}
		}
	}
	if seen < 2 {
		t.Fatalf("saw %d lifecycle events, want at least started and a terminal one", seen)
	}
}

func TestSupervisorEmitsAcceptedAndQueuedOnEachJobsOwnSink(t *testing.T) {
	sinkA, sinkB := &queuedEventSink{}, &queuedEventSink{}
	s, sink := newAsyncSupervisor(1)
	a, b := newAsyncChild("a", ""), newAsyncChild("b", "")
	a.job.Events, b.job.Events = sinkA, sinkB
	spawnAsync(context.Background(), t, s, a)
	<-a.started
	if ticket := spawnAsync(context.Background(), t, s, b); !ticket.Queued {
		t.Fatalf("ticket = %+v, want queued", ticket)
	}
	close(a.release)
	recv(t, sink.ch, "a completion")
	<-b.started
	close(b.release)
	recv(t, sink.ch, "b completion")

	typesFor := func(events []output.Event) []string {
		var out []string
		for _, e := range events {
			out = append(out, e.Type)
		}
		return out
	}
	if got := typesFor(sinkA.snapshot()); len(got) != 1 || got[0] != output.EventTypeDelegationAccepted {
		t.Fatalf("job a sink events = %v, want [accepted]", got)
	}
	got := typesFor(sinkB.snapshot())
	if len(got) != 2 || got[0] != output.EventTypeDelegationAccepted || got[1] != output.EventTypeDelegationQueued {
		t.Fatalf("job b sink events = %v, want [accepted queued]", got)
	}
	if n := countAccepted(sinkA.snapshot(), "b") + countAccepted(sinkB.snapshot(), "a"); n != 0 {
		t.Fatalf("events leaked across job sinks: %d", n)
	}
}

func TestSupervisorWithoutJobSinkEmitsNothing(t *testing.T) {
	s, sink := newAsyncSupervisor(1)
	c := newAsyncChild("a", "")
	spawnAsync(context.Background(), t, s, c)
	close(c.release)
	recv(t, sink.ch, "completion")
}

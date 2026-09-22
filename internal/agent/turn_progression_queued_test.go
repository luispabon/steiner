package agent

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

type eventRecorder struct {
	mu     sync.Mutex
	events []output.Event
}

func (r *eventRecorder) sink() output.EventSink {
	return output.SinkFunc(func(event output.Event) {
		r.mu.Lock()
		r.events = append(r.events, event)
		r.mu.Unlock()
	})
}

func (r *eventRecorder) snapshot() []output.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]output.Event(nil), r.events...)
}

func delegationClassifier(names ...string) func(string) ParallelClass {
	delegations := make(map[string]bool, len(names))
	for _, name := range names {
		delegations[name] = true
	}
	return func(name string) ParallelClass {
		if delegations[name] {
			return ParallelClassDelegation
		}
		return ParallelClassNone
	}
}

func eventIndexes(events []output.Event, eventType string) []int {
	var idx []int
	for i, event := range events {
		if event.Type == eventType {
			idx = append(idx, i)
		}
	}
	return idx
}

func queuedCallIDs(events []output.Event) []string {
	var ids []string
	for _, event := range events {
		payload, ok := event.Payload.(output.ToolCallQueuedEvent)
		if !ok {
			continue
		}
		ids = append(ids, payload.CallID)
	}
	return ids
}

func finishedPayloads(events []output.Event) map[string]output.ToolCallFinishedEvent {
	out := map[string]output.ToolCallFinishedEvent{}
	for _, event := range events {
		payload, ok := event.Payload.(output.ToolCallFinishedEvent)
		if !ok {
			continue
		}
		out[payload.CallID] = payload
	}
	return out
}

func TestExecuteToolCalls_QueuesAllDelegationCallsBeforeDispatch(t *testing.T) {
	recorder := &eventRecorder{}
	executor := parallelTestExecutor{fn: func(_ context.Context, name string) (any, error) { return name, nil }}
	p := newTurnProgressor(RunRequest{
		Executor:               executor,
		ParallelClassOf:        delegationClassifier("code", "sub_agent"),
		MaxParallelTools:       2,
		MaxParallelDelegations: 2,
		Events:                 recorder.sink(),
	}, prompt.AssemblyOptions{}, nil)

	p.executeToolCalls(context.Background(), RunState{Lineage: newConversationLineage(nil)}, parallelCalls("read", "code", "sub_agent"))
	events := recorder.snapshot()

	queued := queuedCallIDs(events)
	if len(queued) != 2 || queued[0] != "code" || queued[1] != "sub_agent" {
		t.Fatalf("queued calls = %v, want [code sub_agent] (regular tools must not queue)", queued)
	}
	queuedIdx := eventIndexes(events, output.EventTypeToolCallQueued)
	startedIdx := eventIndexes(events, output.EventTypeToolCallStarted)
	if len(startedIdx) != 3 {
		t.Fatalf("started events = %d, want 3", len(startedIdx))
	}
	if queuedIdx[len(queuedIdx)-1] > startedIdx[0] {
		t.Fatalf("queued event at %d emitted after first start at %d, want all queued first", queuedIdx[len(queuedIdx)-1], startedIdx[0])
	}
	if got := len(eventIndexes(events, output.EventTypeToolCallFinished)); got != 3 {
		t.Fatalf("finished events = %d, want 3", got)
	}
}

func TestExecuteToolCalls_QueuesSerialDelegationFallback(t *testing.T) {
	recorder := &eventRecorder{}
	executor := parallelTestExecutor{fn: func(_ context.Context, name string) (any, error) { return name, nil }}
	p := newTurnProgressor(RunRequest{
		Executor:               executor,
		ParallelClassOf:        delegationClassifier("code"),
		MaxParallelDelegations: 1,
		Events:                 recorder.sink(),
	}, prompt.AssemblyOptions{}, nil)

	p.executeToolCalls(context.Background(), RunState{Lineage: newConversationLineage(nil)}, parallelCalls("code", "code", "code"))
	events := recorder.snapshot()

	if queued := queuedCallIDs(events); len(queued) != 3 {
		t.Fatalf("queued calls = %v, want 3 (serial fallback must still show every waiting call)", queued)
	}
	queuedIdx := eventIndexes(events, output.EventTypeToolCallQueued)
	startedIdx := eventIndexes(events, output.EventTypeToolCallStarted)
	if len(startedIdx) != 3 {
		t.Fatalf("started events = %d, want 3", len(startedIdx))
	}
	if queuedIdx[len(queuedIdx)-1] > startedIdx[0] {
		t.Fatalf("queued event at %d emitted after first start at %d, want all queued first", queuedIdx[len(queuedIdx)-1], startedIdx[0])
	}
}

func TestExecuteToolCalls_QueuedCallNotDispatchedGetsFinishedError(t *testing.T) {
	recorder := &eventRecorder{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan struct{}, 1)
	executor := parallelTestExecutor{fn: func(ctx context.Context, _ string) (any, error) {
		entered <- struct{}{}
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	p := newTurnProgressor(RunRequest{
		Executor:               executor,
		ParallelClassOf:        delegationClassifier("code"),
		MaxParallelDelegations: 1,
		Events:                 recorder.sink(),
	}, prompt.AssemblyOptions{}, nil)

	calls := []provider.ToolCall{{ID: "c1", Name: "code"}, {ID: "c2", Name: "code"}}
	conversation := []Message{{Role: MessageRoleAssistant, ToolCalls: []ToolCall{{ID: "c1", Name: "code"}, {ID: "c2", Name: "code"}}}}
	state := RunState{Conversation: conversation, Lineage: newConversationLineage(conversation)}
	response := provider.ChatResponse{Message: provider.Message{ToolCalls: calls}}

	done := make(chan turnOutcome, 1)
	go func() { done <- p.executeToolCalls(ctx, state, response) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first delegation call did not enter")
	}
	cancel()
	outcome := <-done
	if !outcome.Stop {
		t.Fatal("outcome.Stop = false, want true after cancellation")
	}

	events := recorder.snapshot()
	queued := queuedCallIDs(events)
	if len(queued) != 2 || queued[0] != "c1" || queued[1] != "c2" {
		t.Fatalf("queued calls = %v, want [c1 c2]", queued)
	}
	started := eventIndexes(events, output.EventTypeToolCallStarted)
	if len(started) != 1 {
		t.Fatalf("started events = %d, want 1 (only the dispatched call)", len(started))
	}
	finished := finishedPayloads(events)
	undispatched, ok := finished["c2"]
	if !ok {
		t.Fatal("no tool_call_finished event for undispatched queued call c2")
	}
	if !strings.Contains(undispatched.Error, "not dispatched") {
		t.Fatalf("c2 finished error = %q, want not-dispatched error", undispatched.Error)
	}
	c2FinishedIdx, stopIdx := -1, -1
	for i, event := range events {
		switch payload := event.Payload.(type) {
		case output.ToolCallFinishedEvent:
			if payload.CallID == "c2" {
				c2FinishedIdx = i
			}
		case output.StopReasonEvent:
			stopIdx = i
		}
	}
	if c2FinishedIdx < 0 || stopIdx < 0 || c2FinishedIdx > stopIdx {
		t.Fatalf("c2 finished at %d, stop_reason at %d, want finished before stop", c2FinishedIdx, stopIdx)
	}
}

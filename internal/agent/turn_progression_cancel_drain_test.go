package agent

import (
	"context"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
)

func TestExecuteToolCalls_CancelWhileBlockedOnAcquire(t *testing.T) {
	entered := make(chan string, 2)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	executor := parallelTestExecutor{fn: func(ctx context.Context, name string) (any, error) {
		entered <- name
		<-ctx.Done()
		return "ok-" + name, nil
	}}
	var events []output.Event
	var eventsMu sync.Mutex
	p := newTurnProgressor(RunRequest{
		Executor:         executor,
		ParallelTool:     func(string) bool { return true },
		MaxParallelTools: 2,
		Events: output.SinkFunc(func(event output.Event) {
			eventsMu.Lock()
			events = append(events, event)
			eventsMu.Unlock()
		}),
	}, prompt.AssemblyOptions{}, nil)
	state := cancelDrainState("a", "b", "c")
	done := make(chan turnOutcome, 1)
	go func() {
		done <- p.executeToolCalls(ctx, state, parallelCalls("a", "b", "c"))
	}()
	for range 2 {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("launched calls did not enter")
		}
	}
	cancel()
	outcome := <-done

	assertCancelledDrain(t, outcome, []string{"a", "b", "c"})
	toolMessages := cancelDrainToolMessages(outcome.State.Conversation)
	if len(toolMessages) != 3 || toolMessages[0].Content != "ok-a" || toolMessages[1].Content != "ok-b" || !strings.Contains(toolMessages[2].Content, "not dispatched") {
		t.Fatalf("tool messages = %#v, want real a/b results and not-dispatched c", toolMessages)
	}
	if got := cancelDrainToolMessages(outcome.State.Lineage.FullMessages()); len(got) != 3 || got[0].ToolCallID != "a" || got[1].ToolCallID != "b" || got[2].ToolCallID != "c" {
		t.Fatalf("lineage tool messages = %#v, want all calls in order", got)
	}
	eventsMu.Lock()
	eventsCopy := append([]output.Event(nil), events...)
	eventsMu.Unlock()
	assertCancelDrainEvents(t, eventsCopy, []string{"a", "b"}, "c")
}

func TestExecuteToolCalls_CancelAfterAllExecutorsReturn(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	executor := parallelTestExecutor{fn: func(ctx context.Context, name string) (any, error) {
		if name == "c" {
			ctx.Value(cancelContextKey{}).(context.CancelFunc)()
		}
		return "late-" + name, nil
	}}
	p := newTurnProgressor(RunRequest{
		Executor:         executor,
		ParallelTool:     func(string) bool { return true },
		MaxParallelTools: 3,
	}, prompt.AssemblyOptions{}, nil)
	ctx = context.WithValue(ctx, cancelContextKey{}, cancel)
	outcome := p.executeToolCalls(ctx, cancelDrainState("a", "b", "c"), parallelCalls("a", "b", "c"))

	assertCancelledDrain(t, outcome, []string{"a", "b", "c"})
	for i, message := range cancelDrainToolMessages(outcome.State.Conversation) {
		want := "late-" + string(rune('a'+i))
		if message.Content != want {
			t.Errorf("tool message %d content = %q, want %q", i, message.Content, want)
		}
	}
}

func TestExecuteToolCalls_SerialCancelRecordsCompletedResult(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	executor := parallelTestExecutor{fn: func(ctx context.Context, _ string) (any, error) {
		ctx.Value(cancelContextKey{}).(context.CancelFunc)()
		return "serial-value", nil
	}}
	ctx = context.WithValue(ctx, cancelContextKey{}, cancel)
	p := newTurnProgressor(RunRequest{Executor: executor}, prompt.AssemblyOptions{}, nil)
	outcome := p.executeToolCalls(ctx, cancelDrainState("a"), parallelCalls("a"))

	assertCancelledDrain(t, outcome, []string{"a"})
	messages := cancelDrainToolMessages(outcome.State.Conversation)
	if len(messages) != 1 || messages[0].Content != "serial-value" {
		t.Fatalf("tool messages = %#v, want completed serial result", messages)
	}
}

func TestExecuteToolCalls_NilSuccessUnderDrain(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	executor := parallelTestExecutor{fn: func(ctx context.Context, name string) (any, error) {
		if name == "b" {
			ctx.Value(cancelContextKey{}).(context.CancelFunc)()
		}
		return nil, nil
	}}
	ctx = context.WithValue(ctx, cancelContextKey{}, cancel)
	p := newTurnProgressor(RunRequest{
		Executor:         executor,
		ParallelTool:     func(string) bool { return true },
		MaxParallelTools: 3,
	}, prompt.AssemblyOptions{}, nil)
	outcome := p.executeToolCalls(ctx, cancelDrainState("a", "b", "c"), parallelCalls("a", "b", "c"))

	assertCancelledDrain(t, outcome, []string{"a", "b", "c"})
	messages := cancelDrainToolMessages(outcome.State.Conversation)
	if len(messages) != 3 {
		t.Fatalf("tool message count = %d, want 3", len(messages))
	}
	for i, wantID := range []string{"a", "b", "c"} {
		if messages[i].ToolCallID != wantID {
			t.Errorf("tool message %d ID = %q, want %q", i, messages[i].ToolCallID, wantID)
		}
	}
}

func cancelDrainState(names ...string) RunState {
	response := parallelCalls(names...)
	calls := make([]ToolCall, len(response.Message.ToolCalls))
	for i, call := range response.Message.ToolCalls {
		calls[i] = ToolCall{ID: call.ID, Name: call.Name}
	}
	conversation := []Message{{Role: MessageRoleAssistant, ToolCalls: calls}}
	return RunState{Conversation: conversation, Lineage: newConversationLineage(conversation)}
}

func cancelDrainToolMessages(messages []Message) []Message {
	var tools []Message
	for _, message := range messages {
		if message.Role == MessageRoleTool {
			tools = append(tools, message)
		}
	}
	return tools
}

func assertCancelledDrain(t *testing.T, outcome turnOutcome, callIDs []string) {
	t.Helper()
	if !outcome.Stop {
		t.Fatal("outcome.Stop = false, want true")
	}
	if outcome.State.StopReason != StopReasonCancelled {
		t.Fatalf("StopReason = %q, want %q", outcome.State.StopReason, StopReasonCancelled)
	}
	if got := ReplaySafeConversation(outcome.State.Conversation); !reflect.DeepEqual(got, outcome.State.Conversation) {
		t.Fatalf("replay sanitization changed conversation: got %#v, want %#v", got, outcome.State.Conversation)
	}
	lineage := outcome.State.Lineage.FullMessages()
	tools := cancelDrainToolMessages(lineage)
	if len(tools) != len(callIDs) {
		t.Fatalf("lineage has %d tool messages, want %d", len(tools), len(callIDs))
	}
	for i, callID := range callIDs {
		if tools[i].ToolCallID != callID {
			t.Fatalf("lineage tool message %d ID = %q, want %q", i, tools[i].ToolCallID, callID)
		}
	}
}

func assertCancelDrainEvents(t *testing.T, events []output.Event, launched []string, absent string) {
	t.Helper()
	started := map[string]int{}
	finished := map[string]int{}
	for _, event := range events {
		switch event.Type {
		case output.EventTypeToolCallStarted:
			payload, ok := event.Payload.(output.ToolCallStartedEvent)
			if !ok {
				t.Fatalf("started payload = %#v, want ToolCallStartedEvent", event.Payload)
			}
			started[payload.CallID]++
		case output.EventTypeToolCallFinished:
			payload, ok := event.Payload.(output.ToolCallFinishedEvent)
			if !ok {
				t.Fatalf("finished payload = %#v, want ToolCallFinishedEvent", event.Payload)
			}
			finished[payload.CallID]++
			if payload.Result == "" {
				t.Errorf("finished %q has empty result", payload.CallID)
			}
		}
	}
	if len(started) != len(launched) || len(finished) != len(launched) {
		t.Errorf("event call counts = started %d, finished %d, want %d each", len(started), len(finished), len(launched))
	}
	for _, callID := range launched {
		if started[callID] != 1 || finished[callID] != 1 {
			t.Errorf("call %q events = started %d, finished %d, want one each", callID, started[callID], finished[callID])
		}
	}
	if started[absent] != 0 || finished[absent] != 0 {
		t.Errorf("not-dispatched call %q events = started %d, finished %d, want zero", absent, started[absent], finished[absent])
	}
}

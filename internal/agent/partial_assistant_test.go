package agent

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

func partialAssistantRunRequest(p provider.Provider, events *[]output.Event) RunRequest {
	return RunRequest{
		Provider: p,
		Executor: &fakeExecutor{},
		ResolvedModel: provider.ResolvedModel{
			Alias: "luna", ProviderAlias: "codex", BackendModelID: "luna",
		},
		Prompt: prompt.AssemblyOptions{Conversation: []provider.Message{{
			Role: provider.MessageRoleUser, Content: "summarise the report",
		}}},
		Limits:             Limits{MaxTurns: 6, MaxTokens: 100000},
		StreamingPreferred: true,
		Events:             output.SinkFunc(func(event output.Event) { *events = append(*events, event) }),
	}
}

// streamScriptProvider answers each stream call with the next script. Every
// script runs in order, so a test can describe a failing attempt followed by a
// successful retry.
func streamScriptProvider(scripts ...func() <-chan provider.ChatChunk) *fakeProvider {
	call := 0
	return &fakeProvider{streamFn: func(context.Context, provider.ChatRequest) (<-chan provider.ChatChunk, error) {
		if call >= len(scripts) {
			return nil, errors.New("unexpected stream call")
		}
		script := scripts[call]
		call++
		return script(), nil
	}}
}

func assistantDeltas(chunks ...provider.ChatChunk) func() <-chan provider.ChatChunk {
	return func() <-chan provider.ChatChunk {
		ch := make(chan provider.ChatChunk, len(chunks))
		for _, chunk := range chunks {
			ch <- chunk
		}
		close(ch)
		return ch
	}
}

func deltaChunk(content string) provider.ChatChunk {
	return provider.ChatChunk{Delta: provider.Message{Role: provider.MessageRoleAssistant, Content: content}}
}

func assistantMessageEvents(events []output.Event) []output.AssistantMessageEvent {
	var found []output.AssistantMessageEvent
	for _, event := range events {
		if payload, ok := event.Payload.(output.AssistantMessageEvent); ok {
			found = append(found, payload)
		}
	}
	return found
}

func lastMessage(msgs []Message) (Message, bool) {
	if len(msgs) == 0 {
		return Message{}, false
	}
	return msgs[len(msgs)-1], true
}

func TestRunnerUsageLimitPreservesPartialAssistantText(t *testing.T) {
	ule := &provider.UsageLimitError{
		Kind:     provider.UsageLimitKindUsage,
		Provider: "codex",
		HTTP:     &provider.HTTPError{StatusCode: 429},
	}
	stub := streamScriptProvider(assistantDeltas(
		deltaChunk("the report so far"),
		provider.ChatChunk{Done: true, Error: ule.Error(), OriginalError: ule},
	))

	var events []output.Event
	state, err := NewRunner().Run(context.Background(), partialAssistantRunRequest(stub, &events))
	if !errors.Is(err, ule) {
		t.Fatalf("Run() error = %v, want the original usage limit error", err)
	}
	if state.StopReason != StopReasonUsageLimit {
		t.Fatalf("StopReason = %q, want %q", state.StopReason, StopReasonUsageLimit)
	}
	if state.TurnCount != 1 {
		t.Errorf("TurnCount = %d, want the attempted turn 1", state.TurnCount)
	}
	last, ok := lastMessage(state.Conversation)
	if !ok {
		t.Fatal("conversation is empty")
	}
	if last.Role != MessageRoleAssistant || last.Content != "the report so far" || !last.Incomplete {
		t.Errorf("last conversation message = %+v, want the incomplete partial", last)
	}
	if last.Turn != 1 {
		t.Errorf("partial turn = %d, want 1", last.Turn)
	}
	lineage := state.Lineage.FullMessages()
	if len(lineage) == 0 || lineage[len(lineage)-1].Content != "the report so far" || !lineage[len(lineage)-1].Incomplete {
		t.Errorf("lineage = %+v, want the incomplete partial appended", lineage)
	}
	emitted := assistantMessageEvents(events)
	if len(emitted) != 1 {
		t.Fatalf("assistant message events = %+v, want exactly one", emitted)
	}
	if emitted[0].Turn != 1 || emitted[0].Content != "the report so far" {
		t.Errorf("assistant message event = %+v, want turn 1 and the exact streamed text", emitted[0])
	}
	stopTurns := stopEventTurns(events)
	if len(stopTurns) != 1 {
		t.Fatalf("stop event turns = %v, want exactly one stop event", stopTurns)
	}
	if stopTurns[0] != emitted[0].Turn || stopTurns[0] != state.TurnCount {
		t.Errorf("stop event turn = %d, want the assistant message turn %d and TurnCount %d", stopTurns[0], emitted[0].Turn, state.TurnCount)
	}
}

func TestRunnerTransientRetrySuccessDropsDiscardedPartial(t *testing.T) {
	origSleep := runnerRetrySleepFn
	runnerRetrySleepFn = func(_ context.Context, _ time.Duration) error { return nil }
	defer func() { runnerRetrySleepFn = origSleep }()

	stub := streamScriptProvider(
		assistantDeltas(
			deltaChunk("draft that failed"),
			provider.ChatChunk{Done: true, Error: "503", OriginalError: &provider.HTTPError{StatusCode: 503, Status: "503 Service Unavailable"}},
		),
		assistantDeltas(provider.ChatChunk{
			Done:  true,
			Delta: provider.Message{Role: provider.MessageRoleAssistant, Content: "final answer"},
		}),
	)

	var events []output.Event
	state, err := NewRunner().Run(context.Background(), partialAssistantRunRequest(stub, &events))
	if err != nil {
		t.Fatalf("Run() error = %v, want success after the retry", err)
	}
	emitted := assistantMessageEvents(events)
	if len(emitted) != 1 || emitted[0].Content != "final answer" {
		t.Fatalf("assistant message events = %+v, want only the successful message", emitted)
	}
	last, ok := lastMessage(state.Conversation)
	if !ok {
		t.Fatal("conversation is empty")
	}
	if last.Content != "final answer" || last.Incomplete {
		t.Errorf("last conversation message = %+v, want the complete successful message", last)
	}
	for _, message := range state.Conversation {
		if message.Incomplete {
			t.Errorf("conversation retained a partial: %+v", message)
		}
	}
	if state.PendingAssistant != (PendingAssistant{}) {
		t.Errorf("PendingAssistant = %+v, want empty", state.PendingAssistant)
	}
}

// TestRunnerRetryWithoutTextKeepsEarlierPartial covers a retry that starts and
// fails before streaming anything: the text the user already saw from the first
// attempt is what run exit commits.
func TestRunnerRetryWithoutTextKeepsEarlierPartial(t *testing.T) {
	origSleep := runnerRetrySleepFn
	runnerRetrySleepFn = func(_ context.Context, _ time.Duration) error { return nil }
	defer func() { runnerRetrySleepFn = origSleep }()

	retryErr := &provider.HTTPError{StatusCode: 400, Status: "400 Bad Request"}
	stub := streamScriptProvider(
		assistantDeltas(
			deltaChunk("the report so far"),
			provider.ChatChunk{Done: true, Error: "503", OriginalError: &provider.HTTPError{StatusCode: 503, Status: "503 Service Unavailable"}},
		),
		assistantDeltas(provider.ChatChunk{Done: true, Error: "400", OriginalError: retryErr}),
	)

	var events []output.Event
	state, err := NewRunner().Run(context.Background(), partialAssistantRunRequest(stub, &events))
	if err == nil {
		t.Fatal("Run() error = nil, want the retry failure")
	}
	emitted := assistantMessageEvents(events)
	if len(emitted) != 1 || emitted[0].Content != "the report so far" {
		t.Fatalf("assistant message events = %+v, want exactly the first attempt's text", emitted)
	}
	var partials []Message
	for _, message := range state.Conversation {
		if message.Incomplete {
			partials = append(partials, message)
		}
	}
	if len(partials) != 1 || partials[0].Content != "the report so far" || partials[0].Turn != 1 {
		t.Fatalf("partial messages = %+v, want the first attempt's text at turn 1", partials)
	}
	stopTurns := stopEventTurns(events)
	if len(stopTurns) != 1 || stopTurns[0] != 1 {
		t.Fatalf("stop event turns = %v, want the committed turn 1", stopTurns)
	}
}

// TestInitialConversationTurnCountSeedsFromIncompleteMessage proves a
// conversation whose last message is an interrupted partial seeds the next run
// at the partial's turn, so the next turn is that turn plus one.
func TestInitialConversationTurnCountSeedsFromIncompleteMessage(t *testing.T) {
	tests := []struct {
		name         string
		conversation []Message
		wantTurn     int
	}{
		{
			name: "incomplete assistant message at turn four",
			conversation: []Message{
				{Role: MessageRoleUser, Content: "task", Turn: 3},
				{Role: MessageRoleAssistant, Content: "the partial report", Turn: 4, Incomplete: true},
			},
			wantTurn: 4,
		},
		{
			name:         "no conversation",
			conversation: nil,
			wantTurn:     0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := initialConversationTurnCount(tt.conversation); got != tt.wantTurn {
				t.Fatalf("initialConversationTurnCount() = %d, want %d", got, tt.wantTurn)
			}
			state := initializeRunState(RunRequest{SourceConversation: tt.conversation})
			if state.TurnCount != tt.wantTurn {
				t.Fatalf("seeded TurnCount = %d, want %d", state.TurnCount, tt.wantTurn)
			}
		})
	}
}

func stopEventTurns(events []output.Event) []int {
	var turns []int
	for _, event := range events {
		if payload, ok := event.Payload.(output.StopReasonEvent); ok {
			turns = append(turns, payload.Turn)
		}
	}
	return turns
}

func TestRunnerExhaustedRetriesCommitOnePartial(t *testing.T) {
	origSleep := runnerRetrySleepFn
	runnerRetrySleepFn = func(_ context.Context, _ time.Duration) error { return nil }
	defer func() { runnerRetrySleepFn = origSleep }()

	attempt := 0
	newScript := func() <-chan provider.ChatChunk {
		attempt++
		content := fmt.Sprintf("attempt %d", attempt)
		return assistantDeltas(
			deltaChunk(content),
			provider.ChatChunk{Done: true, Error: "503", OriginalError: &provider.HTTPError{StatusCode: 503, Status: "503 Service Unavailable"}},
		)()
	}
	stub := &fakeProvider{streamFn: func(context.Context, provider.ChatRequest) (<-chan provider.ChatChunk, error) {
		if attempt > maxRunnerRetries {
			return nil, errors.New("unexpected extra attempt")
		}
		return newScript(), nil
	}}

	var events []output.Event
	state, err := NewRunner().Run(context.Background(), partialAssistantRunRequest(stub, &events))
	if err == nil {
		t.Fatal("Run() error = nil, want the exhausted retry error")
	}
	if attempt != maxRunnerRetries+1 {
		t.Fatalf("stream attempts = %d, want %d", attempt, maxRunnerRetries+1)
	}
	emitted := assistantMessageEvents(events)
	if len(emitted) != 1 {
		t.Fatalf("assistant message events = %+v, want exactly one", emitted)
	}
	wantContent := fmt.Sprintf("attempt %d", maxRunnerRetries+1)
	if emitted[0].Content != wantContent {
		t.Errorf("assistant message event content = %q, want %q", emitted[0].Content, wantContent)
	}
	var partials []Message
	for _, message := range state.Conversation {
		if message.Incomplete {
			partials = append(partials, message)
		}
	}
	if len(partials) != 1 || partials[0].Content != wantContent {
		t.Fatalf("partial messages = %+v, want exactly one from the final attempt", partials)
	}
}

func TestRunnerCancellationCommitsPartialAssistantText(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T) (RunState, []output.Event)
	}{
		{
			name: "cancelled while streaming",
			run: func(t *testing.T) (RunState, []output.Event) {
				t.Helper()
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				stub := streamScriptProvider(func() <-chan provider.ChatChunk {
					ch := make(chan provider.ChatChunk, 1)
					ch <- deltaChunk("half a report")
					cancel()
					close(ch)
					return ch
				})
				var events []output.Event
				state, err := NewRunner().Run(ctx, partialAssistantRunRequest(stub, &events))
				if err != nil {
					t.Fatalf("Run() error = %v, want no error on cancellation", err)
				}
				if state.StopReason != StopReasonCancelled {
					t.Errorf("StopReason = %q, want %q", state.StopReason, StopReasonCancelled)
				}
				return state, events
			},
		},
		{
			name: "cancelled during retry backoff",
			run: func(t *testing.T) (RunState, []output.Event) {
				t.Helper()
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				origSleep := runnerRetrySleepFn
				runnerRetrySleepFn = func(_ context.Context, _ time.Duration) error {
					cancel()
					return context.Canceled
				}
				defer func() { runnerRetrySleepFn = origSleep }()
				stub := streamScriptProvider(assistantDeltas(
					deltaChunk("half a report"),
					provider.ChatChunk{Done: true, Error: "503", OriginalError: &provider.HTTPError{StatusCode: 503, Status: "503 Service Unavailable"}},
				))
				var events []output.Event
				state, err := NewRunner().Run(ctx, partialAssistantRunRequest(stub, &events))
				if err != nil {
					t.Fatalf("Run() error = %v, want no error on cancellation", err)
				}
				if state.StopReason != StopReasonCancelled {
					t.Errorf("StopReason = %q, want %q", state.StopReason, StopReasonCancelled)
				}
				return state, events
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state, events := tt.run(t)
			last, ok := lastMessage(state.Conversation)
			if !ok {
				t.Fatal("conversation is empty")
			}
			if last.Content != "half a report" || !last.Incomplete {
				t.Errorf("last conversation message = %+v, want the incomplete partial", last)
			}
			emitted := assistantMessageEvents(events)
			if len(emitted) != 1 || emitted[0].Content != "half a report" {
				t.Fatalf("assistant message events = %+v, want exactly one carrying the partial", emitted)
			}
			if state.PendingAssistant != (PendingAssistant{}) {
				t.Errorf("PendingAssistant = %+v, want cleared after the commit", state.PendingAssistant)
			}
		})
	}
}

// TestFailedCompactionCallCommitsNoAssistantMessage proves a summary call that
// streams a draft and then fails leaves the conversation untouched and emits no
// assistant message event. Compaction calls executeChatRequestWithOptions
// directly, so it never reaches the run-exit partial commit.
func TestFailedCompactionCallCommitsNoAssistantMessage(t *testing.T) {
	upstreamErr := errors.New("summary failed")
	stub := streamScriptProvider(assistantDeltas(
		deltaChunk("draft summary"),
		provider.ChatChunk{Done: true, Error: upstreamErr.Error(), OriginalError: upstreamErr},
	))
	conversation := []Message{
		{Role: MessageRoleUser, Content: "turn 1 user"},
		{Role: MessageRoleAssistant, Content: "turn 1 assistant"},
		{Role: MessageRoleUser, Content: "turn 2 user"},
		{Role: MessageRoleAssistant, Content: "turn 2 assistant"},
	}
	state := RunState{Conversation: conversation, Lineage: newConversationLineage(conversation)}
	candidate, ok := selectCompactionCandidate(state.Lineage, nil)
	if !ok {
		t.Fatal("selectCompactionCandidate() ok = false, want true")
	}
	var events []output.Event
	req := RunRequest{
		Provider:      stub,
		ResolvedModel: provider.ResolvedModel{BackendModelID: "test-model"},
		ModelBudget: prompt.ModelTokenBudget{
			ContextSize:         100000,
			MaxCompletionTokens: 256,
			SummaryMaxTokens:    128,
		},
		Events: output.SinkFunc(func(event output.Event) { events = append(events, event) }),
	}
	outcome, err := summarizeCompactor{}.Compact(context.Background(), req, state, 3, candidate, "")
	if !errors.Is(err, upstreamErr) {
		t.Fatalf("Compact() error = %v, want %v", err, upstreamErr)
	}
	if outcome.Applied {
		t.Error("Applied = true, want false")
	}
	if len(outcome.State.Conversation) != 0 {
		t.Errorf("failed compaction returned %d messages, want none appended", len(outcome.State.Conversation))
	}
	if got := assistantMessageEvents(events); len(got) != 0 {
		t.Errorf("assistant message events = %+v, want none for a failed summary", got)
	}
}

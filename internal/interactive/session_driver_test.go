package interactive

import (
	"context"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/session"
)

// inputRunner is a runExecutor whose Run sees the whole RunInput.
type inputRunner struct {
	run func(context.Context, RunInput) (RunResult, error)
}

func (r *inputRunner) Run(ctx context.Context, in RunInput) (RunResult, error) {
	return r.run(ctx, in)
}

func (r *inputRunner) Compact(_ context.Context, conversation []agent.Message, _ []provider.ToolSpec, _ string) ([]agent.Message, error) {
	return conversation, nil
}

func withAnswer(in RunInput, answer string) RunResult {
	conv := append(append([]agent.Message(nil), in.Conversation...), agent.Message{Role: agent.MessageRoleAssistant, Content: answer})
	return RunResult{Conversation: conv}
}

func lastMessage(conv []agent.Message) string {
	return conv[len(conv)-1].Content
}

func recv[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
		panic("unreachable")
	}
}

func TestSteerQueuedAfterFinalBoundaryStartsNewSequence(t *testing.T) {
	t.Parallel()
	s := testNewSession(t, Dependencies{Config: config.Config{Modes: config.ModesConfig{Default: config.ExecutionModePlan}}})
	steers := s.ActiveRunController().SteerQueue()
	inputs := make(chan RunInput, 4)
	var calls atomic.Int32
	s.SetRunner(&inputRunner{run: func(_ context.Context, in RunInput) (RunResult, error) {
		inputs <- in
		if calls.Add(1) == 1 {
			// The steer arrives after the run's last boundary drain.
			steers.Add(agent.SteerMessage{Text: "late steer"})
			if err := s.Handle(context.Background(), NotifySteer{}); err != nil {
				t.Errorf("NotifySteer: %v", err)
			}
		}
		return withAnswer(in, "answer"), nil
	}})

	if err := s.Handle(context.Background(), SubmitPrompt{Text: "first"}); err != nil {
		t.Fatalf("SubmitPrompt: %v", err)
	}
	first := recv(t, inputs, "first run")
	if got := lastMessage(first.Conversation); !strings.HasSuffix(got, "first") {
		t.Fatalf("first run last message = %q, want the prompt", got)
	}
	second := recv(t, inputs, "second run for the late steer")
	got := lastMessage(second.Conversation)
	if !strings.HasSuffix(got, "late steer") || !strings.HasPrefix(got, prompt.ModeNotice(config.ExecutionModePlan)) {
		t.Fatalf("second run last message = %q, want the mode notice then the steer", got)
	}
	waitSettled(t, s)
	if n := steers.Len(); n != 0 {
		t.Fatalf("steer queue len = %d, want 0 (the steer was delivered, not dropped)", n)
	}
	if conv := s.Conversation(); len(conv) != 4 || !strings.HasSuffix(conv[2].Content, "late steer") {
		t.Fatalf("conversation = %+v, want prompt, answer, steer, answer", conv)
	}
}

func TestRapidSubmitsAreDeliveredInOrderWithoutConcurrentRuns(t *testing.T) {
	t.Parallel()
	s := testNewSession(t, Dependencies{})
	var inFlight, maxInFlight atomic.Int32
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	var prompts []string // written only by the driver's serial runs
	s.SetRunner(&inputRunner{run: func(_ context.Context, in RunInput) (RunResult, error) {
		n := inFlight.Add(1)
		defer inFlight.Add(-1)
		for {
			m := maxInFlight.Load()
			if n <= m || maxInFlight.CompareAndSwap(m, n) {
				break
			}
		}
		prompts = append(prompts, lastMessage(in.Conversation))
		if len(prompts) == 1 {
			close(firstStarted)
			<-releaseFirst
		}
		return withAnswer(in, "answer"), nil
	}})

	if err := s.Handle(context.Background(), SubmitPrompt{Text: "one"}); err != nil {
		t.Fatalf("SubmitPrompt one: %v", err)
	}
	recv(t, firstStarted, "first run")
	if err := s.Handle(context.Background(), SubmitPrompt{Text: "two"}); err != nil {
		t.Fatalf("SubmitPrompt two: %v", err)
	}
	close(releaseFirst)
	waitSettled(t, s)

	if want := []string{"one", "two"}; !reflect.DeepEqual(prompts, want) {
		t.Fatalf("runs saw prompts %v, want %v in order", prompts, want)
	}
	if got := maxInFlight.Load(); got != 1 {
		t.Fatalf("max concurrent runs = %d, want 1", got)
	}
	var users []string
	for _, msg := range s.Conversation() {
		if msg.Role == agent.MessageRoleUser {
			users = append(users, msg.Content)
		}
	}
	if want := []string{"one", "two"}; !reflect.DeepEqual(users, want) {
		t.Fatalf("conversation user messages = %v, want %v", users, want)
	}
}

func TestSubmitDuringRunIsDeliveredAtTheRunBoundary(t *testing.T) {
	t.Parallel()
	s := testNewSession(t, Dependencies{})
	firstStarted := make(chan struct{})
	secondSubmitted := make(chan struct{})
	var calls atomic.Int32
	var drained string
	s.SetRunner(&inputRunner{run: func(_ context.Context, in RunInput) (RunResult, error) {
		if calls.Add(1) > 1 {
			return withAnswer(in, "answer"), nil
		}
		close(firstStarted)
		<-secondSubmitted
		drain := in.DrainInbox()
		if drain.Message == nil {
			t.Error("boundary drain returned nothing, want the second prompt")
			return withAnswer(in, "answer"), nil
		}
		drained = drain.Message.Content
		conv := append(append([]agent.Message(nil), in.Conversation...), *drain.Message)
		return withAnswer(RunInput{Conversation: conv}, "answer"), nil
	}})

	if err := s.Handle(context.Background(), SubmitPrompt{Text: "one"}); err != nil {
		t.Fatalf("SubmitPrompt one: %v", err)
	}
	recv(t, firstStarted, "first run")
	if err := s.Handle(context.Background(), SubmitPrompt{Text: "two"}); err != nil {
		t.Fatalf("SubmitPrompt two: %v", err)
	}
	close(secondSubmitted)
	waitSettled(t, s)

	if drained != "two" {
		t.Fatalf("boundary delivery = %q, want two", drained)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("Run calls = %d, want 1 (the second prompt joined the running sequence)", got)
	}
}

func TestSavedSessionKeepsEarlierCompactionGenerations(t *testing.T) {
	t.Parallel()
	store := newMockSessionStore()
	summary := []agent.Message{{Role: agent.MessageRoleSummary, Content: "summary"}, {Role: agent.MessageRoleUser, Content: "kept"}}
	s := testNewSession(t, Dependencies{SessionStore: store, Config: guardTestConfig()})
	s.SetRunner(&runExecutorFunc{
		run: func(_ context.Context, conv []agent.Message) (RunResult, error) {
			return RunResult{Conversation: append(append([]agent.Message(nil), conv...), agent.Message{Role: agent.MessageRoleAssistant, Content: "later"})}, nil
		},
		compact: func(context.Context, []agent.Message, []provider.ToolSpec) ([]agent.Message, error) {
			return cloneMessages(summary), nil
		},
	})
	original := twoTurnConversation()
	seedConversation(s, original, lineageOf(original...))
	id := s.SessionID()

	compactAndWait(t, s, "")
	submitAndWait(t, s, "after compaction", nil)

	saved, ok := store.savedSessions[id]
	if !ok {
		t.Fatal("session was not saved")
	}
	generations := saved.Lineage.Generations
	if len(generations) != 2 {
		t.Fatalf("saved generations = %d, want 2 (pre-compaction and compacted)", len(generations))
	}
	if !reflect.DeepEqual(generations[0].Messages, original) {
		t.Fatalf("generation 1 = %+v, want the pre-compaction conversation intact", generations[0].Messages)
	}
	latest := generations[1]
	if len(latest.SummaryPrefix) != 1 || latest.SummaryPrefix[0].Content != "summary" {
		t.Fatalf("latest summary prefix = %+v, want the summary", latest.SummaryPrefix)
	}
	if got := lastMessage(latest.Messages); got != "later" {
		t.Fatalf("latest generation ends with %q, want the run's final answer", got)
	}
	if len(latest.Messages) != 3 {
		t.Fatalf("latest generation messages = %+v, want kept, prompt, answer", latest.Messages)
	}
}

func TestInterruptActiveRunStopsTheDriverRun(t *testing.T) {
	t.Parallel()
	s := testNewSession(t, Dependencies{})
	started := make(chan struct{})
	cancelled := make(chan struct{})
	s.SetRunner(&inputRunner{run: func(ctx context.Context, in RunInput) (RunResult, error) {
		close(started)
		<-ctx.Done()
		close(cancelled)
		return RunResult{Conversation: in.Conversation}, nil
	}})

	if err := s.Handle(context.Background(), SubmitPrompt{Text: "long task"}); err != nil {
		t.Fatalf("SubmitPrompt: %v", err)
	}
	recv(t, started, "run start")
	if err := s.Handle(context.Background(), InterruptActiveRun{}); err != nil {
		t.Fatalf("InterruptActiveRun: %v", err)
	}
	recv(t, cancelled, "run cancellation")
	waitSettled(t, s)
	if s.runActive() {
		t.Fatal("runActive() = true after the interrupted run settled")
	}
}

func TestCloseCancelsRunAndSavesConversation(t *testing.T) {
	t.Parallel()
	store := newMockSessionStore()
	s := testNewSession(t, Dependencies{SessionStore: store, Config: guardTestConfig()})
	started := make(chan struct{})
	s.SetRunner(&inputRunner{run: func(ctx context.Context, in RunInput) (RunResult, error) {
		close(started)
		<-ctx.Done()
		return withAnswer(in, "partial"), nil
	}})
	if err := s.Handle(context.Background(), SubmitPrompt{Text: "hello"}); err != nil {
		t.Fatalf("SubmitPrompt: %v", err)
	}
	recv(t, started, "run start")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s.Close(ctx)

	saved, ok := store.savedSessions[s.SessionID()]
	if !ok {
		t.Fatal("Close did not save the session")
	}
	if msgs := saved.Lineage.FullMessages(); len(msgs) != 2 || msgs[1].Content != "partial" {
		t.Fatalf("saved conversation = %+v, want the prompt and the cancelled run's partial answer", msgs)
	}
}

func TestMergeRunLineage(t *testing.T) {
	t.Parallel()
	summary := agent.Message{Role: agent.MessageRoleSummary, Content: "sum"}
	user := func(text string) agent.Message { return agent.Message{Role: agent.MessageRoleUser, Content: text} }
	generations := func(l agent.ConversationLineage) [][]string {
		var out [][]string
		for _, g := range l.Generations {
			var texts []string
			for _, m := range g.FullMessages() {
				texts = append(texts, m.Content)
			}
			out = append(out, texts)
		}
		return out
	}
	tests := []struct {
		name  string
		prior agent.ConversationLineage
		run   agent.ConversationLineage
		want  [][]string
	}{
		{
			name: "empty prior adopts the run lineage",
			run:  lineageFromMessages([]agent.Message{user("a"), user("b")}),
			want: [][]string{{"a", "b"}},
		},
		{
			name:  "run extends the latest generation",
			prior: lineageFromMessages([]agent.Message{user("a")}),
			run:   lineageFromMessages([]agent.Message{user("a"), user("b")}),
			want:  [][]string{{"a", "b"}},
		},
		{
			name:  "earlier generations survive and the summary prefix is re-split",
			prior: lineageFromMessages([]agent.Message{user("old")}).WithNewGeneration([]agent.Message{summary}, []agent.Message{user("a")}),
			run:   lineageFromMessages([]agent.Message{summary, user("a"), user("b")}),
			want:  [][]string{{"old"}, {"sum", "a", "b"}},
		},
		{
			name:  "generations added by the run are appended",
			prior: lineageFromMessages([]agent.Message{user("old")}),
			run:   lineageFromMessages([]agent.Message{user("old"), user("b")}).WithNewGeneration([]agent.Message{summary}, []agent.Message{user("c")}),
			want:  [][]string{{"old", "b"}, {"sum", "c"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			merged := mergeRunLineage(tt.prior, tt.run)
			if got := generations(merged); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("generations = %v, want %v", got, tt.want)
			}
			for i, g := range merged.Generations {
				if want := i + 1; g.ID != want && tt.name != "empty prior adopts the run lineage" {
					t.Errorf("generation %d has ID %d, want %d", i, g.ID, want)
				}
			}
		})
	}
}

func TestCloseAndRetireDoNotRewriteUntouchedSessions(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		loadThenClear bool
	}{
		{name: "close a new session"},
		{name: "replace a loaded session", loadThenClear: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			store := newMockSessionStore()
			store.loadedSessions["old"] = session.Session{ID: "old", Lineage: lineageOf(userMsg("kept"))}
			s := testNewSession(t, Dependencies{SessionStore: store, Config: guardTestConfig()})
			if tt.loadThenClear {
				if err := s.Handle(context.Background(), LoadSession{SessionID: "old"}); err != nil {
					t.Fatalf("LoadSession: %v", err)
				}
				if err := s.Handle(context.Background(), ClearConversation{}); err != nil {
					t.Fatalf("ClearConversation: %v", err)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			s.Close(ctx)
			waitSettled(t, s)

			store.mu.Lock()
			defer store.mu.Unlock()
			if len(store.savedSessions) != 0 {
				t.Fatalf("saved sessions = %v, want none: nothing changed", store.savedSessions)
			}
		})
	}
}

func TestInterruptWithQueuedSteerStopsInsteadOfRestarting(t *testing.T) {
	t.Parallel()
	s := testNewSession(t, Dependencies{})
	steers := s.ActiveRunController().SteerQueue()
	started := make(chan struct{})
	var calls atomic.Int32
	s.SetRunner(&inputRunner{run: func(ctx context.Context, in RunInput) (RunResult, error) {
		calls.Add(1)
		close(started)
		<-ctx.Done()
		return RunResult{Conversation: in.Conversation}, nil
	}})

	if err := s.Handle(context.Background(), SubmitPrompt{Text: "long task"}); err != nil {
		t.Fatalf("SubmitPrompt: %v", err)
	}
	recv(t, started, "run start")
	steers.Add(agent.SteerMessage{Text: "queued steer"})
	if err := s.Handle(context.Background(), InterruptActiveRun{}); err != nil {
		t.Fatalf("InterruptActiveRun: %v", err)
	}
	waitSettled(t, s)

	if got := calls.Load(); got != 1 {
		t.Fatalf("Run calls = %d, want 1: the interrupt must stop the model, not restart it on the steer", got)
	}
	if got := steers.Len(); got != 1 {
		t.Fatalf("steer queue len = %d, want the steer kept for take-back", got)
	}
}

func TestRotateLeavesSteerQueueToItsOwner(t *testing.T) {
	t.Parallel()
	store := newMockSessionStore()
	s := testNewSession(t, Dependencies{SessionStore: store, Config: guardTestConfig()})
	steers := s.ActiveRunController().SteerQueue()
	started := make(chan struct{})
	release := make(chan struct{})
	drained := make(chan agent.InboxDrain, 1)
	s.SetRunner(&inputRunner{run: func(_ context.Context, in RunInput) (RunResult, error) {
		close(started)
		<-release
		drained <- in.DrainInbox()
		return withAnswer(in, "answer"), nil
	}})

	if err := s.Handle(context.Background(), SubmitPrompt{Text: "hi"}); err != nil {
		t.Fatalf("SubmitPrompt: %v", err)
	}
	recv(t, started, "run start")
	if err := s.Handle(context.Background(), RotateSessionWithGroup{Group: "phase-2"}); err != nil {
		t.Fatalf("RotateSessionWithGroup: %v", err)
	}
	steers.Add(agent.SteerMessage{Text: "for the new phase"})
	close(release)
	if drain := recv(t, drained, "retired driver's boundary drain"); drain.Message != nil {
		t.Fatalf("retired driver drained %+v from the shared steer queue", drain.Message)
	}
	waitSettled(t, s)
	if got := steers.Len(); got != 1 {
		t.Fatalf("steer queue len = %d, want the steer left in place", got)
	}
}

func TestIdleRotateDoesNotDrainSteerQueue(t *testing.T) {
	t.Parallel()
	store := newMockSessionStore()
	s := testNewSession(t, Dependencies{SessionStore: store, Config: guardTestConfig()})
	steers := s.ActiveRunController().SteerQueue()
	steers.Add(agent.SteerMessage{Text: "queued for a oneshot phase"})

	if err := s.Handle(context.Background(), RotateSessionWithGroup{Group: "phase-1"}); err != nil {
		t.Fatalf("RotateSessionWithGroup: %v", err)
	}
	waitSettled(t, s)

	if got := steers.Len(); got != 1 {
		t.Fatalf("steer queue len = %d, want 1: rotation must not settle steers into the old conversation", got)
	}
	if conv := s.Conversation(); len(conv) != 0 {
		t.Fatalf("conversation = %+v, want empty", conv)
	}
}

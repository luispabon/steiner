package interactive

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/session"
	"github.com/luispabon/steiner/internal/tool"
)

type handoffSaveBarrierStore struct {
	inner   *mockSessionStore
	entered chan session.Session
	release chan struct{}
	block   atomic.Bool
}

func newHandoffSaveBarrierStore() *handoffSaveBarrierStore {
	return &handoffSaveBarrierStore{
		inner:   newMockSessionStore(),
		entered: make(chan session.Session, 1),
		release: make(chan struct{}),
	}
}

func (s *handoffSaveBarrierStore) Save(saved session.Session) error {
	if s.block.Load() {
		s.entered <- saved
		<-s.release
	}
	return s.inner.Save(saved)
}

func (s *handoffSaveBarrierStore) Load(id string) (session.Session, error) {
	return s.inner.Load(id)
}

func (s *handoffSaveBarrierStore) List() ([]session.IndexEntry, error) {
	return s.inner.List()
}

func TestAcceptedWorkflowHandoffWaitRunsIncludesSettlementSave(t *testing.T) {
	store := newHandoffSaveBarrierStore()
	var eventsMu sync.Mutex
	var events []output.Event
	requested := make(chan struct{}, 1)
	s := testNewSession(t, Dependencies{
		SessionStore: store,
		BaseEvents: output.SinkFunc(func(event output.Event) {
			eventsMu.Lock()
			events = append(events, event)
			eventsMu.Unlock()
			if event.Type == output.EventTypeWorkflowHandoffRequested {
				requested <- struct{}{}
			}
		}),
		Config: guardTestConfig(),
	})
	oldID := s.SessionID()
	prompt := "prepare handoff"
	requestStarted := make(chan struct{})
	resumeRunner := make(chan struct{})
	returnTransition := tool.WorkflowHandoffTransition{Next: "implement", Target: ".steiner/plans/step-2"}
	s.SetRunner(newRunExecutorFunc(func(ctx context.Context, conv []agent.Message) (RunResult, error) {
		responder := s.WorkflowHandoffResponder(s.EventSink())
		response, err := responder.RequestWorkflowHandoff(ctx, tool.WorkflowHandoffRequest{
			Next: returnTransition.Next, Target: returnTransition.Target, Message: "approved",
		})
		if err != nil {
			return RunResult{}, err
		}
		if !response.Accepted {
			return RunResult{}, errors.New("workflow handoff was not accepted")
		}
		close(requestStarted)
		// Match the accepted tool path: it emits workflow_handoff stop reason
		// before the runner result is adopted and the session save starts.
		s.EventSink().Emit(output.NewStopReasonEvent(1, string(agent.StopReasonWorkflowHandoff), nil))
		<-resumeRunner
		final := append(append([]agent.Message(nil), conv...), agent.Message{Role: agent.MessageRoleAssistant, Content: "final handoff turn"})
		return RunResult{Conversation: final, WorkflowHandoff: &returnTransition}, nil
	}))

	if err := s.Handle(context.Background(), SubmitPrompt{Text: prompt}); err != nil {
		t.Fatalf("SubmitPrompt: %v", err)
	}
	select {
	case <-requested:
	case <-time.After(10 * time.Second):
		t.Fatal("workflow handoff request was not emitted")
	}
	if err := s.Handle(context.Background(), SubmitWorkflowHandoff{Decision: "accept"}); err != nil {
		t.Fatalf("accept workflow handoff: %v", err)
	}
	select {
	case <-requestStarted:
	case <-time.After(10 * time.Second):
		t.Fatal("accepted workflow handoff did not reach runner barrier")
	}

	if err := s.Handle(context.Background(), ClearConversation{}); !errors.Is(err, errRunInProgress) {
		t.Fatalf("ClearConversation before final result = %v, want errRunInProgress", err)
	}
	if s.SessionID() != oldID || s.PromptCacheKey() != oldID {
		t.Fatal("refused clear changed session identity or cache key")
	}
	store.block.Store(true)
	ctx, cancel := context.WithCancel(context.Background())
	waited := make(chan bool, 1)
	go func() { waited <- s.WaitRuns(ctx) }()
	close(resumeRunner)

	var finalSaved session.Session
	select {
	case finalSaved = <-store.entered:
	case <-time.After(10 * time.Second):
		cancel()
		t.Fatal("final session save did not reach barrier")
	}
	if finalSaved.ID != oldID {
		t.Fatalf("save ID = %q, want original ID %q", finalSaved.ID, oldID)
	}
	if got := finalSaved.Lineage.FullMessages(); len(got) == 0 || got[len(got)-1].Content != "final handoff turn" {
		t.Fatalf("save lineage = %+v, want final handoff turn", got)
	}
	cancel()
	select {
	case result := <-waited:
		if result {
			t.Fatal("WaitRuns returned settled while the final save was blocked")
		}
	case <-time.After(10 * time.Second):
		cancel()
		t.Fatal("WaitRuns did not return when its context was cancelled during save")
	}
	cancel()

	store.inner.mu.Lock()
	priorSaved, prematurelySaved := store.inner.savedSessions[oldID]
	store.inner.mu.Unlock()
	if prematurelySaved {
		for _, message := range priorSaved.Lineage.FullMessages() {
			if message.Content == "final handoff turn" {
				t.Fatal("final handoff lineage reached the backing store before save release")
			}
		}
	}

	close(store.release)
	ctx, cancel = context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if !s.WaitRuns(ctx) {
		t.Fatal("WaitRuns returned false after final save completed")
	}

	if err := s.Handle(context.Background(), ClearConversation{}); err != nil {
		t.Fatalf("ClearConversation after settlement: %v", err)
	}
	newID := s.SessionID()
	if newID == oldID || s.PromptCacheKey() != newID {
		t.Fatalf("cleared session identity = %q, cache key = %q, want fresh matching identity", newID, s.PromptCacheKey())
	}
	if got := s.Conversation(); len(got) != 0 {
		t.Fatalf("cleared conversation = %+v, want empty", got)
	}
	if got := s.lineage.FullMessages(); len(got) != 0 {
		t.Fatalf("cleared lineage = %+v, want empty", got)
	}

	store.inner.mu.Lock()
	saved, ok := store.inner.savedSessions[oldID]
	_, newIdentitySaved := store.inner.savedSessions[newID]
	store.inner.mu.Unlock()
	if !ok || saved.ID != oldID {
		t.Fatalf("final source save under old ID = %v, ID %q", ok, saved.ID)
	}
	if newIdentitySaved {
		t.Fatal("final handoff history was saved under the cleared identity")
	}
	if got := saved.Lineage.FullMessages(); len(got) == 0 || got[len(got)-1].Content != "final handoff turn" {
		t.Fatalf("saved final lineage = %+v, want final handoff turn", got)
	}

	eventsMu.Lock()
	defer eventsMu.Unlock()
	var accepted, stopReason bool
	for _, event := range events {
		if event.Type == output.EventTypeWorkflowHandoffAccepted {
			if payload, ok := event.Payload.(output.WorkflowHandoffEvent); ok && payload.Decision == "accepted" {
				accepted = true
			}
		}
		if event.Type == output.EventTypeStopReason {
			if payload, ok := event.Payload.(output.StopReasonEvent); ok && payload.Reason == string(agent.StopReasonWorkflowHandoff) {
				stopReason = true
			}
		}
	}
	if !accepted {
		t.Fatal("real workflow handoff responder did not emit accepted event")
	}
	if !stopReason {
		t.Fatal("accepted workflow handoff did not emit stop reason before save")
	}
}

package interactive

import (
	"context"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/session"
)

// manualClock records timers and fires them only when the test says so.
type manualClock struct {
	mu     sync.Mutex
	timers []func()
}

func (c *manualClock) Now() time.Time { return time.Time{} }

func (c *manualClock) AfterFunc(_ time.Duration, f func()) agent.Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.timers = append(c.timers, f)
	return stoppedTimer{}
}

func (c *manualClock) fireAll() {
	c.mu.Lock()
	timers := c.timers
	c.timers = nil
	c.mu.Unlock()
	for _, f := range timers {
		f()
	}
}

type stoppedTimer struct{}

func (stoppedTimer) Stop() bool { return true }

// ledgerBackground is a stubBackground with a settable ledger.
type ledgerBackground struct {
	stubBackground
	ledger atomic.Pointer[[]agent.SubAgentLedgerEntry]
}

func (b *ledgerBackground) Ledger() []agent.SubAgentLedgerEntry {
	if l := b.ledger.Load(); l != nil {
		return slices.Clone(*l)
	}
	return nil
}

// sinkRecorder captures the latest completion sink the session installs.
type sinkRecorder struct {
	mu   sync.Mutex
	sink agent.CompletionSink
}

func (r *sinkRecorder) set(s agent.CompletionSink) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sink = s
}

func (r *sinkRecorder) get() agent.CompletionSink {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sink
}

func asyncTestSession(t *testing.T, store sessionStore) (*Session, *sinkRecorder, *manualClock, *ledgerBackground) {
	t.Helper()
	rec := &sinkRecorder{}
	clock := &manualClock{}
	bg := &ledgerBackground{}
	s := testNewSession(t, Dependencies{
		SessionStore:      store,
		Background:        bg,
		SetCompletionSink: rec.set,
		Clock:             clock,
	})
	return s, rec, clock, bg
}

func TestCompletionSinkFollowsTheLiveDriver(t *testing.T) {
	t.Parallel()
	store := newMockSessionStore()
	store.loadedSessions["other"] = session.Session{ID: "other", Model: "m", Lineage: lineageFromMessages([]agent.Message{{Role: agent.MessageRoleUser, Content: "hi"}})}
	s, rec, _, _ := asyncTestSession(t, store)

	if got := rec.get(); got != agent.CompletionSink(s.currentDriver()) {
		t.Fatalf("sink after NewSession = %T %v, want the live driver", got, got)
	}
	first := s.currentDriver()
	if err := s.Handle(context.Background(), LoadSession{SessionID: "other"}); err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if s.currentDriver() == first {
		t.Fatal("load did not rebuild the driver")
	}
	if got := rec.get(); got != agent.CompletionSink(s.currentDriver()) {
		t.Fatal("sink after load is not the new driver")
	}
	if err := s.Handle(context.Background(), RotateSession{}); err != nil {
		t.Fatalf("RotateSession: %v", err)
	}
	if got := rec.get(); got != agent.CompletionSink(s.currentDriver()) {
		t.Fatal("sink after rotate is not the new driver")
	}
}

func TestDeliveredCompletionWakesARunAfterTheWindow(t *testing.T) {
	t.Parallel()
	s, rec, clock, _ := asyncTestSession(t, newMockSessionStore())
	inputs := make(chan RunInput, 2)
	s.SetRunner(&inputRunner{run: func(_ context.Context, in RunInput) (RunResult, error) {
		inputs <- in
		return withAnswer(in, "noted"), nil
	}})

	rec.get().DeliverCompletions([]agent.SubAgentCompletion{{
		ParentCallID: "call-1", AgentID: "a1", AgentType: "explore", Status: "complete",
		Body: agent.FailureBody("complete", "done"),
	}})
	select {
	case in := <-inputs:
		t.Fatalf("run started before the coalescing window: %+v", in)
	default:
	}
	clock.fireAll()
	in := recv(t, inputs, "wake run")
	if got := lastMessage(in.Conversation); !strings.Contains(got, "<steiner-sub-agent-result") {
		t.Fatalf("run last message = %q, want a sub-agent result envelope", got)
	}
	if in.OnToolBatchDone == nil || in.PendingSubAgents == nil {
		t.Fatal("run input lacks SealBatch/Pending hooks from Background")
	}
	waitSettled(t, s)
}

func TestLedgerIsPersistedAtDurabilityPoints(t *testing.T) {
	t.Parallel()
	store := newMockSessionStore()
	s, _, _, bg := asyncTestSession(t, store)
	entries := []agent.SubAgentLedgerEntry{{AgentID: "a1", AgentType: "code", ParentCallID: "call-1", WorktreePath: "/wt/a1"}}
	bg.ledger.Store(&entries)
	s.SetRunner(&inputRunner{run: func(_ context.Context, in RunInput) (RunResult, error) {
		return withAnswer(in, "spawned"), nil
	}})

	submitAndWait(t, s, "go", nil)

	store.mu.Lock()
	saved := store.savedSessions[s.SessionID()]
	store.mu.Unlock()
	if !slices.Equal(saved.SubAgentLedger, entries) {
		t.Fatalf("saved ledger = %+v, want %+v", saved.SubAgentLedger, entries)
	}
}

// waitQuiescent waits until the driver has settled every buffered completion.
func waitQuiescent(t *testing.T, s *Session) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.currentDriver().WaitQuiescent(ctx); err != nil {
		t.Fatalf("session did not reach quiescence: %v", err)
	}
}

func TestLoadWithLedgerRecordsLostOnceWithoutARun(t *testing.T) {
	t.Parallel()
	store := newMockSessionStore()
	base := []agent.Message{{Role: agent.MessageRoleUser, Content: "start"}, {Role: agent.MessageRoleAssistant, Content: "spawned"}}
	store.loadedSessions["old"] = session.Session{
		ID: "old", Model: "m", Lineage: lineageFromMessages(base),
		SubAgentLedger: []agent.SubAgentLedgerEntry{{AgentID: "a1", AgentType: "code", ParentCallID: "call-1", WorktreePath: "/wt/a1"}},
	}
	s, _, _, _ := asyncTestSession(t, store)
	var runs atomic.Int32
	s.SetRunner(&inputRunner{run: func(_ context.Context, in RunInput) (RunResult, error) {
		runs.Add(1)
		return withAnswer(in, "x"), nil
	}})

	if err := s.Handle(context.Background(), LoadSession{SessionID: "old"}); err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	waitQuiescent(t, s)

	if runs.Load() != 0 {
		t.Fatalf("runs = %d, want 0", runs.Load())
	}
	conv := s.Conversation()
	if len(conv) != len(base)+1 {
		t.Fatalf("conversation len = %d, want %d: %+v", len(conv), len(base)+1, conv)
	}
	last := conv[len(conv)-1].Content
	for _, want := range []string{`status="lost"`, "call-1", "/wt/a1"} {
		if !strings.Contains(last, want) {
			t.Fatalf("lost message %q missing %q", last, want)
		}
	}
	store.mu.Lock()
	saved := store.savedSessions["old"]
	store.mu.Unlock()
	if len(saved.SubAgentLedger) != 0 {
		t.Fatalf("saved ledger = %+v, want empty", saved.SubAgentLedger)
	}

	if err := s.Handle(context.Background(), LoadSession{SessionID: "old"}); err != nil {
		t.Fatalf("reload: %v", err)
	}
	waitQuiescent(t, s)
	if got := len(s.Conversation()); got != len(base)+1 {
		t.Fatalf("conversation len after reload = %d, want %d (nothing appended)", got, len(base)+1)
	}
	if runs.Load() != 0 {
		t.Fatalf("runs after reload = %d, want 0", runs.Load())
	}
}

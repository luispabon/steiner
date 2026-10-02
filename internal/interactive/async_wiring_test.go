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
	"github.com/luispabon/steiner/internal/output"
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
	tests := []struct {
		name      string
		messages  []agent.Message
		lifecycle []string
		failed    []string
		ledger    []agent.SubAgentLedgerEntry
		groups    *agent.DelegationGroupLedger
	}{
		{name: "unknown running then ambiguous ledger", messages: []agent.Message{delegateCall("call-1", "sub_agent", "unknown task"), ackResult(t, "call-1", "sub_agent", "unknown-agent", "running"), delegateCall("call-1", "sub_agent", "other task")}, lifecycle: []string{"started:unknown-agent:"}, failed: []string{"unknown-agent:no result"}, ledger: []agent.SubAgentLedgerEntry{{AgentID: "a1", AgentType: "code", ParentCallID: "call-1", BatchID: "batch-lost", Group: "group-lost", WorktreePath: "/wt/a1"}, {AgentID: "a2", AgentType: "code", ParentCallID: "call-1", BatchID: "batch-lost", Group: "group-lost", WorktreePath: "/wt/a2"}}},
		{name: "orphan then ledger", messages: []agent.Message{{Role: agent.MessageRoleAssistant, ToolCalls: []agent.ToolCall{{ID: "call-1", Name: "sub_agent", Arguments: map[string]any{"task": "lost task"}}}}}, lifecycle: []string{"accepted:a1:call-1:batch-lost:group-lost", "started:a1:call-1"}},
		{name: "completed reused ID then orphan", messages: []agent.Message{delegateCall("call-1", "sub_agent", "done"), {Role: agent.MessageRoleTool, ToolCallID: "call-1", Name: "sub_agent", Content: `{"output":"done"}`, Retention: &agent.MessageRetention{Status: "completed", AgentID: "old-agent"}}, {Role: agent.MessageRoleAssistant, ToolCalls: []agent.ToolCall{{ID: "call-1", Name: "sub_agent", Arguments: map[string]any{"task": "lost task"}}}}}, lifecycle: []string{"started:old-agent:", "accepted:a1:call-1:batch-lost:group-lost", "started:a1:call-1"}},
		{name: "single unknown running with result", messages: []agent.Message{delegateCall("call-1", "sub_agent", "live task"), ackResult(t, "call-1", "sub_agent", "unknown-agent", "running"), {Role: agent.MessageRoleUser, Source: agent.MessageSourceSubAgentResult, Content: agent.RenderSubAgentResultEnvelope(agent.SubAgentCompletion{Seq: 1, ParentCallID: "call-1", AgentID: "a1", AgentType: "code", Status: "completed", Body: `{"output":"done"}`})}}, lifecycle: []string{"accepted:a1:call-1:batch-lost:group-lost", "started:a1:call-1"}},
		{name: "latest generation only", messages: nil, lifecycle: []string{"accepted:a1:call-1:batch-lost:group-lost", "started:a1:call-1"}},
		{name: "reserved name not acceptance", messages: []agent.Message{{Role: agent.MessageRoleAssistant, ToolCalls: []agent.ToolCall{{ID: "call-1", Name: "sub_agent", Arguments: map[string]any{"task": "reserved task", "group": "reserved"}}}}}, groups: &agent.DelegationGroupLedger{Version: 1, Names: []string{"reserved"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			store := newMockSessionStore()
			base := append([]agent.Message{{Role: agent.MessageRoleUser, Content: "start"}}, tt.messages...)
			base = append(base, agent.Message{Role: agent.MessageRoleAssistant, Content: "marker"})
			ledger := tt.ledger
			if ledger == nil && tt.name != "reserved name not acceptance" {
				ledger = []agent.SubAgentLedgerEntry{{AgentID: "a1", AgentType: "code", ParentCallID: "call-1", BatchID: "batch-lost", Group: "group-lost", WorktreePath: "/wt/a1"}}
			}
			lineage := lineageFromMessages(base)
			if tt.name == "latest generation only" {
				lineage = agent.ConversationLineage{Generations: []agent.ConversationGeneration{
					{ID: 1, Messages: []agent.Message{{Role: agent.MessageRoleUser, Content: "start"}, delegateCall("call-1", "sub_agent", "old"), admissionResult("call-1", "old-agent", "", ""), {Role: agent.MessageRoleTool, ToolCallID: "call-1", Name: "sub_agent", Content: `{"output":"old result"}`}, {Role: agent.MessageRoleAssistant, Content: "marker"}}},
					{ID: 2, Messages: []agent.Message{{Role: agent.MessageRoleUser, Content: "start"}, delegateCall("call-1", "sub_agent", "new"), ackResult(t, "call-1", "sub_agent", "unknown-agent", "running"), {Role: agent.MessageRoleAssistant, Content: "marker"}}},
				}, NextGenerationID: 3}
				base = lineage.Generations[1].Messages
			}
			store.loadedSessions["old"] = session.Session{
				ID: "old", Model: "m", Lineage: lineage,
				SubAgentLedger: ledger, DelegationGroups: tt.groups,
			}
			var eventMu sync.Mutex
			var events []output.Event
			capture := output.SinkFunc(func(event output.Event) {
				eventMu.Lock()
				defer eventMu.Unlock()
				events = append(events, event)
			})
			baseDeps := Dependencies{BaseEvents: capture, SessionStore: store, Background: &ledgerBackground{}, Clock: &manualClock{}}
			s := testNewSession(t, baseDeps)
			eventSnapshot := func() []output.Event {
				eventMu.Lock()
				defer eventMu.Unlock()
				return slices.Clone(events)
			}
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
			firstEvents := eventSnapshot()
			var lifecycle []string
			for _, event := range firstEvents {
				switch event.Type {
				case output.EventTypeDelegationAccepted:
					p := event.Payload.(output.DelegationAcceptedEvent)
					lifecycle = append(lifecycle, "accepted:"+p.AgentID+":"+p.CallID+":"+p.BatchID+":"+p.Group)
				case output.EventTypeDelegationStarted:
					p := event.Payload.(output.DelegationStartedEvent)
					lifecycle = append(lifecycle, "started:"+p.AgentID+":"+p.CallID)
				case output.EventTypeAssistantMessage:
					p := event.Payload.(output.AssistantMessageEvent)
					if p.Content == "marker" {
						lifecycle = append(lifecycle, "marker")
					}
				}
			}
			if tt.name == "latest generation only" && (slices.Contains(lifecycle, "started:old-agent:") || slices.Contains(lifecycle, "accepted:old-agent:call-1::")) {
				t.Fatalf("earlier generation lifecycle replayed: %v", lifecycle)
			}
			if !slices.Equal(lifecycle[:len(tt.lifecycle)], tt.lifecycle) || lifecycle[len(tt.lifecycle)] != "marker" {
				t.Fatalf("replayed lifecycle before marker = %v, want %v then marker", lifecycle, tt.lifecycle)
			}
			var failures []string
			for _, event := range firstEvents {
				if event.Type == output.EventTypeDelegationFailed {
					p := event.Payload.(output.DelegationFailedEvent)
					failures = append(failures, p.AgentID+":"+p.Error)
				}
			}
			if !slices.Equal(failures, tt.failed) {
				t.Fatalf("replayed failures = %v, want %v", failures, tt.failed)
			}
			if tt.groups != nil {
				store.mu.Lock()
				saved := store.savedSessions["old"]
				store.mu.Unlock()
				if len(saved.SubAgentLedger) != 0 {
					t.Fatalf("reserved-name session ledger = %+v, want nil", saved.SubAgentLedger)
				}
				if accepted := len(eventsOfType(firstEvents, output.EventTypeDelegationAccepted)); accepted != 0 {
					t.Fatalf("reserved group accepted = %d, want 0", accepted)
				}
				if started := len(eventsOfType(firstEvents, output.EventTypeDelegationStarted)); started != 0 {
					t.Fatalf("reserved group started = %d, want 0", started)
				}
				if len(s.Conversation()) != len(base) || !slices.Equal(s.delegationGroups.Names, tt.groups.Names) {
					t.Fatalf("reserved groups or conversation changed: %+v / %d", s.delegationGroups, len(s.Conversation()))
				}
				return
			}
			liveDelivered := 0
			for _, event := range firstEvents {
				if event.Type == output.EventTypeSubAgentsDelivered {
					for _, item := range event.Payload.(output.SubAgentsDeliveredEvent).Items {
						if item.AgentID == "a1" && item.Status == "lost" && item.ParentCallID == "call-1" {
							liveDelivered++
						}
					}
				}
			}
			if liveDelivered != 1 {
				t.Fatalf("LIVE per-agent lost delivery records = %d, want 1", liveDelivered)
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
			var replayDelivered int
			for _, event := range eventSnapshot() {
				if event.Type != output.EventTypeSubAgentsDelivered {
					continue
				}
				for _, item := range event.Payload.(output.SubAgentsDeliveredEvent).Items {
					if item.AgentID == "a1" && item.Status == "lost" && item.ParentCallID == "call-1" {
						replayDelivered++
					}
				}
			}
			if replayDelivered != 2 {
				t.Fatalf("per-agent lost deliveries after two loads = %d, want one per load", replayDelivered)
			}
			if runs.Load() != 0 {
				t.Fatalf("runs after reload = %d, want 0", runs.Load())
			}
		})
	}
}

func TestEpisodeBudgetReachesTheRunner(t *testing.T) {
	t.Parallel()
	const budget, used = 1000, 300
	tests := []struct {
		name   string
		budget int
		want   [3]int
	}{
		{name: "limited budget is shared then reset", budget: budget, want: [3]int{budget, budget - used, budget}},
		{name: "unlimited budget passes zero", budget: 0, want: [3]int{0, 0, 0}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rec := &sinkRecorder{}
			clock := &manualClock{}
			bg := &ledgerBackground{}
			bg.setPending("a1", "a2")
			s := testNewSession(t, Dependencies{
				SessionStore:        newMockSessionStore(),
				Background:          bg,
				SetCompletionSink:   rec.set,
				Clock:               clock,
				MaxTokensPerEpisode: tt.budget,
			})
			inputs := make(chan RunInput, 3)
			s.SetRunner(&inputRunner{run: func(_ context.Context, in RunInput) (RunResult, error) {
				inputs <- in
				res := withAnswer(in, "ok")
				res.TokenCount = used
				return res, nil
			}})

			submitAndWait(t, s, "first", nil)
			if got := recv(t, inputs, "first run").MaxTokens; got != tt.want[0] {
				t.Fatalf("first run MaxTokens = %d, want %d", got, tt.want[0])
			}
			rec.get().DeliverCompletions([]agent.SubAgentCompletion{{
				ParentCallID: "call-1", AgentID: "a1", AgentType: "explore", Status: "complete",
				Body: agent.FailureBody("complete", "done"),
			}})
			clock.fireAll()
			if got := recv(t, inputs, "wake run").MaxTokens; got != tt.want[1] {
				t.Fatalf("wake run MaxTokens = %d, want %d", got, tt.want[1])
			}
			waitForState(t, s, agent.DriverWaiting)
			submitAndWait(t, s, "second", nil)
			if got := recv(t, inputs, "second run").MaxTokens; got != tt.want[2] {
				t.Fatalf("run after Submit MaxTokens = %d, want %d", got, tt.want[2])
			}
		})
	}
}

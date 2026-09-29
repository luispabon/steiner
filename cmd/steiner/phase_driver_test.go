package main

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/delegation"
	"github.com/luispabon/steiner/internal/oneshot"
)

const phaseDriverTestTimeout = 5 * time.Second

// phaseBackground is a BackgroundAgents fake whose pending agent "a" is
// released by MarkDelivered("call-a").
type phaseBackground struct {
	mu        sync.Mutex
	pending   []agent.PendingSubAgent
	delivered []string
}

func (b *phaseBackground) addPending(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.pending = append(b.pending, agent.PendingSubAgent{AgentID: id, AgentType: "code", State: agent.SubAgentRunning})
}

func (b *phaseBackground) Pending() []agent.PendingSubAgent {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.pending)
}

func (b *phaseBackground) HasPending() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.pending) > 0
}

func (b *phaseBackground) MarkDelivered(ids []string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.delivered = append(b.delivered, ids...)
	b.pending = slices.DeleteFunc(b.pending, func(p agent.PendingSubAgent) bool {
		return slices.Contains(ids, "call-"+p.AgentID)
	})
}

func (b *phaseBackground) Ledger() []agent.SubAgentLedgerEntry { return nil }
func (b *phaseBackground) SealBatch(string)                    {}

func (b *phaseBackground) deliveredIDs() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.delivered)
}

// phaseHarness records the order of shutdown and session saves.
type phaseHarness struct {
	mu     sync.Mutex
	order  []string
	causes []delegation.CancelCause
	bg     *phaseBackground
	sink   agent.CompletionSink
	// onShutdown mimics the supervisor releasing its children's results.
	onShutdown func(sink agent.CompletionSink)
}

func (h *phaseHarness) log(entry string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.order = append(h.order, entry)
}

func (h *phaseHarness) snapshotOrder() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.order)
}

func (h *phaseHarness) host(run agent.DriverRunFunc) phaseDriverHost {
	rec := &driverRunRecord{}
	return phaseDriverHost{
		run: func(ctx context.Context, in agent.DriverRunInput) (agent.DriverRunOutput, error) {
			out, err := run(ctx, in)
			rec.record(runResult{TokenCount: out.TokenCount, StopReason: out.StopReason}, err)
			return out, err
		},
		record:     rec,
		background: h.bg,
		setSink:    func(sink agent.CompletionSink) { h.sink = sink },
		shutdown: func(_ context.Context, cause delegation.CancelCause) {
			h.log("shutdown")
			h.mu.Lock()
			h.causes = append(h.causes, cause)
			h.mu.Unlock()
			if h.onShutdown != nil {
				h.onShutdown(h.sink)
			}
		},
	}
}

func (h *phaseHarness) input() oneshot.PhaseRunInput {
	return oneshot.PhaseRunInput{
		Conversation: []agent.Message{{Role: agent.MessageRoleUser, Content: "do the phase"}},
		Session: oneshot.PhaseSession{
			ID: "phase-session",
			Save: func(context.Context, agent.DriverSnapshot) error {
				h.log("save")
				return nil
			},
		},
	}
}

func assistantReply(text string, conv []agent.Message) []agent.Message {
	return append(slices.Clone(conv), agent.Message{Role: agent.MessageRoleAssistant, Content: text})
}

func TestRunPhaseOnDriverSuccessSkipsShutdown(t *testing.T) {
	h := &phaseHarness{bg: &phaseBackground{}}
	host := h.host(func(_ context.Context, in agent.DriverRunInput) (agent.DriverRunOutput, error) {
		return agent.DriverRunOutput{Conversation: assistantReply("finished", in.Conversation), TokenCount: 7, StopReason: agent.StopReasonComplete}, nil
	})

	result, err := runPhaseOnDriver(context.Background(), h.input(), host)
	if err != nil {
		t.Fatalf("runPhaseOnDriver() error = %v", err)
	}
	if result.Reply != "finished" || result.TokenCount != 7 || result.StopReason != agent.StopReasonComplete {
		t.Fatalf("result = reply %q tokens %d stop %q, want finished/7/complete", result.Reply, result.TokenCount, result.StopReason)
	}
	if len(result.Conversation) != 2 {
		t.Fatalf("conversation len = %d, want prompt + reply", len(result.Conversation))
	}
	if slices.Contains(h.snapshotOrder(), "shutdown") {
		t.Fatalf("order = %v, want no shutdown on success", h.snapshotOrder())
	}
}

func TestRunPhaseOnDriverWaitsForBackgroundChild(t *testing.T) {
	h := &phaseHarness{bg: &phaseBackground{}}
	ran := make(chan struct{})
	host := h.host(func(_ context.Context, in agent.DriverRunInput) (agent.DriverRunOutput, error) {
		h.bg.addPending("a")
		close(ran)
		return agent.DriverRunOutput{Conversation: assistantReply("launched a", in.Conversation)}, nil
	})

	returned := make(chan error, 1)
	go func() {
		_, err := runPhaseOnDriver(context.Background(), h.input(), host)
		returned <- err
	}()

	<-ran
	select {
	case err := <-returned:
		t.Fatalf("phase returned (%v) while a sub-agent was still pending", err)
	case <-time.After(100 * time.Millisecond):
	}

	h.sink.DeliverCompletions([]agent.SubAgentCompletion{{Seq: 1, ParentCallID: "call-a", AgentID: "a", AgentType: "code", Status: "completed", Quiet: true}})
	select {
	case err := <-returned:
		if err != nil {
			t.Fatalf("runPhaseOnDriver() error = %v", err)
		}
	case <-time.After(phaseDriverTestTimeout):
		t.Fatal("phase did not return after the child's result was delivered")
	}
	if got := h.bg.deliveredIDs(); !slices.Equal(got, []string{"call-a"}) {
		t.Fatalf("delivered = %v, want [call-a]", got)
	}
}

func TestRunPhaseOnDriverBudgetExhaustionFailsPhase(t *testing.T) {
	h := &phaseHarness{bg: &phaseBackground{}}
	h.onShutdown = func(sink agent.CompletionSink) {
		sink.DeliverCompletions([]agent.SubAgentCompletion{{Seq: 1, ParentCallID: "call-a", AgentID: "a", AgentType: "code", Status: "cancelled", Quiet: true}})
	}
	host := h.host(func(_ context.Context, in agent.DriverRunInput) (agent.DriverRunOutput, error) {
		h.bg.addPending("a")
		return agent.DriverRunOutput{Conversation: assistantReply("launched a", in.Conversation), TokenCount: 10}, nil
	})
	host.maxTokensPerEpisode = 10

	_, err := runPhaseOnDriver(context.Background(), h.input(), host)
	if !errors.Is(err, agent.ErrEpisodeBudgetExhausted) {
		t.Fatalf("error = %v, want ErrEpisodeBudgetExhausted", err)
	}
	assertShutdownBeforeFinalSave(t, h)
	if got := h.bg.deliveredIDs(); !slices.Equal(got, []string{"call-a"}) {
		t.Fatalf("delivered = %v, want the cancelled child settled into the conversation", got)
	}
	if !slices.Equal(h.causes, []delegation.CancelCause{delegation.CancelCauseSystem}) {
		t.Fatalf("shutdown causes = %v, want [system]", h.causes)
	}
}

func TestRunPhaseOnDriverCancellationShutsDownBeforeClose(t *testing.T) {
	h := &phaseHarness{bg: &phaseBackground{}}
	started := make(chan struct{})
	runReturned := make(chan struct{})
	host := h.host(func(ctx context.Context, in agent.DriverRunInput) (agent.DriverRunOutput, error) {
		defer close(runReturned)
		close(started)
		<-ctx.Done()
		return agent.DriverRunOutput{Conversation: in.Conversation}, ctx.Err()
	})

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := runPhaseOnDriver(ctx, h.input(), host)
		errCh <- err
	}()
	<-started
	cancel()

	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled", err)
		}
	case <-time.After(phaseDriverTestTimeout):
		t.Fatal("phase did not return after cancellation")
	}
	select {
	case <-runReturned:
	default:
		t.Fatal("run was still in flight when the phase returned; the driver loop was not joined")
	}
	assertShutdownBeforeFinalSave(t, h)
}

func TestRunPhaseOnDriverRunFailureFailsPhase(t *testing.T) {
	h := &phaseHarness{bg: &phaseBackground{}}
	boom := errors.New("provider down")
	host := h.host(func(_ context.Context, in agent.DriverRunInput) (agent.DriverRunOutput, error) {
		h.bg.addPending("a")
		return agent.DriverRunOutput{Conversation: in.Conversation}, boom
	})

	_, err := runPhaseOnDriver(context.Background(), h.input(), host)
	if !errors.Is(err, boom) {
		t.Fatalf("error = %v, want the run failure", err)
	}
	assertShutdownBeforeFinalSave(t, h)
}

func TestRunPhaseOnDriverRequiresTrailingUserMessage(t *testing.T) {
	h := &phaseHarness{bg: &phaseBackground{}}
	in := h.input()
	in.Conversation = []agent.Message{{Role: agent.MessageRoleAssistant, Content: "hi"}}
	if _, err := runPhaseOnDriver(context.Background(), in, h.host(nil)); err == nil {
		t.Fatal("runPhaseOnDriver() succeeded, want an error")
	}
}

func assertShutdownBeforeFinalSave(t *testing.T, h *phaseHarness) {
	t.Helper()
	order := h.snapshotOrder()
	shutdown := slices.Index(order, "shutdown")
	final := -1
	for i, entry := range order {
		if entry == "save" {
			final = i
		}
	}
	if shutdown < 0 {
		t.Fatalf("order = %v, want a shutdown", order)
	}
	if final < shutdown {
		t.Fatalf("order = %v, want the driver's final save after shutdown", order)
	}
}

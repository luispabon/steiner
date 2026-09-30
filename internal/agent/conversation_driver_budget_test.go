package agent

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/output"
)

func budgetHarness(t *testing.T, budget int) *driverHarness {
	t.Helper()
	return newDriverHarnessOpts(t, nil, nil, func(o *DriverOptions) { o.MaxTokensPerEpisode = budget })
}

func (c *runCall) finishWith(tokens int, reason StopReason) {
	conv := append(slices.Clone(c.in.Conversation), Message{Role: MessageRoleAssistant, Content: "ok"})
	c.release <- driverTestResult{out: DriverRunOutput{Conversation: conv, TokenCount: tokens, StopReason: reason}}
}

func (h *driverHarness) waitBudgetExhausted() {
	h.t.Helper()
	for {
		select {
		case e := <-h.events:
			if p, ok := e.Payload.(output.ConversationStateEvent); ok && p.BudgetExhausted {
				return
			}
		case <-time.After(driverTestTimeout):
			h.t.Fatal("timed out waiting for a budget-exhausted state event")
		}
	}
}

func (h *driverHarness) waitFor(desc string, cond func() bool) {
	h.t.Helper()
	timeout := time.After(driverTestTimeout)
	for {
		h.d.mu.Lock()
		ok := cond()
		ch := h.d.changed
		h.d.mu.Unlock()
		if ok {
			return
		}
		select {
		case <-ch:
		case <-timeout:
			h.t.Fatalf("timed out waiting for %s", desc)
		}
	}
}

func TestConversationDriverEpisodeBudgetSharedAndResetBySubmit(t *testing.T) {
	t.Parallel()
	h := budgetHarness(t, 1000)
	h.bg.setPending("a", "b")
	h.start()

	h.d.Submit("go", nil, SubmitMeta{})
	first := h.nextRun()
	if first.in.MaxTokens != 1000 {
		t.Fatalf("first MaxTokens = %d, want 1000", first.in.MaxTokens)
	}
	first.finishWith(400, StopReasonComplete)
	h.waitSettled(DriverWaiting, false)

	h.d.DeliverCompletions([]SubAgentCompletion{completionFor(1, "a")})
	h.clock.fire()
	second := h.nextRun()
	if second.in.MaxTokens != 600 {
		t.Fatalf("wake MaxTokens = %d, want the remaining 600", second.in.MaxTokens)
	}
	second.finishWith(100, StopReasonComplete)
	h.waitSettled(DriverWaiting, false)

	h.d.Submit("again", nil, SubmitMeta{})
	third := h.nextRun()
	if third.in.MaxTokens != 1000 {
		t.Fatalf("MaxTokens after submit = %d, want a fresh 1000", third.in.MaxTokens)
	}
	third.finishWith(0, StopReasonComplete)
}

func TestConversationDriverUnlimitedBudgetPassesZero(t *testing.T) {
	t.Parallel()
	h := budgetHarness(t, 0)
	h.start()

	h.d.Submit("go", nil, SubmitMeta{})
	call := h.nextRun()
	if call.in.MaxTokens != 0 {
		t.Fatalf("MaxTokens = %d, want 0 (host default) when unlimited", call.in.MaxTokens)
	}
	call.finishWith(1_000_000, StopReasonComplete)
	h.waitQuiescent()
}

func TestConversationDriverBudgetExhaustionSettlesQuietly(t *testing.T) {
	t.Parallel()
	h := budgetHarness(t, 500)
	h.bg.setPending("a", "b")
	h.start()

	h.d.Submit("go", nil, SubmitMeta{})
	h.nextRun().finishWith(500, StopReasonMaxTokens)
	h.waitBudgetExhausted()

	h.d.DeliverCompletions([]SubAgentCompletion{completionFor(1, "a")})
	h.waitFor("a to be recorded", func() bool { return len(h.d.completions) == 0 })
	h.noRun()
	if got := h.clock.armed(); got != 0 {
		t.Fatalf("armed timers = %d, want none while exhausted", got)
	}
	if got := h.bg.deliveredIDs(); !slices.Equal(got, []string{"call-a"}) {
		t.Fatalf("delivered = %v, want [call-a]", got)
	}

	ctx, cancel := context.WithTimeout(context.Background(), driverTestTimeout)
	defer cancel()
	if err := h.d.WaitQuiescent(ctx); !errors.Is(err, ErrEpisodeBudgetExhausted) {
		t.Fatalf("WaitQuiescent = %v, want ErrEpisodeBudgetExhausted", err)
	}

	h.d.Submit("more", nil, SubmitMeta{})
	call := h.nextRun()
	if call.in.MaxTokens != 500 {
		t.Fatalf("MaxTokens after submit = %d, want a fresh 500", call.in.MaxTokens)
	}
	call.finishWith(0, StopReasonComplete)
}

func TestConversationDriverBudgetSpentWithoutStopReasonStartsNoRun(t *testing.T) {
	t.Parallel()
	h := budgetHarness(t, 100)
	h.bg.setPending("a", "b")
	h.start()

	h.d.Submit("go", nil, SubmitMeta{})
	h.nextRun().finishWith(100, StopReasonComplete)
	h.waitBudgetExhausted()

	h.d.DeliverCompletions([]SubAgentCompletion{completionFor(1, "a")})
	h.waitFor("a to be recorded", func() bool { return len(h.d.completions) == 0 })
	h.noRun()
}

func TestConversationDriverBudgetExhaustedLastResultReturnsToIdle(t *testing.T) {
	t.Parallel()
	h := budgetHarness(t, 100)
	h.bg.setPending("a")
	h.start()

	h.d.Submit("go", nil, SubmitMeta{})
	h.nextRun().finishWith(100, StopReasonMaxTokens)
	h.waitBudgetExhausted()
	h.d.DeliverCompletions([]SubAgentCompletion{completionFor(1, "a")})
	h.waitSettled(DriverIdle, false)
	h.noRun()
	h.waitQuiescent()
}

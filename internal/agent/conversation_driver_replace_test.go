package agent

import (
	"testing"
	"time"
)

// newHeldDriverHarness returns a running driver stopped in the waiting-held
// state, with one sub-agent still pending.
func newHeldDriverHarness(t *testing.T) *driverHarness {
	t.Helper()
	h := newDriverHarness(t, nil, nil)
	h.bg.setPending("a")
	h.start()
	h.d.Submit("go", nil, SubmitMeta{})
	call := h.nextRun()
	h.d.StopTurn()
	<-call.ctx.Done()
	h.waitSettled(DriverWaiting, true)
	return h
}

func TestConversationDriverReplaceWhileHeldOutcomes(t *testing.T) {
	t.Parallel()

	t.Run("not held", func(t *testing.T) {
		h := newDriverHarness(t, nil, nil)
		called := false
		outcome := h.d.ReplaceWhileHeld(
			func() bool { called = true; return true },
			func() { called = true },
		)
		if outcome != ReplaceNotHeld {
			t.Fatalf("outcome = %v, want ReplaceNotHeld", outcome)
		}
		if called {
			t.Fatal("callbacks ran for an unheld driver")
		}
	})

	t.Run("busy", func(t *testing.T) {
		h := newDriverHarness(t, nil, nil)
		h.bg.setPending("a")
		h.start()
		h.d.Submit("go", nil, SubmitMeta{})
		call := h.nextRun()
		called := false
		outcome := h.d.ReplaceWhileHeld(
			func() bool { called = true; return true },
			func() { called = true },
		)
		if outcome != ReplaceBusy {
			t.Fatalf("outcome = %v, want ReplaceBusy", outcome)
		}
		if called {
			t.Fatal("callbacks ran for a busy driver")
		}
		if err := call.ctx.Err(); err != nil {
			t.Fatalf("replace cancelled the run: %v", err)
		}
		call.finish()
		h.waitSettled(DriverWaiting, false)
	})

	t.Run("refused keeps hold", func(t *testing.T) {
		h := newHeldDriverHarness(t)
		swapped := false
		outcome := h.d.ReplaceWhileHeld(
			func() bool { return false },
			func() { swapped = true },
		)
		if outcome != ReplaceRefused {
			t.Fatalf("outcome = %v, want ReplaceRefused", outcome)
		}
		if swapped {
			t.Fatal("swap ran after check refused")
		}
		if state, held := h.d.State(); state != DriverWaiting || !held {
			t.Fatalf("State = %s held=%v, want waiting held", state, held)
		}
	})

	t.Run("done detaches and swaps", func(t *testing.T) {
		h := newHeldDriverHarness(t)
		swapped := false
		outcome := h.d.ReplaceWhileHeld(
			func() bool { return true },
			func() { swapped = true },
		)
		if outcome != ReplaceDone {
			t.Fatalf("outcome = %v, want ReplaceDone", outcome)
		}
		if !swapped {
			t.Fatal("swap did not run after check accepted")
		}
		h.d.mu.Lock()
		steers := h.d.opts.Steers
		h.d.mu.Unlock()
		if steers != nil {
			t.Fatal("steer queue still attached")
		}
		if _, held := h.d.State(); !held {
			t.Fatal("replacement released the hold")
		}
	})
}

func TestConversationDriverReplaceWhileHeldBlocksCompletionDelivery(t *testing.T) {
	t.Parallel()
	h := newHeldDriverHarness(t)

	entered := make(chan struct{})
	release := make(chan struct{})
	outcome := make(chan ReplaceOutcome, 1)
	go func() {
		outcome <- h.d.ReplaceWhileHeld(
			func() bool { close(entered); <-release; return true },
			func() {},
		)
	}()
	<-entered

	delivered := make(chan struct{})
	go func() {
		h.d.DeliverCompletions([]SubAgentCompletion{completionFor(1, "a")})
		close(delivered)
	}()
	select {
	case <-delivered:
		t.Fatal("completion delivered while replacement held the driver lock")
	case <-time.After(50 * time.Millisecond):
	}

	close(release)
	if got := <-outcome; got != ReplaceDone {
		t.Fatalf("outcome = %v, want ReplaceDone", got)
	}
	select {
	case <-delivered:
	case <-time.After(driverTestTimeout):
		t.Fatal("completion delivery did not finish")
	}
	h.noRun()
	if _, held := h.d.State(); !held {
		t.Fatal("replacement released the hold")
	}
}

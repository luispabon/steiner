package agent

import (
	"context"
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
	ctx, cancel := context.WithTimeout(context.Background(), driverTestTimeout)
	defer cancel()
	if err := h.d.WaitIdle(ctx); err != nil {
		t.Fatalf("WaitIdle: %v", err)
	}
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
		h.d.DeliverCompletions([]SubAgentCompletion{completionFor(1, "a")})
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
		completions := len(h.d.completions)
		h.d.mu.Unlock()
		if steers != nil {
			t.Fatal("steer queue still attached")
		}
		if completions != 0 {
			t.Fatalf("buffered completions = %d, want discarded", completions)
		}
		// held is not asserted: the replacement closes the old inbox, so the
		// loop is free to exit and finalize clears held at any time. The
		// assertions above cover effects that outlive that.
		if !h.d.isClosing() {
			t.Fatal("replaced driver inbox still open")
		}
	})
}

// A coalescing window cannot be armed while the driver is held, so the held
// fixture above never arms one. This test arms a window while the driver is
// waiting, stops the turn, then verifies the replacement disarms it.
func TestConversationDriverReplaceWhileHeldDisarmsArmedWindow(t *testing.T) {
	t.Parallel()
	h := newDriverHarness(t, nil, nil)
	h.bg.setPending("a", "b")
	h.start()

	h.d.Submit("go", nil, SubmitMeta{})
	h.nextRun().finish()
	h.waitSettled(DriverWaiting, false)

	h.bg.markFinished("a")
	h.d.DeliverCompletions([]SubAgentCompletion{completionFor(1, "a")})
	if got := h.clock.armed(); got != 1 {
		t.Fatalf("armed timers = %d, want the coalescing window", got)
	}
	h.d.StopTurn()
	ctx, cancel := context.WithTimeout(context.Background(), driverTestTimeout)
	defer cancel()
	if err := h.d.WaitIdle(ctx); err != nil {
		t.Fatalf("WaitIdle: %v", err)
	}
	if state, held := h.d.State(); state != DriverWaiting || !held {
		t.Fatalf("State = %s held=%v, want waiting held", state, held)
	}

	swapped := false
	// swap runs under the driver lock after the discard and before closing, so
	// the captured window cannot have been cleared by finalize instead.
	var windowAtSwap Timer
	var armedAtSwap int
	outcome := h.d.ReplaceWhileHeld(
		func() bool { return true },
		func() {
			swapped = true
			windowAtSwap = h.d.window
			armedAtSwap = h.clock.armed()
		},
	)
	if outcome != ReplaceDone {
		t.Fatalf("outcome = %v, want ReplaceDone", outcome)
	}
	if !swapped {
		t.Fatal("swap did not run after check accepted")
	}
	if windowAtSwap != nil {
		t.Fatal("completion window still armed during swap")
	}
	if armedAtSwap != 0 {
		t.Fatalf("armed timers at swap = %d, want 0", armedAtSwap)
	}
	h.noRun()
	if !h.d.isClosing() {
		t.Fatal("replaced driver inbox still open")
	}
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
	if !h.d.isClosing() {
		t.Fatal("replaced driver inbox still open")
	}
}

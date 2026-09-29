package agent

import (
	"context"
	"slices"
	"time"
)

// completionCoalesceWindow is how long a wake completion arriving while the
// driver is stopped waits for further results before starting a run.
const completionCoalesceWindow = 5 * time.Second

// Inbox items are wake or quiet. Prompts and steers wake. Completions wake
// unless Quiet; while the budget is exhausted every completion is quiet, and
// while held completions only accumulate. Quiet items never start a run: they
// are appended by a settle transition.

func (d *ConversationDriver) hasWakeCompletionLocked() bool {
	return slices.ContainsFunc(d.completions, func(c SubAgentCompletion) bool { return !c.Quiet })
}

// wakeReadyLocked reports whether the inbox should start a run or continue the
// current sequence now. While generating, a wake completion counts at once;
// while stopped it counts only after the coalescing window expired.
func (d *ConversationDriver) wakeReadyLocked() bool {
	if len(d.users) > 0 || (d.opts.Steers.Len() > 0 && !d.steersParked) {
		return true
	}
	if d.held || d.exhausted || !d.hasWakeCompletionLocked() {
		return false
	}
	return d.state == DriverGenerating || d.windowExpired
}

// quietSettleableLocked reports whether a stopped driver should record its
// buffered completions without running the model. Held completions wait for
// the next prompt instead.
func (d *ConversationDriver) quietSettleableLocked() bool {
	if d.held || len(d.completions) == 0 {
		return false
	}
	return d.exhausted || !d.hasWakeCompletionLocked()
}

// settleQuietLocked appends everything deliverable as one message, marks it
// delivered, saves and moves to waiting or idle. It never calls Run.
func (d *ConversationDriver) settleQuietLocked(ctx context.Context) bool {
	if drain := d.drainLocked(DeliveryParts{}); drain.Message != nil {
		d.appendLocked(*drain.Message)
	}
	d.settleLocked()
	return d.saveAndUnlock(ctx, false)
}

// armWindowLocked starts the coalescing window for a wake completion arriving
// while stopped. Completions arriving while armed just accumulate.
func (d *ConversationDriver) armWindowLocked(cs []SubAgentCompletion) {
	if d.state == DriverGenerating || d.held || d.exhausted || d.window != nil || d.windowExpired {
		return
	}
	if !slices.ContainsFunc(cs, func(c SubAgentCompletion) bool { return !c.Quiet }) {
		return
	}
	gen := d.windowGen
	d.window = d.clock.AfterFunc(completionCoalesceWindow, func() { d.windowFired(gen) })
}

// disarmWindowLocked cancels the window and invalidates its callback.
func (d *ConversationDriver) disarmWindowLocked() {
	if d.window != nil {
		d.window.Stop()
	}
	d.window = nil
	d.windowGen++
	d.windowExpired = false
}

// windowFired runs on the clock's goroutine: it only flags the expiry and wakes
// the loop. A callback from a disarmed window is ignored.
func (d *ConversationDriver) windowFired(gen uint64) {
	d.mu.Lock()
	if gen != d.windowGen || d.closing {
		d.unlockEmit()
		return
	}
	d.window = nil
	d.windowExpired = true
	d.unlockEmit()
	d.signalWake()
}

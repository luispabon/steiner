package agent

import (
	"context"
	"slices"

	"github.com/luispabon/steiner/internal/output"
)

// DriverState is the conversation driver's state.
type DriverState string

const (
	// DriverIdle means the model is stopped and no sub-agent is pending.
	DriverIdle DriverState = "idle"
	// DriverGenerating means a run is in flight.
	DriverGenerating DriverState = "generating"
	// DriverWaiting means the model is stopped while sub-agents are pending or
	// completions are held.
	DriverWaiting DriverState = "waiting"
)

type driverStateKey struct {
	state   DriverState
	held    bool
	pending int
	budget  bool
}

// State returns the driver state and whether completions are held.
func (d *ConversationDriver) State() (DriverState, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.state, d.held
}

// Snapshot returns a copy of the driver's durable state.
func (d *ConversationDriver) Snapshot() DriverSnapshot {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.snapshotLocked()
}

func (d *ConversationDriver) snapshotLocked() DriverSnapshot {
	snap := DriverSnapshot{Conversation: slices.Clone(d.conv), Lineage: d.lineage.Clone()}
	if d.opts.Background != nil {
		snap.Ledger = d.opts.Background.Ledger()
	}
	return snap
}

func (d *ConversationDriver) hasPendingLocked() bool {
	return len(d.completions) > 0 || (d.opts.Background != nil && d.opts.Background.HasPending())
}

// settleLocked moves to the state a stopped model rests in. A hold with
// nothing to hold back is dropped.
func (d *ConversationDriver) settleLocked() {
	pending := d.hasPendingLocked()
	if !pending {
		d.held = false
	}
	if pending || d.held {
		d.state = DriverWaiting
		return
	}
	d.state = DriverIdle
	d.resetEpisodeLocked()
}

// StopTurn cancels the current run and holds completions: they accumulate
// without waking the model until the next prompt. With no run in flight it
// only holds, and only when something is pending.
func (d *ConversationDriver) StopTurn() {
	d.mu.Lock()
	defer d.unlockEmit()
	if d.state == DriverGenerating {
		d.stopLocked()
		return
	}
	if d.hasPendingLocked() {
		d.held = true
	}
}

// stopEpoch is StopTurn scoped to one run epoch; a stop for an epoch that has
// already ended must not cancel a later run.
func (d *ConversationDriver) stopEpoch(epoch uint64) {
	d.mu.Lock()
	defer d.unlockEmit()
	if d.state == DriverGenerating && d.epoch == epoch {
		d.stopLocked()
	}
}

func (d *ConversationDriver) stopLocked() {
	d.held = true
	if d.cancel != nil {
		d.cancel()
	}
}

// WaitQuiescent blocks until the driver is idle, has nothing queued and no
// sub-agent is pending, or ctx ends. It returns ErrEpisodeBudgetExhausted when
// the episode budget is spent while sub-agents are still pending.
func (d *ConversationDriver) WaitQuiescent(ctx context.Context) error {
	for {
		d.mu.Lock()
		if d.budgetBlockedLocked() {
			d.mu.Unlock()
			return ErrEpisodeBudgetExhausted
		}
		if d.quiescentLocked() {
			d.mu.Unlock()
			return nil
		}
		changed := d.changed
		d.mu.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (d *ConversationDriver) quiescentLocked() bool {
	return d.state == DriverIdle && !d.compacting && len(d.compactions) == 0 &&
		len(d.users) == 0 && d.opts.Steers.Len() == 0 && !d.hasPendingLocked()
}

// warnLocked queues a conversation warning event.
func (d *ConversationDriver) warnLocked(msg string) {
	d.pendingEvents = append(d.pendingEvents, output.NewConversationWarningEvent(msg))
}

// unlockEmit releases d.mu, first queuing a state event when state, held or
// the pending count changed, and wakes WaitQuiescent callers. Events are
// emitted after unlocking, in lock order, so sinks may call back into the
// driver.
func (d *ConversationDriver) unlockEmit() {
	close(d.changed)
	d.changed = make(chan struct{})
	if d.opts.Events == nil {
		d.pendingEvents = nil
		d.mu.Unlock()
		return
	}
	key := driverStateKey{state: d.state, held: d.held, budget: d.exhausted}
	if d.opts.Background != nil {
		key.pending = len(d.opts.Background.Pending())
	}
	if !d.emitted || key != d.lastEmitted {
		d.emitted = true
		d.lastEmitted = key
		d.pendingEvents = append(d.pendingEvents,
			output.NewConversationStateEvent(string(key.state), key.held, key.pending, key.budget))
	}
	events := d.pendingEvents
	d.pendingEvents = nil
	if len(events) == 0 {
		d.mu.Unlock()
		return
	}
	d.emitMu.Lock()
	d.mu.Unlock()
	defer d.emitMu.Unlock()
	for _, e := range events {
		d.opts.Events.Emit(e)
	}
}

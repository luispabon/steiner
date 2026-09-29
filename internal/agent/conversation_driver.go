package agent

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/luispabon/steiner/internal/output"
)

// DriverRunInput is what the driver hands the host's run function for one
// turn sequence.
type DriverRunInput struct {
	Conversation     []Message
	Lineage          ConversationLineage
	DrainInbox       func() InboxDrain
	OnToolBatchDone  func(batchID string)
	PendingSubAgents func() []PendingSubAgent
	// MaxTokens of 0 means the host default.
	MaxTokens int
}

// DriverRunOutput is what a run function returns to the driver.
type DriverRunOutput struct {
	Conversation []Message
	Lineage      ConversationLineage
	TokenCount   int
	StopReason   StopReason
	// SkipAdoption keeps the driver's conversation and adopts only the
	// lineage; set for workflow handoff.
	SkipAdoption bool
}

// DriverRunFunc runs one turn sequence. The host builds the RunRequest. It
// must return when ctx is cancelled and must not wait on sub-agents.
type DriverRunFunc func(ctx context.Context, in DriverRunInput) (DriverRunOutput, error)

// BackgroundAgents is the driver's view of the sub-agent supervisor.
type BackgroundAgents interface {
	Pending() []PendingSubAgent
	HasPending() bool
	MarkDelivered(parentCallIDs []string)
	Ledger() []SubAgentLedgerEntry
	SealBatch(batchID string)
}

// DriverSnapshot is the durable state saved at every driver transition.
type DriverSnapshot struct {
	Conversation []Message
	Lineage      ConversationLineage
	Ledger       []SubAgentLedgerEntry
}

// SubmitMeta carries host side effects attached to a submitted prompt. It has
// no fields yet; the interactive host extends it.
type SubmitMeta struct{}

// DriverOptions configures a ConversationDriver.
type DriverOptions struct {
	Run DriverRunFunc
	// Background may be nil when the session has no sub-agents.
	Background BackgroundAgents
	// Steers may be nil (headless oneshot).
	Steers *SteerQueue
	// Save persists a snapshot. A save error is reported as a warning event
	// and never stops the loop: losing one durability point is better than
	// wedging the conversation.
	Save   func(ctx context.Context, snap DriverSnapshot) error
	Events output.EventSink
	// Clock defaults to the real clock.
	Clock Clock
	// PrepareTurn supplies the mode notice and skill deltas prefixed to the
	// first message of each started sequence.
	PrepareTurn func(ctx context.Context, conv []Message) DeliveryParts
}

type compactionFunc func(ctx context.Context, conv []Message) ([]Message, ConversationLineage, error)

// ConversationDriver is the single writer of a conversation. One loop
// goroutine starts turn sequences from inbox items (prompts, steers, sub-agent
// completions), adopts each run's result and tracks whether the model is
// generating, idle or waiting on background sub-agents.
type ConversationDriver struct {
	opts  DriverOptions
	clock Clock

	// mu guards everything below. Emission of events is serialised by emitMu,
	// handed over from mu in unlockEmit so events keep their order.
	mu          sync.Mutex
	conv        []Message
	lineage     ConversationLineage
	state       DriverState
	held        bool
	epoch       uint64
	cancel      context.CancelFunc
	compacting  bool
	users       []SteerMessage
	completions []SubAgentCompletion
	compactions []compactionFunc
	started     bool
	closing     bool

	lastEmitted   driverStateKey
	emitted       bool
	pendingEvents []output.Event
	changed       chan struct{}
	emitMu        sync.Mutex

	wake chan struct{}
	done chan struct{}
}

// NewConversationDriver returns a driver over conv and lineage. It is inert
// until Start.
func NewConversationDriver(opts DriverOptions, conv []Message, lineage ConversationLineage) *ConversationDriver {
	clock := opts.Clock
	if clock == nil {
		clock = realClock{}
	}
	return &ConversationDriver{
		opts:    opts,
		clock:   clock,
		conv:    slices.Clone(conv),
		lineage: lineage.Clone(),
		state:   DriverIdle,
		changed: make(chan struct{}),
		wake:    make(chan struct{}, 1),
		done:    make(chan struct{}),
	}
}

// Start runs the loop goroutine until ctx is cancelled or Close is called.
// Later calls, and calls after Close, do nothing.
func (d *ConversationDriver) Start(ctx context.Context) {
	d.mu.Lock()
	if d.started || d.closing {
		d.mu.Unlock()
		return
	}
	d.started = true
	d.mu.Unlock()
	go d.loop(ctx)
}

func (d *ConversationDriver) loop(ctx context.Context) {
	defer close(d.done)
	defer func() { d.finalize(context.WithoutCancel(ctx)) }()
	for {
		select {
		case <-d.wake:
		case <-ctx.Done():
			return
		}
		for d.step(ctx) {
		}
		if d.isClosing() {
			return
		}
	}
}

func (d *ConversationDriver) isClosing() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.closing
}

func (d *ConversationDriver) signalWake() {
	select {
	case d.wake <- struct{}{}:
	default:
	}
}

// step does one unit of loop work and reports whether more may be ready.
func (d *ConversationDriver) step(ctx context.Context) bool {
	d.mu.Lock()
	if d.closing {
		d.unlockEmit()
		return false
	}
	between := d.state == DriverGenerating
	if !between && len(d.compactions) > 0 {
		return d.compact(ctx)
	}
	if d.held || !d.hasWakeLocked() {
		if !between {
			d.unlockEmit()
			return false
		}
		d.settleLocked()
		return d.saveAndUnlock(ctx, true)
	}
	conv := slices.Clone(d.conv)
	d.unlockEmit()

	var parts DeliveryParts
	if d.opts.PrepareTurn != nil {
		parts = d.opts.PrepareTurn(ctx, conv)
	}

	d.mu.Lock()
	if d.closing {
		d.unlockEmit()
		return false
	}
	drain := d.drainLocked(parts)
	if drain.Message == nil {
		// The wake item was taken back between the check and the drain.
		d.settleLocked()
		return d.saveAndUnlock(ctx, false)
	}
	d.conv = append(d.conv, *drain.Message)
	d.state = DriverGenerating
	d.epoch++
	runCtx, cancel := context.WithCancel(ctx)
	d.cancel = cancel
	in := DriverRunInput{
		Conversation:     slices.Clone(d.conv),
		Lineage:          d.lineage.Clone(),
		DrainInbox:       d.drainForRun,
		OnToolBatchDone:  d.sealer(),
		PendingSubAgents: d.pendingFn(),
	}
	snap := d.snapshotLocked()
	d.unlockEmit()
	d.save(ctx, snap)

	out, err := d.opts.Run(runCtx, in)
	cancel()
	d.finish(ctx, out, err)
	return true
}

func (d *ConversationDriver) sealer() func(string) {
	if d.opts.Background == nil {
		return nil
	}
	return d.opts.Background.SealBatch
}

func (d *ConversationDriver) pendingFn() func() []PendingSubAgent {
	if d.opts.Background == nil {
		return nil
	}
	return d.opts.Background.Pending
}

// finish adopts a returned run and, in the same critical section, either keeps
// generating (an item arrived after the last boundary drain) or settles.
func (d *ConversationDriver) finish(ctx context.Context, out DriverRunOutput, err error) {
	d.mu.Lock()
	d.cancel = nil
	if !out.SkipAdoption && len(out.Conversation) > 0 {
		d.conv = out.Conversation
	}
	if len(out.Lineage.Generations) > 0 {
		d.lineage = out.Lineage.Clone()
	}
	if err != nil && !errors.Is(err, context.Canceled) {
		d.warnLocked(fmt.Sprintf("run failed: %v", err))
	}
	if d.closing || d.held || !d.hasWakeLocked() {
		d.settleLocked()
	}
	d.saveAndUnlock(ctx, false)
}

// saveAndUnlock snapshots under the held lock, releases it and saves.
func (d *ConversationDriver) saveAndUnlock(ctx context.Context, more bool) bool {
	snap := d.snapshotLocked()
	d.unlockEmit()
	d.save(ctx, snap)
	return more
}

func (d *ConversationDriver) save(ctx context.Context, snap DriverSnapshot) {
	if d.opts.Save == nil {
		return
	}
	if err := d.opts.Save(ctx, snap); err != nil {
		d.mu.Lock()
		d.warnLocked(fmt.Sprintf("save conversation: %v", err))
		d.unlockEmit()
	}
}

// compact runs the next queued compaction with the lock held on entry.
func (d *ConversationDriver) compact(ctx context.Context) bool {
	fn := d.compactions[0]
	d.compactions = d.compactions[1:]
	d.compacting = true
	cctx, cancel := context.WithCancel(ctx)
	d.cancel = cancel
	conv := slices.Clone(d.conv)
	d.unlockEmit()

	newConv, lineage, err := fn(cctx, conv)
	cancel()

	d.mu.Lock()
	d.compacting = false
	d.cancel = nil
	switch {
	case err == nil:
		d.conv = newConv
		d.lineage = lineage.Clone()
		if d.opts.Background != nil {
			if msg, ok := BuildDeliveryMessage(DeliveryParts{Pending: d.opts.Background.Pending()}); ok {
				d.conv = append(d.conv, msg)
			}
		}
	case !errors.Is(err, context.Canceled):
		d.warnLocked(fmt.Sprintf("compact conversation: %v", err))
	}
	return d.saveAndUnlock(ctx, true)
}

// finalize settles every remaining inbox item quietly, without running the
// model, so nothing delivered to the ledger is missing from the conversation.
func (d *ConversationDriver) finalize(ctx context.Context) {
	d.mu.Lock()
	d.closing = true
	d.compactions = nil
	if drain := d.drainLocked(DeliveryParts{}); drain.Message != nil {
		d.conv = append(d.conv, *drain.Message)
	}
	d.held = false
	d.settleLocked()
	d.saveAndUnlock(ctx, false)
}

// Close cancels the current run, waits for it to return and adopts it, settles
// every remaining inbox item into the conversation without calling Run, then
// stops the loop. Queued compactions are dropped. It is idempotent, safe
// before Start and safe to call concurrently. If ctx ends first, Close returns
// while the loop finishes settling in the background.
func (d *ConversationDriver) Close(ctx context.Context) {
	d.mu.Lock()
	inline := false
	if !d.closing {
		d.closing = true
		if d.cancel != nil {
			d.cancel()
		}
		if !d.started {
			d.started = true
			inline = true
		}
	}
	d.mu.Unlock()

	if inline {
		d.finalize(ctx)
		close(d.done)
		return
	}
	d.signalWake()
	select {
	case <-d.done:
	case <-ctx.Done():
	}
}

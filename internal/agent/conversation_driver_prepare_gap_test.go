package agent

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// gatedPrepare blocks the driver's PrepareTurn call at index skip (0-based)
// until the test releases it, so the gap between the wake check and the drain
// is deterministic. Later calls return immediately.
type gatedPrepare struct {
	skip    int
	mu      sync.Mutex
	calls   int
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func newGatedPrepare(skip int) *gatedPrepare {
	return &gatedPrepare{skip: skip, entered: make(chan struct{}, 1), release: make(chan struct{})}
}

func (g *gatedPrepare) prepare(ctx context.Context, _ []Message) DeliveryParts {
	g.mu.Lock()
	block := g.calls == g.skip
	g.calls++
	g.mu.Unlock()
	if !block {
		return DeliveryParts{}
	}
	select {
	case g.entered <- struct{}{}:
	default:
	}
	select {
	case <-g.release:
	case <-ctx.Done():
	}
	return DeliveryParts{}
}

func (g *gatedPrepare) waitEntered(t *testing.T) {
	t.Helper()
	select {
	case <-g.entered:
	case <-time.After(driverTestTimeout):
		t.Fatal("timed out waiting for PrepareTurn")
	}
}

// open releases the gated call. It is idempotent so cleanup can call it after
// the test did.
func (g *gatedPrepare) open() {
	g.once.Do(func() { close(g.release) })
}

// changedChan returns the current change channel. The loop closes it the next
// time it releases the lock, which is its first move after PrepareTurn
// returns, so a waiter observes the step's decision.
func (h *driverHarness) changedChan() <-chan struct{} {
	h.d.mu.Lock()
	defer h.d.mu.Unlock()
	return h.d.changed
}

func waitClosed(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(driverTestTimeout):
		t.Fatal("timed out waiting for the driver to release the lock after PrepareTurn")
	}
}

func TestConversationDriverStopDuringPrepareTurnHoldsCompletions(t *testing.T) {
	t.Parallel()
	gate := newGatedPrepare(0)
	h := newDriverHarness(t, nil, gate.prepare)
	h.bg.setPending("a")
	h.bg.markFinished("a")
	h.start()
	t.Cleanup(gate.open)

	h.d.DeliverCompletions([]SubAgentCompletion{completionFor(1, "a")})
	gate.waitEntered(t)
	// The driver approved the run and is preparing it. Nothing is generating,
	// so the stop only holds the buffered completion.
	h.d.StopTurn()
	released := h.changedChan()
	gate.open()
	waitClosed(t, released)

	h.d.mu.Lock()
	state, held, buffered := h.d.state, h.d.held, slices.Clone(h.d.completions)
	h.d.mu.Unlock()
	if state == DriverGenerating {
		t.Fatal("driver started the run its wake check approved before the stop")
	}
	if !held {
		t.Fatal("stop during PrepareTurn did not hold the completion")
	}
	if len(buffered) != 1 || buffered[0].AgentID != "a" {
		t.Fatalf("buffered completions = %+v, want a kept for the next prompt", buffered)
	}
	if got := h.bg.deliveredIDs(); len(got) != 0 {
		t.Fatalf("delivered = %v, want nothing marked delivered while held", got)
	}
	h.noRun()

	h.d.Submit("resume", nil, SubmitMeta{})
	call := h.nextRun()
	if msg := lastContent(call); !strings.Contains(msg, `agent_id="a"`) || !strings.HasSuffix(msg, "resume") {
		t.Fatalf("resumed message = %q, want the held envelope with the prompt", msg)
	}
	if _, held := h.d.State(); held {
		t.Fatal("hold not lifted by Submit")
	}
	call.finish()
	h.waitQuiescent()
}

func TestConversationDriverStopDuringPrepareTurnEndsSequenceHeld(t *testing.T) {
	t.Parallel()
	gate := newGatedPrepare(1)
	h := newDriverHarness(t, nil, gate.prepare)
	h.bg.setPending("a")
	h.bg.markFinished("a")
	h.start()
	t.Cleanup(gate.open)

	h.d.Submit("go", nil, SubmitMeta{})
	first := h.nextRun()
	h.d.DeliverCompletions([]SubAgentCompletion{completionFor(1, "a")})
	first.finish()
	// The completion is ready, so the sequence continues and the driver
	// prepares its next run. The stop lands in that gap.
	gate.waitEntered(t)
	h.d.StopTurn()
	base := len(h.d.Snapshot().Conversation)
	released := h.changedChan()
	gate.open()
	waitClosed(t, released)

	h.d.mu.Lock()
	state, held, buffered, conv := h.d.state, h.d.held, len(h.d.completions), len(h.d.conv)
	h.d.mu.Unlock()
	if state != DriverWaiting || !held {
		t.Fatalf("state = %s held=%v, want waiting held after the stop", state, held)
	}
	if conv != base {
		t.Fatalf("conversation grew to %d messages, want %d: the drain ran despite the stop", conv, base)
	}
	if buffered != 1 {
		t.Fatalf("buffered completions = %d, want the completion held", buffered)
	}
	h.noRun()

	h.d.Submit("resume", nil, SubmitMeta{})
	next := h.nextRun()
	if msg := lastContent(next); !strings.Contains(msg, `agent_id="a"`) || !strings.HasSuffix(msg, "resume") {
		t.Fatalf("resumed message = %q, want the held envelope with the prompt", msg)
	}
	next.finish()
	h.waitQuiescent()
}

func TestConversationDriverWithdrawnSteerDuringPrepareTurnKeepsBufferedCompletion(t *testing.T) {
	t.Parallel()
	gate := newGatedPrepare(0)
	h := newDriverHarness(t, nil, gate.prepare)
	h.bg.setPending("a", "b")
	h.bg.markFinished("a")
	h.start()
	t.Cleanup(gate.open)

	// The finished sibling waits behind the coalescing window while b runs, so
	// only the steer makes the inbox ready.
	h.d.DeliverCompletions([]SubAgentCompletion{completionFor(1, "a")})
	if got := h.clock.armed(); got != 1 {
		t.Fatalf("armed timers = %d, want the window armed by the finished sibling", got)
	}
	h.noRun()
	h.steers.Add(SteerMessage{Text: "take me back"})
	h.d.NotifySteer()
	gate.waitEntered(t)
	// The user takes the steer back into the input box while the driver is
	// preparing the run.
	if drained := h.steers.Drain(); len(drained) != 1 {
		t.Fatalf("drained steers = %v, want the steer taken back", drained)
	}
	gate.open()

	// The withdrawn steer does not cancel the run: the buffered completion
	// still starts one, as it did before the stop fix.
	call := h.nextRun()
	if msg := lastContent(call); !strings.Contains(msg, `agent_id="a"`) || strings.Contains(msg, "take me back") {
		t.Fatalf("message = %q, want the buffered sibling envelope without the withdrawn steer", msg)
	}
	if got := h.clock.armed(); got != 0 {
		t.Fatalf("armed timers = %d, want the window disarmed by the drain", got)
	}
	call.finish()
	h.waitSettled(DriverWaiting, false)
	h.noRun()
}

func TestConversationDriverWithdrawnSteerDuringPrepareTurnSettlesAndSaves(t *testing.T) {
	t.Parallel()
	gate := newGatedPrepare(0)
	h := newDriverHarness(t, nil, gate.prepare)
	h.start()
	t.Cleanup(gate.open)

	h.steers.Add(SteerMessage{Text: "withdraw me"})
	h.d.NotifySteer()
	gate.waitEntered(t)
	if drained := h.steers.Drain(); len(drained) != 1 {
		t.Fatalf("drained steers = %v, want the steer taken back", drained)
	}
	released := h.changedChan()
	gate.open()
	waitClosed(t, released)

	ctx, cancel := context.WithTimeout(context.Background(), driverTestTimeout)
	defer cancel()
	if err := h.d.WaitIdle(ctx); err != nil {
		t.Fatalf("WaitIdle: %v", err)
	}
	h.noRun()
	h.mu.Lock()
	saves := len(h.saves)
	h.mu.Unlock()
	if saves != 1 {
		t.Fatalf("saves = %d, want the one settle save an emptied inbox records", saves)
	}
	if conv := h.d.Snapshot().Conversation; len(conv) != 0 {
		t.Fatalf("conversation = %+v, want nothing delivered", conv)
	}
}

func TestConversationDriverSubmitDuringPrepareTurnStartsRun(t *testing.T) {
	t.Parallel()
	gate := newGatedPrepare(0)
	h := newDriverHarness(t, nil, gate.prepare)
	h.bg.setPending("a")
	h.bg.markFinished("a")
	h.start()
	t.Cleanup(gate.open)

	h.d.DeliverCompletions([]SubAgentCompletion{completionFor(1, "a")})
	gate.waitEntered(t)
	// A prompt lifts the hold and must start the approved run.
	h.d.Submit("now", nil, SubmitMeta{})
	gate.open()

	call := h.nextRun()
	if msg := lastContent(call); !strings.Contains(msg, `agent_id="a"`) || !strings.HasSuffix(msg, "now") {
		t.Fatalf("message = %q, want the completion envelope with the prompt", msg)
	}
	call.finish()
	h.waitQuiescent()
}

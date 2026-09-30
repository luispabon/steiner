package agent

import (
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeTimer struct {
	clock   *fakeClock
	f       func()
	stopped bool
}

func (t *fakeTimer) Stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	was := !t.stopped
	t.stopped = true
	return was
}

// fakeClock records timers and fires them only when the test says so.
type fakeClock struct {
	mu     sync.Mutex
	timers []*fakeTimer
	delays []time.Duration
}

func (c *fakeClock) Now() time.Time { return time.Unix(0, 0) }

func (c *fakeClock) AfterFunc(d time.Duration, f func()) Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &fakeTimer{clock: c, f: f}
	c.timers = append(c.timers, t)
	c.delays = append(c.delays, d)
	return t
}

// armed counts timers that were not stopped or fired.
func (c *fakeClock) armed() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, t := range c.timers {
		if !t.stopped {
			n++
		}
	}
	return n
}

func (c *fakeClock) callbacks(includeStopped bool) []func() {
	c.mu.Lock()
	defer c.mu.Unlock()
	var fs []func()
	for _, t := range c.timers {
		if includeStopped || !t.stopped {
			fs = append(fs, t.f)
		}
		t.stopped = true
	}
	return fs
}

// fire runs every live timer once.
func (c *fakeClock) fire() {
	for _, f := range c.callbacks(false) {
		f()
	}
}

// fireStopped runs every timer, including stopped ones a real clock would
// have dropped, to model a callback already in flight when Stop was called.
func (c *fakeClock) fireStopped() {
	for _, f := range c.callbacks(true) {
		f()
	}
}

func quietCompletionFor(seq uint64, id string) SubAgentCompletion {
	c := completionFor(seq, id)
	c.Status = "cancelled"
	c.Quiet = true
	return c
}

func TestConversationDriverQuietCompletionWhileWaitingSettlesIdle(t *testing.T) {
	t.Parallel()
	h := newDriverHarness(t, nil, nil)
	h.bg.setPending("a")
	h.start()

	h.d.Submit("go", nil, SubmitMeta{})
	h.nextRun().finish()
	h.waitSettled(DriverWaiting, false)

	h.d.DeliverCompletions([]SubAgentCompletion{quietCompletionFor(1, "a")})
	h.waitSettled(DriverIdle, false)
	h.noRun()
	if got := h.clock.armed(); got != 0 {
		t.Fatalf("armed timers = %d, want 0 for a quiet completion", got)
	}
	if got := h.bg.deliveredIDs(); !slices.Equal(got, []string{"call-a"}) {
		t.Fatalf("delivered = %v, want [call-a]", got)
	}
	conv := h.d.Snapshot().Conversation
	last := conv[len(conv)-1]
	if last.Source != MessageSourceSubAgentResult || !strings.Contains(last.Content, `agent_id="a"`) {
		t.Fatalf("last message = %+v, want recorded envelope for a", last)
	}
}

func TestConversationDriverQuietCompletionWhileGeneratingDoesNotContinue(t *testing.T) {
	t.Parallel()
	h := newDriverHarness(t, nil, nil)
	h.bg.setPending("a")
	h.start()

	h.d.Submit("go", nil, SubmitMeta{})
	call := h.nextRun()
	h.d.DeliverCompletions([]SubAgentCompletion{quietCompletionFor(1, "a")})

	drain := call.in.DrainInbox()
	if drain.Message == nil || drain.Wake {
		t.Fatalf("drain = %+v, want a delivered message without wake", drain)
	}
	conv := append(slices.Clone(call.in.Conversation), *drain.Message, Message{Role: MessageRoleAssistant, Content: "ok"})
	call.release <- driverTestResult{out: DriverRunOutput{Conversation: conv}}
	h.waitQuiescent()
	h.noRun()
}

func TestConversationDriverCoalescingWindow(t *testing.T) {
	t.Parallel()
	h := newDriverHarness(t, nil, nil)
	h.bg.setPending("a", "b", "c")
	h.start()

	h.d.Submit("go", nil, SubmitMeta{})
	h.nextRun().finish()
	h.waitSettled(DriverWaiting, false)

	h.bg.markFinished("a")
	h.d.DeliverCompletions([]SubAgentCompletion{completionFor(1, "a")})
	if got := h.clock.armed(); got != 1 {
		t.Fatalf("armed timers = %d, want 1", got)
	}
	h.noRun()
	h.bg.markFinished("b")
	h.d.DeliverCompletions([]SubAgentCompletion{completionFor(2, "b")})
	if got := h.clock.armed(); got != 1 {
		t.Fatalf("armed timers = %d, want the shared window while c runs", got)
	}
	if got := h.clock.delays; len(got) != 1 || got[0] != completionCoalesceWindow {
		t.Fatalf("window delays = %v, want [%v]", got, completionCoalesceWindow)
	}
	h.noRun()
	h.bg.markFinished("c")
	h.d.DeliverCompletions([]SubAgentCompletion{quietCompletionFor(3, "c")})
	if got := h.clock.armed(); got != 0 {
		t.Fatalf("armed timers = %d, want the window cancelled", got)
	}
	call := h.nextRun()
	msg := lastContent(call)
	if !strings.Contains(msg, `agent_id="a"`) || !strings.Contains(msg, `agent_id="b"`) || !strings.Contains(msg, `agent_id="c"`) {
		t.Fatalf("message = %q, want all envelopes in one run", msg)
	}
	call.finish()
	h.waitQuiescent()
	h.noRun()
}

func TestConversationDriverLoneCompletionStartsWithoutWindow(t *testing.T) {
	t.Parallel()
	h := newDriverHarness(t, nil, nil)
	h.bg.setPending("a")
	h.bg.markFinished("a")
	h.start()

	h.d.DeliverCompletions([]SubAgentCompletion{completionFor(1, "a")})
	if got := h.clock.armed(); got != 0 {
		t.Fatalf("armed timers = %d, want none without unfinished siblings", got)
	}
	call := h.nextRun()
	if !strings.Contains(lastContent(call), `agent_id="a"`) {
		t.Fatalf("message = %q, want envelope for a", lastContent(call))
	}
	call.finish()
	h.waitQuiescent()
}

func TestConversationDriverQueuedSiblingKeepsSharedWindow(t *testing.T) {
	t.Parallel()
	h := newDriverHarness(t, nil, nil)
	h.bg.setPending("a", "b")
	h.bg.markFinished("a")
	h.bg.mu.Lock()
	h.bg.pending[1].State = SubAgentQueued
	h.bg.mu.Unlock()
	h.start()

	h.d.DeliverCompletions([]SubAgentCompletion{completionFor(1, "a")})
	if got := h.clock.armed(); got != 1 {
		t.Fatalf("armed timers = %d, want 1 for queued sibling", got)
	}
	h.bg.markFinished("b")
	h.d.DeliverCompletions([]SubAgentCompletion{quietCompletionFor(2, "b")})
	if got := h.clock.armed(); got != 0 {
		t.Fatalf("armed timers = %d, want cancelled after last sibling", got)
	}
	call := h.nextRun()
	if !strings.Contains(lastContent(call), `agent_id="a"`) || !strings.Contains(lastContent(call), `agent_id="b"`) {
		t.Fatalf("message = %q, want both envelopes", lastContent(call))
	}
	call.finish()
	h.waitQuiescent()
}

func TestConversationDriverNilBackgroundCompletionStartsImmediately(t *testing.T) {
	t.Parallel()
	h := newDriverHarnessOpts(t, nil, nil, func(opts *DriverOptions) { opts.Background = nil })
	h.start()

	h.d.DeliverCompletions([]SubAgentCompletion{completionFor(1, "a")})
	if got := h.clock.armed(); got != 0 {
		t.Fatalf("armed timers = %d, want none without background", got)
	}
	call := h.nextRun()
	call.finish()
	h.waitQuiescent()
}

func TestConversationDriverStaleWindowCallbackAfterLastSiblingIsHarmless(t *testing.T) {
	t.Parallel()
	h := newDriverHarness(t, nil, nil)
	h.bg.setPending("a", "b")
	h.bg.markFinished("a")

	h.d.DeliverCompletions([]SubAgentCompletion{completionFor(1, "a")})
	if got := h.clock.armed(); got != 1 {
		t.Fatalf("armed timers = %d, want 1", got)
	}
	h.d.mu.Lock()
	armedGen := h.d.windowGen
	h.d.mu.Unlock()
	h.bg.markFinished("b")
	h.d.DeliverCompletions([]SubAgentCompletion{quietCompletionFor(2, "b")})
	if got := h.clock.armed(); got != 0 {
		t.Fatalf("armed timers = %d, want cancelled after last sibling", got)
	}
	h.d.mu.Lock()
	gen := h.d.windowGen
	ready := h.d.windowExpired
	h.d.mu.Unlock()
	if gen == armedGen {
		t.Fatalf("window generation = %d, want invalidation after early release", gen)
	}
	if !ready {
		t.Fatal("last completion did not make buffered wake ready")
	}

	// Fire the stopped callback before any drain can invalidate it again.
	h.clock.fireStopped()
	h.d.mu.Lock()
	ready = h.d.windowExpired
	h.d.mu.Unlock()
	if !ready {
		t.Fatal("stale callback cleared readiness after the last sibling")
	}
}

func TestConversationDriverSubmitDuringWindowStartsAtOnce(t *testing.T) {
	t.Parallel()
	h := newDriverHarness(t, nil, nil)
	h.bg.setPending("a")
	h.start()

	h.d.DeliverCompletions([]SubAgentCompletion{completionFor(1, "a")})
	if got := h.clock.armed(); got != 1 {
		t.Fatalf("armed timers = %d, want 1", got)
	}
	h.d.Submit("now", nil, SubmitMeta{})
	call := h.nextRun()
	msg := lastContent(call)
	if !strings.Contains(msg, `agent_id="a"`) || !strings.HasSuffix(msg, "now") {
		t.Fatalf("message = %q, want envelope with prompt", msg)
	}
	if got := h.clock.armed(); got != 0 {
		t.Fatalf("armed timers = %d, want the window cancelled", got)
	}

	h.clock.fireStopped()
	h.d.mu.Lock()
	expired := h.d.windowExpired
	h.d.mu.Unlock()
	if expired {
		t.Fatal("stale window callback flagged an expiry")
	}
	call.finish()
	h.waitQuiescent()
	h.noRun()
}

func TestConversationDriverSteerDuringWindowStartsAtOnce(t *testing.T) {
	t.Parallel()
	h := newDriverHarness(t, nil, nil)
	h.bg.setPending("a")
	h.start()

	h.d.DeliverCompletions([]SubAgentCompletion{completionFor(1, "a")})
	h.steers.Add(SteerMessage{Text: "steer"})
	h.d.NotifySteer()
	call := h.nextRun()
	if msg := lastContent(call); !strings.Contains(msg, `agent_id="a"`) || !strings.HasSuffix(msg, "steer") {
		t.Fatalf("message = %q, want envelope with steer", msg)
	}
	call.finish()
	h.waitQuiescent()
}

func TestConversationDriverNoWindowWhileGenerating(t *testing.T) {
	t.Parallel()
	h := newDriverHarness(t, nil, nil)
	h.bg.setPending("a")
	h.start()

	h.d.Submit("go", nil, SubmitMeta{})
	call := h.nextRun()
	h.d.DeliverCompletions([]SubAgentCompletion{completionFor(1, "a")})
	if got := h.clock.armed(); got != 0 {
		t.Fatalf("armed timers = %d, want none while generating", got)
	}
	if drain := call.in.DrainInbox(); !drain.Wake {
		t.Fatalf("drain = %+v, want wake", drain)
	}
	call.finish()
	h.waitQuiescent()
}

func TestConversationDriverCompletionAfterLastBoundaryContinuesWithoutWindow(t *testing.T) {
	t.Parallel()
	h := newDriverHarness(t, nil, nil)
	h.bg.setPending("a")
	h.start()

	h.d.Submit("go", nil, SubmitMeta{})
	call := h.nextRun()
	h.d.DeliverCompletions([]SubAgentCompletion{completionFor(1, "a")})
	call.finish()
	next := h.nextRun()
	if msg := lastContent(next); !strings.Contains(msg, `agent_id="a"`) {
		t.Fatalf("message = %q, want envelope for a", msg)
	}
	next.finish()
	h.waitQuiescent()
}

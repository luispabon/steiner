package agent

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/output"
)

const driverTestTimeout = 5 * time.Second

type fakeBackground struct {
	mu        sync.Mutex
	pending   []PendingSubAgent
	delivered []string
	sealed    []string
}

func (f *fakeBackground) Pending() []PendingSubAgent {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.pending)
}

func (f *fakeBackground) HasPending() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.pending) > 0
}

// MarkDelivered drops the pending agent whose call id is "call-"+AgentID.
func (f *fakeBackground) MarkDelivered(ids []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.delivered = append(f.delivered, ids...)
	f.pending = slices.DeleteFunc(f.pending, func(p PendingSubAgent) bool {
		return slices.Contains(ids, "call-"+p.AgentID)
	})
}

func (f *fakeBackground) Ledger() []SubAgentLedgerEntry {
	f.mu.Lock()
	defer f.mu.Unlock()
	entries := make([]SubAgentLedgerEntry, len(f.pending))
	for i, p := range f.pending {
		entries[i] = SubAgentLedgerEntry{AgentID: p.AgentID, ParentCallID: "call-" + p.AgentID}
	}
	return entries
}

func (f *fakeBackground) SealBatch(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sealed = append(f.sealed, id)
}

func (f *fakeBackground) deliveredIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.delivered)
}

func (f *fakeBackground) setPending(ids ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pending = nil
	for _, id := range ids {
		f.pending = append(f.pending, PendingSubAgent{AgentID: id, AgentType: "code", State: SubAgentRunning})
	}
}

func (f *fakeBackground) markFinished(ids ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.pending {
		if slices.Contains(ids, f.pending[i].AgentID) {
			f.pending[i].State = SubAgentFinished
		}
	}
}

func completionFor(seq uint64, id string) SubAgentCompletion {
	return SubAgentCompletion{
		Seq: seq, ParentCallID: "call-" + id, AgentID: id, AgentType: "code",
		Status: "complete", ObjectivePreview: "obj " + id, Body: `{"output":"done ` + id + `"}`,
	}
}

// runCall is one Run invocation the test controls: it returns when release
// receives a result or the run context ends.
type runCall struct {
	ctx     context.Context
	in      DriverRunInput
	release chan driverTestResult
}

type driverTestResult struct {
	out DriverRunOutput
	err error
}

type driverHarness struct {
	t      *testing.T
	d      *ConversationDriver
	bg     *fakeBackground
	steers *SteerQueue
	calls  chan *runCall
	events chan output.Event
	clock  *fakeClock

	mu    sync.Mutex
	saves []DriverSnapshot
	saveE error
}

func newDriverHarness(t *testing.T, conv []Message, prepare func(context.Context, []Message) DeliveryParts) *driverHarness {
	t.Helper()
	return newDriverHarnessOpts(t, conv, prepare, nil)
}

func newDriverHarnessOpts(t *testing.T, conv []Message, prepare func(context.Context, []Message) DeliveryParts, mutate func(*DriverOptions)) *driverHarness {
	t.Helper()
	h := &driverHarness{
		clock:  &fakeClock{},
		t:      t,
		bg:     &fakeBackground{},
		steers: NewSteerQueue(),
		calls:  make(chan *runCall, 16),
		events: make(chan output.Event, 1024),
	}
	opts := DriverOptions{
		Clock:       h.clock,
		Run:         h.run,
		Background:  h.bg,
		Steers:      h.steers,
		Save:        h.save,
		Events:      output.SinkFunc(func(e output.Event) { h.events <- e }),
		PrepareTurn: prepare,
	}
	if mutate != nil {
		mutate(&opts)
	}
	h.d = NewConversationDriver(opts, conv, ConversationLineage{})
	return h
}

func (h *driverHarness) start() {
	h.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	h.d.Start(ctx)
	h.t.Cleanup(func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), driverTestTimeout)
		defer closeCancel()
		h.d.Close(closeCtx)
		cancel()
	})
}

func (h *driverHarness) run(ctx context.Context, in DriverRunInput) (DriverRunOutput, error) {
	call := &runCall{ctx: ctx, in: in, release: make(chan driverTestResult, 1)}
	h.calls <- call
	select {
	case res := <-call.release:
		return res.out, res.err
	case <-ctx.Done():
		return DriverRunOutput{Conversation: in.Conversation}, ctx.Err()
	}
}

func (h *driverHarness) save(_ context.Context, snap DriverSnapshot) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.saves = append(h.saves, snap)
	return h.saveE
}

// closeDriver joins the loop goroutine, so every save and event it produced
// has landed.
func (h *driverHarness) closeDriver() {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), driverTestTimeout)
	defer cancel()
	h.d.Close(ctx)
}

// saveLens closes the driver first so the loop's last save has landed; the
// result therefore ends with Close's own settle save.
func (h *driverHarness) saveLens() []int {
	h.t.Helper()
	h.closeDriver()
	h.mu.Lock()
	defer h.mu.Unlock()
	lens := make([]int, len(h.saves))
	for i, s := range h.saves {
		lens[i] = len(s.Conversation)
	}
	return lens
}

func (h *driverHarness) nextRun() *runCall {
	h.t.Helper()
	select {
	case c := <-h.calls:
		return c
	case <-time.After(driverTestTimeout):
		h.t.Fatal("timed out waiting for Run")
		return nil
	}
}

func (h *driverHarness) noRun() {
	h.t.Helper()
	select {
	case <-h.calls:
		h.t.Fatal("Run was called unexpectedly")
	default:
	}
}

// waitState blocks until a conversation_state event matches.
func (h *driverHarness) waitState(state DriverState, held bool) {
	h.t.Helper()
	timeout := time.After(driverTestTimeout)
	for {
		select {
		case e := <-h.events:
			p, ok := e.Payload.(output.ConversationStateEvent)
			if ok && p.State == string(state) && p.Held == held {
				return
			}
		case <-timeout:
			h.t.Fatalf("timed out waiting for state %s held=%v", state, held)
		}
	}
}

// waitSettled blocks until State reports the given state and held flag.
func (h *driverHarness) waitSettled(state DriverState, held bool) {
	h.t.Helper()
	timeout := time.After(driverTestTimeout)
	for {
		h.d.mu.Lock()
		ok := h.d.state == state && h.d.held == held
		ch := h.d.changed
		h.d.mu.Unlock()
		if ok {
			return
		}
		select {
		case <-ch:
		case <-timeout:
			h.t.Fatalf("timed out waiting for state %s held=%v", state, held)
		}
	}
}

func (h *driverHarness) waitQuiescent() {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), driverTestTimeout)
	defer cancel()
	if err := h.d.WaitQuiescent(ctx); err != nil {
		h.t.Fatalf("WaitQuiescent: %v", err)
	}
}

// finish releases a run with its input conversation plus one assistant reply.
func (c *runCall) finish() {
	conv := append(slices.Clone(c.in.Conversation), Message{Role: MessageRoleAssistant, Content: "ok"})
	c.release <- driverTestResult{out: DriverRunOutput{Conversation: conv}}
}

func lastContent(c *runCall) string {
	return c.in.Conversation[len(c.in.Conversation)-1].Content
}

func TestConversationDriverIdleSubmitGeneratingIdle(t *testing.T) {
	t.Parallel()
	h := newDriverHarness(t, []Message{{Role: MessageRoleUser, Content: "earlier"}}, nil)
	h.start()

	h.d.Submit("hello", nil, SubmitMeta{})
	call := h.nextRun()
	if got := lastContent(call); got != "hello" {
		t.Fatalf("first message = %q, want hello", got)
	}
	if state, held := h.d.State(); state != DriverGenerating || held {
		t.Fatalf("State = %s held=%v, want generating", state, held)
	}
	call.finish()
	h.waitQuiescent()

	snap := h.d.Snapshot()
	if len(snap.Conversation) != 3 || snap.Conversation[2].Content != "ok" {
		t.Fatalf("conversation = %+v, want earlier, hello, ok", snap.Conversation)
	}
	if got, want := h.saveLens(), []int{2, 3}; len(got) < len(want) || !slices.Equal(got[:len(want)], want) {
		t.Fatalf("save conversation lengths = %v, want %v (first-message append, then adoption)", got, want)
	}
}

func TestConversationDriverPassesBackgroundHooks(t *testing.T) {
	t.Parallel()
	h := newDriverHarness(t, nil, nil)
	h.bg.setPending("a")
	h.start()

	h.d.Submit("go", nil, SubmitMeta{})
	call := h.nextRun()
	call.in.OnToolBatchDone("batch-1")
	if got := h.bg.sealed; !slices.Equal(got, []string{"batch-1"}) {
		t.Fatalf("sealed = %v, want [batch-1]", got)
	}
	if got := call.in.PendingSubAgents(); len(got) != 1 || got[0].AgentID != "a" {
		t.Fatalf("PendingSubAgents = %v, want agent a", got)
	}
	call.finish()
}

func TestConversationDriverCompletionWhileIdleStartsRun(t *testing.T) {
	t.Parallel()
	h := newDriverHarness(t, nil, nil)
	h.bg.setPending("a")
	h.start()

	h.d.DeliverCompletions([]SubAgentCompletion{completionFor(1, "a")})
	h.clock.fire()
	call := h.nextRun()
	msg := call.in.Conversation[len(call.in.Conversation)-1]
	if msg.Source != MessageSourceSubAgentResult || !strings.Contains(msg.Content, `agent_id="a"`) {
		t.Fatalf("first message = %+v, want sub-agent result envelope for a", msg)
	}
	if got := h.bg.deliveredIDs(); !slices.Equal(got, []string{"call-a"}) {
		t.Fatalf("delivered = %v, want [call-a]", got)
	}
	call.finish()
	h.waitQuiescent()
}

func TestConversationDriverCompletionWhileGeneratingDeliveredAtBoundary(t *testing.T) {
	t.Parallel()
	h := newDriverHarness(t, nil, nil)
	h.bg.setPending("a")
	h.start()

	h.d.Submit("go", nil, SubmitMeta{})
	call := h.nextRun()
	h.d.DeliverCompletions([]SubAgentCompletion{completionFor(1, "a")})

	drain := call.in.DrainInbox()
	if !drain.Wake || drain.Message == nil || !strings.Contains(drain.Message.Content, `agent_id="a"`) {
		t.Fatalf("drain = %+v, want wake with envelope for a", drain)
	}
	if drain.UserText != "" {
		t.Fatalf("UserText = %q, want empty for completion-only delivery", drain.UserText)
	}
	if again := call.in.DrainInbox(); again.Message != nil {
		t.Fatalf("second drain = %+v, want empty", again)
	}
	conv := append(slices.Clone(call.in.Conversation), *drain.Message, Message{Role: MessageRoleAssistant, Content: "ok"})
	call.release <- driverTestResult{out: DriverRunOutput{Conversation: conv}}
	h.waitQuiescent()
	h.noRun()
}

func TestConversationDriverSteerAfterFinalBoundaryStartsNewSequence(t *testing.T) {
	t.Parallel()
	h := newDriverHarness(t, nil, nil)
	h.start()

	h.d.Submit("first", nil, SubmitMeta{})
	call := h.nextRun()
	h.steers.Add(SteerMessage{Text: "late steer"})
	h.d.NotifySteer()
	call.finish()

	next := h.nextRun()
	if got := lastContent(next); got != "late steer" {
		t.Fatalf("second sequence first message = %q, want late steer", got)
	}
	if len(next.in.Conversation) != 3 {
		t.Fatalf("second sequence conversation = %+v, want first, ok, late steer", next.in.Conversation)
	}
	next.finish()
	h.waitQuiescent()
}

func TestConversationDriverStopTurnHoldsAndSubmitReleases(t *testing.T) {
	t.Parallel()
	h := newDriverHarness(t, nil, nil)
	h.bg.setPending("a")
	h.start()

	h.d.Submit("go", nil, SubmitMeta{})
	call := h.nextRun()
	h.d.StopTurn()
	h.waitState(DriverWaiting, true)
	if call.ctx.Err() == nil {
		t.Fatal("run context still live after StopTurn")
	}

	h.d.DeliverCompletions([]SubAgentCompletion{completionFor(1, "a")})
	h.noRun()
	if state, held := h.d.State(); state != DriverWaiting || !held {
		t.Fatalf("State = %s held=%v, want waiting held", state, held)
	}

	h.d.Submit("resume", nil, SubmitMeta{})
	next := h.nextRun()
	msg := lastContent(next)
	if !strings.Contains(msg, `agent_id="a"`) || !strings.HasSuffix(msg, "resume") {
		t.Fatalf("released message = %q, want held envelope with prompt", msg)
	}
	if _, held := h.d.State(); held {
		t.Fatal("hold not lifted by Submit")
	}
	next.finish()
	h.waitQuiescent()
}

func TestConversationDriverStaleEpochStopIgnored(t *testing.T) {
	t.Parallel()
	h := newDriverHarness(t, nil, nil)
	h.bg.setPending("a")
	h.start()

	h.d.Submit("one", nil, SubmitMeta{})
	first := h.nextRun()
	h.d.mu.Lock()
	staleEpoch := h.d.epoch
	h.d.mu.Unlock()
	first.finish()
	h.waitSettled(DriverWaiting, false)

	h.d.Submit("two", nil, SubmitMeta{})
	second := h.nextRun()
	h.d.stopEpoch(staleEpoch)
	if err := second.ctx.Err(); err != nil {
		t.Fatalf("second run cancelled by stale stop: %v", err)
	}
	if _, held := h.d.State(); held {
		t.Fatal("stale stop set held")
	}

	h.d.mu.Lock()
	current := h.d.epoch
	h.d.mu.Unlock()
	h.d.stopEpoch(current)
	if _, held := h.d.State(); !held {
		t.Fatal("current-epoch stop did not hold")
	}
	<-second.ctx.Done()
	h.waitSettled(DriverWaiting, true)
}

func TestConversationDriverCompactionDeferredWhileGenerating(t *testing.T) {
	t.Parallel()
	h := newDriverHarness(t, nil, nil)
	h.bg.setPending("a")
	h.start()

	h.d.Submit("go", nil, SubmitMeta{})
	call := h.nextRun()

	compacted := make(chan []Message, 1)
	h.d.RequestCompaction(func(_ context.Context, conv []Message) ([]Message, ConversationLineage, error) {
		compacted <- conv
		summary := []Message{{Role: MessageRoleUser, Content: "summary"}}
		return summary, newConversationLineage(summary), nil
	})
	select {
	case <-compacted:
		t.Fatal("compaction ran while generating")
	default:
	}

	call.finish()
	select {
	case conv := <-compacted:
		if len(conv) != 2 {
			t.Fatalf("compaction input = %+v, want adopted run conversation", conv)
		}
	case <-time.After(driverTestTimeout):
		t.Fatal("compaction never ran")
	}
	h.waitState(DriverWaiting, false)

	deadline := time.After(driverTestTimeout)
	for {
		snap := h.d.Snapshot()
		if len(snap.Conversation) == 2 && snap.Conversation[0].Content == "summary" {
			if got := snap.Conversation[1]; got.Source != MessageSourceSubAgentResult || !strings.Contains(got.Content, "<steiner-sub-agents-pending>") {
				t.Fatalf("post-compaction message = %+v, want pending-only message", got)
			}
			if snap.Conversation[0].Content != "summary" {
				t.Fatalf("conversation = %+v, want summary first", snap.Conversation)
			}
			lineage := snap.Lineage.FullMessages()
			if len(lineage) != 2 || lineage[1].Content != snap.Conversation[1].Content {
				t.Fatalf("snapshot lineage = %+v, want summary plus pending message", lineage)
			}
			reloaded := NewConversationDriver(DriverOptions{}, snap.Conversation, snap.Lineage).Snapshot()
			if got := reloaded.Lineage.FullMessages(); len(got) != 2 || got[1].Content != snap.Conversation[1].Content {
				t.Fatalf("reloaded lineage = %+v, want summary plus pending message", got)
			}
			return
		}
		select {
		case <-deadline:
			t.Fatalf("conversation = %+v, want summary plus pending message", snap.Conversation)
		default:
		}
		h.waitQuiescentOrPending()
	}
}

// waitQuiescentOrPending yields until the driver state changes; the harness
// background always has a pending agent here so it waits for a change signal.
func (h *driverHarness) waitQuiescentOrPending() {
	h.d.mu.Lock()
	ch := h.d.changed
	h.d.mu.Unlock()
	select {
	case <-ch:
	case <-time.After(driverTestTimeout):
	}
}

func TestConversationDriverCompactionCompletionsWait(t *testing.T) {
	t.Parallel()
	h := newDriverHarness(t, []Message{{Role: MessageRoleUser, Content: "old"}}, nil)
	h.bg.setPending("a")
	h.start()

	inCompaction := make(chan struct{})
	finishCompaction := make(chan struct{})
	h.d.RequestCompaction(func(_ context.Context, _ []Message) ([]Message, ConversationLineage, error) {
		close(inCompaction)
		<-finishCompaction
		return []Message{{Role: MessageRoleUser, Content: "summary"}}, ConversationLineage{}, nil
	})
	<-inCompaction
	h.d.DeliverCompletions([]SubAgentCompletion{completionFor(1, "a")})
	h.noRun()
	h.clock.fire()
	h.noRun()
	close(finishCompaction)

	call := h.nextRun()
	conv := call.in.Conversation
	if len(conv) != 3 || conv[0].Content != "summary" || !strings.Contains(conv[2].Content, `agent_id="a"`) {
		t.Fatalf("conversation = %+v, want summary, pending line, envelope", conv)
	}
	call.finish()
	h.waitQuiescent()
}

func TestConversationDriverWaitQuiescent(t *testing.T) {
	t.Parallel()
	h := newDriverHarness(t, nil, nil)
	h.bg.setPending("a")
	h.start()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := h.d.WaitQuiescent(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("WaitQuiescent with pending agent = %v, want context.Canceled", err)
	}

	h.d.DeliverCompletions([]SubAgentCompletion{completionFor(1, "a")})
	h.clock.fire()
	done := make(chan error, 1)
	go func() { done <- h.d.WaitQuiescent(context.Background()) }()
	call := h.nextRun()
	select {
	case err := <-done:
		t.Fatalf("WaitQuiescent returned %v while generating", err)
	default:
	}
	call.finish()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("WaitQuiescent = %v, want nil", err)
		}
	case <-time.After(driverTestTimeout):
		t.Fatal("WaitQuiescent did not return")
	}
}

func TestConversationDriverCloseSettlesBufferedItemsWithoutRun(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		inRun   bool
		wantLen int
	}{
		{"never started", false, 1},
		{"buffered during a run", true, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := newDriverHarness(t, nil, nil)
			h.bg.setPending("a", "b", "c")
			var call *runCall
			if tt.inRun {
				h.start()
				h.d.Submit("go", nil, SubmitMeta{})
				call = h.nextRun()
			}
			h.d.DeliverCompletions([]SubAgentCompletion{completionFor(2, "b"), completionFor(1, "a")})
			h.d.Submit("last words", nil, SubmitMeta{})

			ctx, cancel := context.WithTimeout(context.Background(), driverTestTimeout)
			defer cancel()
			h.d.Close(ctx)
			h.d.Close(ctx)

			if call != nil && call.ctx.Err() == nil {
				t.Fatal("run context not cancelled by Close")
			}
			h.noRun()
			snap := h.d.Snapshot()
			if len(snap.Conversation) != tt.wantLen {
				t.Fatalf("conversation = %+v, want %d messages", snap.Conversation, tt.wantLen)
			}
			settled := snap.Conversation[len(snap.Conversation)-1]
			content := settled.Content
			ia, ib := strings.Index(content, `agent_id="a"`), strings.Index(content, `agent_id="b"`)
			if ia < 0 || ib < ia || !strings.HasSuffix(content, "last words") {
				t.Fatalf("settled message = %q, want envelopes a, b then user text", content)
			}
			if !strings.Contains(content, "c (code") {
				t.Fatalf("settled message = %q, want pending line for c", content)
			}
			got := h.bg.deliveredIDs()
			slices.Sort(got)
			if !slices.Equal(got, []string{"call-a", "call-b"}) {
				t.Fatalf("delivered = %v, want call-a and call-b", got)
			}
			lens := h.saveLens()
			if len(lens) == 0 || lens[len(lens)-1] != tt.wantLen {
				t.Fatalf("save lengths = %v, want last save to include settled message", lens)
			}
			h.d.Submit("ignored", nil, SubmitMeta{})
			if got := len(h.d.Snapshot().Conversation); got != tt.wantLen {
				t.Fatalf("Submit after Close changed conversation to %d messages", got)
			}
		})
	}
}

func TestConversationDriverCloseCancelsRunAndAdopts(t *testing.T) {
	t.Parallel()
	h := newDriverHarness(t, nil, nil)
	h.bg.setPending("a")
	h.start()

	h.d.Submit("go", nil, SubmitMeta{})
	call := h.nextRun()
	h.d.DeliverCompletions([]SubAgentCompletion{completionFor(1, "a")})

	ctx, cancel := context.WithTimeout(context.Background(), driverTestTimeout)
	defer cancel()
	h.d.Close(ctx)

	if call.ctx.Err() == nil {
		t.Fatal("run context not cancelled by Close")
	}
	h.noRun()
	snap := h.d.Snapshot()
	if len(snap.Conversation) != 2 || !strings.Contains(snap.Conversation[1].Content, `agent_id="a"`) {
		t.Fatalf("conversation = %+v, want go plus settled envelope", snap.Conversation)
	}
}

func TestConversationDriverSaveSequence(t *testing.T) {
	t.Parallel()
	h := newDriverHarness(t, nil, nil)
	h.start()

	h.d.Submit("one", nil, SubmitMeta{})
	first := h.nextRun()
	h.steers.Add(SteerMessage{Text: "two"})
	h.d.NotifySteer()
	first.finish()
	second := h.nextRun()
	second.finish()
	h.waitQuiescent()

	// first message; adoption of run 1; second sequence's first message; adoption of run 2.
	if got, want := h.saveLens(), []int{1, 2, 3, 4}; len(got) < len(want) || !slices.Equal(got[:len(want)], want) {
		t.Fatalf("save conversation lengths = %v, want %v", got, want)
	}
}

func TestConversationDriverSaveErrorDoesNotStopLoop(t *testing.T) {
	t.Parallel()
	h := newDriverHarness(t, nil, nil)
	h.saveE = errors.New("disk full")
	h.start()

	h.d.Submit("one", nil, SubmitMeta{})
	h.nextRun().finish()
	h.d.Submit("two", nil, SubmitMeta{})
	h.nextRun().finish()
	h.waitQuiescent()

	h.closeDriver()
	warnings := 0
	for len(h.events) > 0 {
		if e := <-h.events; e.Type == output.EventTypeConversationWarning {
			warnings++
		}
	}
	if warnings == 0 {
		t.Fatal("no conversation warning emitted for failed save")
	}
}

func TestConversationDriverRunOutcomes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		result    func(in DriverRunInput) driverTestResult
		wantLen   int
		wantWarns bool
	}{
		{
			name: "error keeps returned conversation and warns",
			result: func(in DriverRunInput) driverTestResult {
				return driverTestResult{out: DriverRunOutput{Conversation: append(slices.Clone(in.Conversation), Message{Role: MessageRoleAssistant, Content: "partial"})}, err: errors.New("boom")}
			},
			wantLen:   2,
			wantWarns: true,
		},
		{
			name: "skip adoption keeps driver conversation",
			result: func(_ DriverRunInput) driverTestResult {
				return driverTestResult{out: DriverRunOutput{Conversation: []Message{{Content: "other"}}, SkipAdoption: true}}
			},
			wantLen: 1,
		},
		{
			name: "cancellation is not a warning",
			result: func(_ DriverRunInput) driverTestResult {
				return driverTestResult{err: context.Canceled}
			},
			wantLen: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := newDriverHarness(t, nil, nil)
			h.start()
			h.d.Submit("go", nil, SubmitMeta{})
			call := h.nextRun()
			call.release <- tt.result(call.in)
			h.waitQuiescent()
			gotLen := len(h.d.Snapshot().Conversation)

			h.d.Submit("again", nil, SubmitMeta{})
			h.nextRun().finish()
			h.waitQuiescent()

			if gotLen != tt.wantLen {
				t.Fatalf("conversation length = %d, want %d", gotLen, tt.wantLen)
			}
			h.closeDriver()
			warned := false
			for len(h.events) > 0 {
				if e := <-h.events; e.Type == output.EventTypeConversationWarning {
					warned = true
				}
			}
			if warned != tt.wantWarns {
				t.Fatalf("warning emitted = %v, want %v", warned, tt.wantWarns)
			}
		})
	}
}

func TestConversationDriverPrepareTurnPrefixesSequence(t *testing.T) {
	t.Parallel()
	prepare := func(context.Context, []Message) DeliveryParts {
		return DeliveryParts{ModeNotice: "<mode>plan</mode>\n\n", SkillBlocks: []string{"<skill>x</skill>"}}
	}
	h := newDriverHarness(t, nil, prepare)
	h.start()
	h.d.Submit("hi", nil, SubmitMeta{})
	call := h.nextRun()
	want := "<mode>plan</mode>\n\n<skill>x</skill>\n\nhi"
	if got := lastContent(call); got != want {
		t.Fatalf("first message = %q, want %q", got, want)
	}
	call.finish()
	h.waitQuiescent()
}

func TestConversationDriverCloseWithoutStartAndRepeated(t *testing.T) {
	t.Parallel()
	h := newDriverHarness(t, nil, nil)
	var wg sync.WaitGroup
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h.d.Close(context.Background())
		}()
	}
	wg.Wait()
	h.d.Start(context.Background())
	h.noRun()
}

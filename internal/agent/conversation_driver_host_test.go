package agent

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/output"
)

func TestConversationDriverSubmitSkillBlocks(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		prepare   []string
		first     []string
		queued    [][]string
		wantFirst string
		wantDrain string
	}{
		{name: "meta blocks precede text", first: []string{"<a/>"}, wantFirst: "<a/>\n\nfirst"},
		{name: "meta blocks replace prepared blocks", prepare: []string{"<p/>"}, first: []string{"<a/>"}, wantFirst: "<a/>\n\nfirst"},
		{name: "prepared blocks apply without meta", prepare: []string{"<p/>"}, wantFirst: "<p/>\n\nfirst"},
		{
			name: "queued prompts keep their blocks once", first: []string{"<a/>"}, queued: [][]string{{"<b/>"}, {"<b/>", "<c/>"}},
			wantFirst: "<a/>\n\nfirst", wantDrain: "<b/>\n\n<c/>\n\nq0\n\nq1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			prepare := func(context.Context, []Message) DeliveryParts { return DeliveryParts{SkillBlocks: tt.prepare} }
			h := newDriverHarness(t, nil, prepare)
			h.start()

			h.d.Submit("first", nil, SubmitMeta{SkillBlocks: tt.first})
			call := h.nextRun()
			if got := lastContent(call); got != tt.wantFirst {
				t.Fatalf("first message = %q, want %q", got, tt.wantFirst)
			}
			for i, blocks := range tt.queued {
				h.d.Submit(fmt.Sprintf("q%d", i), nil, SubmitMeta{SkillBlocks: blocks})
			}
			if len(tt.queued) > 0 {
				drain := call.in.DrainInbox()
				if drain.Message == nil || drain.Message.Content != tt.wantDrain {
					t.Fatalf("drained message = %+v, want content %q", drain.Message, tt.wantDrain)
				}
			}
			call.finish()
			h.waitQuiescent()
		})
	}
}

func TestConversationDriverLineageFollowsConversation(t *testing.T) {
	t.Parallel()
	h := newDriverHarness(t, []Message{{Role: MessageRoleUser, Content: "earlier"}}, nil)
	h.d.lineage = newConversationLineage(h.d.conv)
	h.start()

	h.d.Submit("hello", nil, SubmitMeta{})
	call := h.nextRun()
	if got := len(call.in.Lineage.FullMessages()); got != len(call.in.Conversation) {
		t.Fatalf("run lineage has %d messages, conversation %d; they must match", got, len(call.in.Conversation))
	}
	call.finish()
	h.waitQuiescent()

	h.mu.Lock()
	defer h.mu.Unlock()
	first := h.saves[0]
	if got := first.Lineage.FullMessages(); len(got) != 2 || got[1].Content != "hello" {
		t.Fatalf("pre-run save lineage = %+v, want earlier, hello", got)
	}
}

func TestConversationDriverSteerStartedSequenceAnnouncesSteer(t *testing.T) {
	t.Parallel()
	h := newDriverHarness(t, nil, nil)
	h.start()

	h.steers.Add(SteerMessage{Text: "steer me"})
	h.d.NotifySteer()
	call := h.nextRun()
	call.finish()
	h.waitQuiescent()

	timeout := time.After(driverTestTimeout)
	for {
		select {
		case e := <-h.events:
			if p, ok := e.Payload.(output.SteerReceivedEvent); ok {
				if p.Text != "steer me" {
					t.Fatalf("SteerReceived text = %q, want steer me", p.Text)
				}
				return
			}
		case <-timeout:
			t.Fatal("no SteerReceived event for a steer-started sequence")
		}
	}
}

func TestConversationDriverPromptStartedSequenceDoesNotAnnouncePrompt(t *testing.T) {
	t.Parallel()
	h := newDriverHarness(t, nil, nil)
	h.start()

	h.d.Submit("typed", nil, SubmitMeta{})
	h.nextRun().finish()
	h.waitQuiescent()
	h.closeDriver()

	for {
		select {
		case e := <-h.events:
			if _, ok := e.Payload.(output.SteerReceivedEvent); ok {
				t.Fatal("a submitted prompt was announced as a steer")
			}
		default:
			return
		}
	}
}

func TestConversationDriverWaitQuiescentWaitsForSettlingSave(t *testing.T) {
	t.Parallel()
	saveStarted := make(chan struct{}, 8)
	releaseSave := make(chan struct{})
	h := newDriverHarnessOpts(t, nil, nil, func(o *DriverOptions) {
		o.Save = func(_ context.Context, snap DriverSnapshot) error {
			// Only the settling save (after the reply was adopted) blocks.
			if len(snap.Conversation) == 2 {
				saveStarted <- struct{}{}
				<-releaseSave
			}
			return nil
		}
	})
	h.start()

	h.d.Submit("hi", nil, SubmitMeta{})
	h.nextRun().finish()
	<-saveStarted

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := h.d.WaitQuiescent(ctx); err == nil {
		t.Fatal("WaitQuiescent returned while the settling save was still running")
	}
	close(releaseSave)
	h.waitQuiescent()
}

func TestConversationDriverBusy(t *testing.T) {
	t.Parallel()
	h := newDriverHarness(t, nil, nil)
	h.start()
	if h.d.Busy() {
		t.Fatal("Busy() = true on an idle driver")
	}

	h.d.Submit("hi", nil, SubmitMeta{})
	call := h.nextRun()
	if !h.d.Busy() {
		t.Fatal("Busy() = false while generating")
	}
	call.finish()
	h.waitQuiescent()
	if h.d.Busy() {
		t.Fatal("Busy() = true after the run settled")
	}
}

func TestConversationDriverStopParksQueuedSteers(t *testing.T) {
	t.Parallel()
	h := newDriverHarness(t, nil, nil)
	h.start()

	h.d.Submit("first", nil, SubmitMeta{})
	h.nextRun()
	h.steers.Add(SteerMessage{Text: "queued before stop"})
	h.d.StopTurn()
	// The harness run returns on cancellation. A driver that kept generating
	// for the queued steer would never settle here.
	h.waitSettled(DriverIdle, false)
	h.noRun()
	if got := h.steers.Len(); got != 1 {
		t.Fatalf("steer queue len = %d, want the steer kept, not delivered or dropped", got)
	}
	if err := h.d.WaitIdle(context.Background()); err != nil {
		t.Fatalf("WaitIdle: %v", err)
	}

	h.d.NotifySteer()
	next := h.nextRun()
	if got := lastContent(next); got != "queued before stop" {
		t.Fatalf("run after NotifySteer last message = %q, want the parked steer", got)
	}
	next.finish()
	h.waitQuiescent()
}

func TestConversationDriverDetachSteers(t *testing.T) {
	t.Parallel()
	h := newDriverHarness(t, nil, nil)
	h.start()

	h.d.Submit("first", nil, SubmitMeta{})
	call := h.nextRun()
	h.d.DetachSteers()
	h.steers.Add(SteerMessage{Text: "for the successor"})
	if drain := call.in.DrainInbox(); drain.Message != nil {
		t.Fatalf("boundary drain = %+v, want nothing from a detached queue", drain.Message)
	}
	h.d.NotifySteer()
	call.finish()
	if err := h.d.WaitIdle(context.Background()); err != nil {
		t.Fatalf("WaitIdle: %v", err)
	}
	h.noRun()
	if got := h.steers.Len(); got != 1 {
		t.Fatalf("steer queue len = %d, want the steer left for its owner", got)
	}
}

func TestConversationDriverWaitIdleIgnoresPendingAndParkedSteers(t *testing.T) {
	t.Parallel()
	h := newDriverHarness(t, nil, nil)
	h.bg.setPending("child")
	h.start()

	h.d.Submit("go", nil, SubmitMeta{})
	h.nextRun()
	h.steers.Add(SteerMessage{Text: "parked"})
	h.d.StopTurn()
	h.waitSettled(DriverWaiting, true)

	ctx, cancel := context.WithTimeout(context.Background(), driverTestTimeout)
	defer cancel()
	if err := h.d.WaitIdle(ctx); err != nil {
		t.Fatalf("WaitIdle = %v, want nil while a child is pending and a steer is queued", err)
	}
	short, shortCancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer shortCancel()
	if err := h.d.WaitQuiescent(short); err == nil {
		t.Fatal("WaitQuiescent returned with a child pending and a steer queued")
	}
}

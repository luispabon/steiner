package agent

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/output"
)

func TestConversationDriverProcessSteersRewritesDelivery(t *testing.T) {
	t.Parallel()
	h := newDriverHarnessOpts(t, nil, nil, func(opts *DriverOptions) {
		opts.ProcessSteers = func(steers []SteerMessage) ([]SteerMessage, []string) {
			processed := make([]SteerMessage, len(steers))
			for i, steer := range steers {
				steer.Text = strings.ReplaceAll(steer.Text, "[img-1]", "[img-2]")
				steer.Images = []ImageBlock{{ID: "img-2", FilePath: "/phase/two.png"}}
				processed[i] = steer
			}
			return processed, []string{"oneshot steer image img-9 was not delivered"}
		}
	})
	h.start()

	h.steers.Add(SteerMessage{Text: "look at [img-1]", Images: []ImageBlock{{ID: "img-1", FilePath: "/launch/one.png"}}})
	h.d.NotifySteer()
	call := h.nextRun()
	last := call.in.Conversation[len(call.in.Conversation)-1]
	if last.Content != "look at [img-2]" {
		t.Fatalf("delivered content = %q, want the rewritten marker only", last.Content)
	}
	if len(last.Images) != 1 || last.Images[0].ID != "img-2" || last.Images[0].FilePath != "/phase/two.png" {
		t.Fatalf("delivered images = %+v, want the rewritten image", last.Images)
	}
	call.finish()
	h.waitQuiescent()

	timeout := time.After(driverTestTimeout)
	for {
		select {
		case event := <-h.events:
			payload, ok := event.Payload.(output.ConversationWarningEvent)
			if !ok || payload.Kind != output.ConversationWarningKindRun {
				continue
			}
			if payload.Message != "oneshot steer image img-9 was not delivered" {
				t.Fatalf("warning = %q", payload.Message)
			}
			return
		case <-timeout:
			t.Fatal("no warning event for the processed steer")
		}
	}
}

func TestConversationDriverTakeBackRacingDrain(t *testing.T) {
	t.Parallel()
	for i := range 50 {
		h := newDriverHarness(t, nil, nil)
		h.start()
		h.d.Submit("go", nil, SubmitMeta{})
		call := h.nextRun()
		h.steers.Add(SteerMessage{Text: "steer"})

		var wg sync.WaitGroup
		barrier := make(chan struct{})
		var takenBack []SteerMessage
		var drained InboxDrain
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-barrier
			takenBack = h.steers.Drain()
		}()
		go func() {
			defer wg.Done()
			<-barrier
			drained = call.in.DrainInbox()
		}()
		close(barrier)
		wg.Wait()

		delivered := 0
		if drained.Message != nil && strings.Contains(drained.Message.Content, "steer") {
			delivered++
		}
		delivered += len(takenBack)
		if delivered != 1 {
			t.Fatalf("iteration %d: steer delivered %d times (drain=%+v takeBack=%v), want exactly once", i, delivered, drained, takenBack)
		}
		call.finish()
		h.waitQuiescent()
	}
}

func TestConversationDriverSubmitRacingCompletionOneSequence(t *testing.T) {
	t.Parallel()
	h := newDriverHarness(t, nil, nil)
	h.bg.setPending("a")

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		h.d.Submit("question", nil, SubmitMeta{})
	}()
	go func() {
		defer wg.Done()
		h.d.DeliverCompletions([]SubAgentCompletion{completionFor(1, "a")})
	}()
	wg.Wait()
	h.start()

	call := h.nextRun()
	msg := call.in.Conversation[len(call.in.Conversation)-1]
	if !strings.Contains(msg.Content, `agent_id="a"`) || !strings.HasSuffix(msg.Content, "question") {
		t.Fatalf("first message = %q, want envelope then question", msg.Content)
	}
	if msg.Source != "" {
		t.Fatalf("Source = %q, want real user message", msg.Source)
	}
	call.finish()
	h.waitQuiescent()
	h.noRun()
}

func TestConversationDriverPendingLineExcludesDeliveredAgent(t *testing.T) {
	t.Parallel()
	h := newDriverHarness(t, nil, nil)
	h.bg.setPending("a", "b")
	h.start()

	h.d.DeliverCompletions([]SubAgentCompletion{completionFor(1, "a")})
	h.clock.fire()
	call := h.nextRun()
	content := lastContent(call)
	pendingLine := content[strings.Index(content, "<steiner-sub-agents-pending>"):strings.Index(content, "</steiner-sub-agents-pending>")]
	if !strings.Contains(pendingLine, "b (code") || strings.Contains(pendingLine, "a (code") {
		t.Fatalf("pending line = %q, want only b", pendingLine)
	}
	if !strings.Contains(content, `agent_id="a"`) {
		t.Fatalf("message = %q, want envelope for a", content)
	}
	call.finish()
}

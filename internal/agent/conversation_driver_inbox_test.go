package agent

import (
	"strings"
	"sync"
	"testing"
)

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

package tui

import (
	"fmt"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/output"
)

func TestEventBridgeQueuesBeforeStartAndPreservesOrder(t *testing.T) {
	const total = 2000 // far above the channel buffer
	b := newEventBridge(4)

	emitted := make(chan struct{})
	go func() {
		for i := range total {
			b.Emit(output.Event{Type: fmt.Sprint(i)})
		}
		close(emitted)
	}()
	select {
	case <-emitted:
	case <-time.After(5 * time.Second):
		t.Fatal("Emit blocked before start")
	}

	b.start()
	b.start() // idempotent
	for i := range total {
		select {
		case msg := <-b.Messages():
			got := msg.(runtimeEventMsg).Event.Type
			if want := fmt.Sprint(i); got != want {
				t.Fatalf("event %d = %q, want %q", i, got, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out waiting for event %d", i)
		}
	}

	b.Emit(output.Event{Type: "live"})
	select {
	case msg := <-b.Messages():
		if got := msg.(runtimeEventMsg).Event.Type; got != "live" {
			t.Fatalf("post-start event = %q, want live", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("post-start event not delivered")
	}
}

func TestEventBridgeCloseUnblocksFlush(t *testing.T) {
	b := newEventBridge(1)
	for i := range 10 {
		b.Emit(output.Event{Type: fmt.Sprint(i)})
	}
	b.start()
	b.close()
	b.Emit(output.Event{Type: "after-close"}) // must not hang or panic
}

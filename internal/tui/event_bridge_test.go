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

	returned := make(chan struct{})
	go func() {
		b.Emit(output.Event{Type: "after-close"})
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(5 * time.Second):
		t.Fatal("Emit blocked after close")
	}
}

func TestEventBridgeDropsEventsAfterClose(t *testing.T) {
	pendingLen := func(b *eventBridge) int {
		b.mu.Lock()
		defer b.mu.Unlock()
		return len(b.pending)
	}
	tests := []struct {
		name  string
		setup func(t *testing.T, b *eventBridge)
	}{
		{"closed before start", func(_ *testing.T, b *eventBridge) {
			b.close()
		}},
		{"closed mid-flush", func(t *testing.T, b *eventBridge) {
			for i := range 10 {
				b.Emit(output.Event{Type: fmt.Sprint(i)})
			}
			b.start() // buffer of 1 leaves flush blocked on the channel
			b.close()
			deadline := time.Now().Add(5 * time.Second)
			for {
				b.mu.Lock()
				live := b.live
				b.mu.Unlock()
				if live {
					return
				}
				if time.Now().After(deadline) {
					t.Fatal("flush did not settle after close")
				}
				time.Sleep(time.Millisecond)
			}
		}},
		{"closed after live", func(t *testing.T, b *eventBridge) {
			b.start()
			deadline := time.Now().Add(5 * time.Second)
			for {
				b.mu.Lock()
				live := b.live
				b.mu.Unlock()
				if live {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("bridge did not go live")
				}
				time.Sleep(time.Millisecond)
			}
			b.close()
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := newEventBridge(1)
			tt.setup(t, b)
			for i := range 100 {
				b.Emit(output.Event{Type: fmt.Sprint("late-", i)})
			}
			if n := pendingLen(b); n != 0 {
				t.Fatalf("pending len = %d after close, want 0", n)
			}
		})
	}
}

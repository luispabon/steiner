package tui

import (
	"sync"

	tea "charm.land/bubbletea/v2"

	"github.com/luispabon/steiner/internal/output"
)

// eventBridge carries runtime events into the Bubble Tea message loop.
//
// Events emitted before start (session resume replays the whole saved
// conversation before the program exists) are queued in order instead of
// blocking on a channel nothing drains yet. start flushes the queue into the
// channel, after which Emit sends directly with backpressure.
type eventBridge struct {
	ch   chan tea.Msg
	done chan struct{}

	mu      sync.Mutex
	live    bool
	pending []tea.Msg
	once    sync.Once
}

func newEventBridge(buffer int) *eventBridge {
	if buffer < 1 {
		buffer = 1
	}
	return &eventBridge{ch: make(chan tea.Msg, buffer), done: make(chan struct{})}
}

// Emit delivers event to the TUI's message loop. Before start it queues the
// event. Once done is closed (after the program has exited) events are dropped
// in every state, before or after start, so nothing queues in pending forever
// and no emitting goroutine hangs on a channel nobody drains anymore.
func (b *eventBridge) Emit(event output.Event) {
	if b == nil {
		return
	}
	msg := runtimeEventMsg{Event: event}
	b.mu.Lock()
	select {
	case <-b.done:
		b.mu.Unlock()
		return
	default:
	}
	if !b.live {
		b.pending = append(b.pending, msg)
		b.mu.Unlock()
		return
	}
	b.mu.Unlock()
	select {
	case b.ch <- msg:
	case <-b.done:
	}
}

// start flushes events queued before the program existed, in order, then
// switches Emit to direct sends. Emits racing with the flush keep queueing
// until the queue is empty, so ordering is preserved. Idempotent.
func (b *eventBridge) start() {
	if b == nil {
		return
	}
	b.once.Do(func() { go b.flush() })
}

func (b *eventBridge) flush() {
	for {
		b.mu.Lock()
		batch := b.pending
		b.pending = nil
		if len(batch) == 0 {
			b.live = true
			b.mu.Unlock()
			return
		}
		b.mu.Unlock()
		for _, msg := range batch {
			select {
			case b.ch <- msg:
			case <-b.done:
				// Closed mid-flush: release the queue and go live so the
				// state is terminal; Emit drops from here on.
				b.mu.Lock()
				b.pending = nil
				b.live = true
				b.mu.Unlock()
				return
			}
		}
	}
}

// close marks the bridge closed so any Emit calls racing with or following
// program shutdown return instead of blocking forever.
func (b *eventBridge) close() {
	if b == nil {
		return
	}
	select {
	case <-b.done:
	default:
		close(b.done)
	}
}

func (b *eventBridge) Messages() <-chan tea.Msg {
	if b == nil {
		return nil
	}
	return b.ch
}

func (b *eventBridge) OnEvent(event output.Event) {
	b.Emit(event)
}

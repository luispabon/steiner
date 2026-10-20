package provider

import (
	"context"
	"sync"
)

// claudeSubQueue is an unbounded, cancellation-aware FIFO of connection events.
type claudeSubQueue struct {
	mu     sync.Mutex
	cond   *sync.Cond
	items  []claudeSubEvent
	closed bool
}

func newClaudeSubQueue() *claudeSubQueue {
	q := &claudeSubQueue{}
	q.cond = sync.NewCond(&q.mu)
	return q
}

// push appends ev without blocking. Events pushed after close are dropped.
func (q *claudeSubQueue) push(ev claudeSubEvent) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return
	}
	q.items = append(q.items, ev)
	q.cond.Signal()
}

// pop returns the oldest event, waiting until one is available, the queue
// closes or ctx is done. ok is false in the latter two cases.
func (q *claudeSubQueue) pop(ctx context.Context) (claudeSubEvent, bool) {
	ev, ok, _ := q.popInterruptible(ctx, nil)
	return ev, ok
}

// popInterruptible waits for the oldest event. An interrupt is checked before
// removing an ordinary event, including when both are already ready. The third
// result reports that the wait ended because interrupt was signaled.
func (q *claudeSubQueue) popInterruptible(ctx context.Context, interrupt <-chan struct{}) (claudeSubEvent, bool, bool) {
	wake := make(chan struct{})
	defer close(wake)
	go func() {
		select {
		case <-ctx.Done():
			q.mu.Lock()
			q.cond.Broadcast()
			q.mu.Unlock()
		case <-interrupt:
			q.mu.Lock()
			q.cond.Broadcast()
			q.mu.Unlock()
		case <-wake:
		}
	}()

	q.mu.Lock()
	defer q.mu.Unlock()
	for {
		if interrupt != nil {
			select {
			case <-interrupt:
				return claudeSubEvent{}, false, true
			default:
			}
		}
		if len(q.items) > 0 {
			ev := q.items[0]
			q.items = q.items[1:]
			return ev, true, false
		}
		if q.closed || ctx.Err() != nil {
			return claudeSubEvent{}, false, false
		}
		q.cond.Wait()
	}
}

// close marks the queue closed and wakes every waiter. Buffered events stay
// readable until drained.
func (q *claudeSubQueue) close() {
	q.mu.Lock()
	q.closed = true
	q.cond.Broadcast()
	q.mu.Unlock()
}

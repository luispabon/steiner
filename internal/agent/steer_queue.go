package agent

import "sync"

// SteerQueue is the single queue of pending steering messages for an
// interactive session. It is safe for concurrent use: the TUI goroutine
// adds and takes, and the agent run goroutine drains.
type SteerQueue struct {
	mu   sync.Mutex
	msgs []SteerMessage
}

// NewSteerQueue returns an empty queue ready for use. The zero value is
// also usable; this exists so callers can construct one in a struct
// literal without taking the address of a composite literal.
func NewSteerQueue() *SteerQueue {
	return &SteerQueue{}
}

// Add appends a steering message to the queue.
func (q *SteerQueue) Add(msg SteerMessage) {
	if q == nil {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.msgs = append(q.msgs, msg)
}

// Drain returns all pending messages and empties the queue. The agent run
// loop calls this at a turn boundary.
func (q *SteerQueue) Drain() []SteerMessage {
	if q == nil {
		return nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	msgs := q.msgs
	q.msgs = nil
	return msgs
}

// Take returns all pending messages and empties the queue, for returning
// them to the user's composer. It is identical to Drain and exists to name
// the caller's intent; both share the mutex, so a message is either drained
// into the conversation or taken back, never both.
func (q *SteerQueue) Take() []SteerMessage {
	return q.Drain()
}

// Snapshot returns a copy of the pending messages without removing them.
// Callers must not retain the returned slice across mutations.
func (q *SteerQueue) Snapshot() []SteerMessage {
	if q == nil {
		return nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.msgs) == 0 {
		return nil
	}
	msgs := make([]SteerMessage, len(q.msgs))
	copy(msgs, q.msgs)
	return msgs
}

// Len returns the number of pending messages.
func (q *SteerQueue) Len() int {
	if q == nil {
		return 0
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.msgs)
}

// Clear discards all pending messages.
func (q *SteerQueue) Clear() {
	if q == nil {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.msgs = nil
}

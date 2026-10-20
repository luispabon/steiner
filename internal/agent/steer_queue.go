package agent

import (
	"strings"
	"sync"

	"github.com/luispabon/steiner/internal/output"
)

// SteerQueue is the single queue of pending steering messages for an
// interactive session. It is safe for concurrent use: the TUI goroutine
// adds, snapshots, and drains for take-back, and the agent run goroutine
// drains at turn boundaries.
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
// loop calls this at a turn boundary, and the TUI calls it for take-back.
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

// MergeSteers joins queued steering messages into the single user message the
// runner appends to the conversation, joining texts with a blank line and
// concatenating image blocks. Composer markers are store IDs, so the texts are
// passed through unchanged.
func MergeSteers(steers []SteerMessage) Message {
	if len(steers) == 1 {
		return Message{Role: MessageRoleUser, Content: steers[0].Text, Images: steers[0].Images}
	}
	var texts []string
	var images []ImageBlock
	for _, s := range steers {
		texts = append(texts, s.Text)
		images = append(images, s.Images...)
	}
	return Message{Role: MessageRoleUser, Content: strings.Join(texts, "\n\n"), Images: images}
}

// outputImageBlocks converts agent image blocks to the output-package mirror
// used in event payloads. It returns nil for an empty slice.
func outputImageBlocks(blocks []ImageBlock) []output.ImageBlock {
	if len(blocks) == 0 {
		return nil
	}
	converted := make([]output.ImageBlock, len(blocks))
	for i, b := range blocks {
		converted[i] = output.ImageBlock{
			ID:        b.ID,
			FilePath:  b.FilePath,
			MediaType: b.MediaType,
			Data:      b.Data,
			Width:     b.Width,
			Height:    b.Height,
			SizeBytes: b.SizeBytes,
		}
	}
	return converted
}

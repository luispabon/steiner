package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// claudeSubFakeConn is an in-memory claudeSubConn for tests. Send records lines
// and, when responder is set, pushes scripted events for the router to read.
// Close models the production contract: it is bounded (closeDelay) and closes
// Events.
type claudeSubFakeConn struct {
	mu         sync.Mutex
	sent       [][]byte
	events     chan claudeSubEvent
	err        error
	closed     bool
	sendErr    error
	sendBlock  chan struct{} // when non-nil, Send waits for it before recording
	closeDelay time.Duration // models a bounded Close; Events closes afterwards
	closeErr   error         // when set, Close reports local shutdown did not complete
	responder  func(line []byte) []claudeSubEvent

	drainOnce sync.Once
	drained   chan struct{} // closed on the first Err call (the router's control.close)
}

func newClaudeSubFakeConn() *claudeSubFakeConn {
	return &claudeSubFakeConn{
		events:  make(chan claudeSubEvent, claudeSubEventBuffer),
		drained: make(chan struct{}),
	}
}

func (f *claudeSubFakeConn) Send(line []byte) error {
	f.mu.Lock()
	block := f.sendBlock
	sendErr := f.sendErr
	f.mu.Unlock()

	if block != nil {
		<-block
	}
	if sendErr != nil {
		return sendErr
	}

	cp := make([]byte, len(line))
	copy(cp, line)
	f.mu.Lock()
	f.sent = append(f.sent, cp)
	responder := f.responder
	f.mu.Unlock()

	if responder != nil {
		for _, ev := range responder(cp) {
			f.push(ev)
		}
	}
	return nil
}

func (f *claudeSubFakeConn) Events() <-chan claudeSubEvent { return f.events }

func (f *claudeSubFakeConn) Err() error {
	// In discovery the only caller is the router's control.close, so observing
	// this proves the router drained its control and returned.
	f.drainOnce.Do(func() { close(f.drained) })
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.err
}

func (f *claudeSubFakeConn) Close(ctx context.Context) error {
	f.mu.Lock()
	delay := f.closeDelay
	cerr := f.closeErr
	f.mu.Unlock()
	if delay > 0 {
		timer := time.NewTimer(delay)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("fake close did not complete: %w", ctx.Err())
		}
	}
	if cerr != nil {
		return cerr
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.closed {
		f.closed = true
		close(f.events)
	}
	return nil
}

// push queues an event for the router. Tests must not push after Close.
func (f *claudeSubFakeConn) push(ev claudeSubEvent) {
	f.mu.Lock()
	closed := f.closed
	f.mu.Unlock()
	if closed {
		return
	}
	f.events <- ev
}

func (f *claudeSubFakeConn) isClosed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

func (f *claudeSubFakeConn) sentLines() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.sent))
	for i, line := range f.sent {
		out[i] = string(line)
	}
	return out
}

// waitSent blocks until at least n lines have been sent and returns them all.
func (f *claudeSubFakeConn) waitSent(t *testing.T, n int) []string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if lines := f.sentLines(); len(lines) >= n {
			return lines
		}
		if time.Now().After(deadline) {
			t.Fatalf("conn sent %d lines, want at least %d", len(f.sentLines()), n)
		}
		time.Sleep(time.Millisecond)
	}
}

// newClaudeSubTestControl creates a control and stops its writer goroutine when
// the test ends.
func newClaudeSubTestControl(t *testing.T, conn claudeSubConn) *claudeSubControl {
	t.Helper()
	control := newClaudeSubControl(conn)
	t.Cleanup(control.close)
	return control
}

// claudeSubSuccessEvent builds a successful control_response event.
func claudeSubSuccessEvent(requestID string, response json.RawMessage) claudeSubEvent {
	inner := map[string]any{"subtype": "success", "request_id": requestID}
	if response != nil {
		inner["response"] = response
	}
	raw, _ := json.Marshal(map[string]any{"type": "control_response", "response": inner})
	return claudeSubEvent{Type: "control_response", Raw: raw}
}

// claudeSubErrorEvent builds an error control_response event.
func claudeSubErrorEvent(requestID, msg string) claudeSubEvent {
	raw, _ := json.Marshal(map[string]any{
		"type": "control_response",
		"response": map[string]any{
			"subtype":    "error",
			"request_id": requestID,
			"error":      msg,
		},
	})
	return claudeSubEvent{Type: "control_response", Raw: raw}
}

// claudeSubSubtypeEvent builds a control_response event with an arbitrary
// subtype and no payload.
func claudeSubSubtypeEvent(requestID, subtype string) claudeSubEvent {
	raw, _ := json.Marshal(map[string]any{
		"type": "control_response",
		"response": map[string]any{
			"subtype":    subtype,
			"request_id": requestID,
		},
	})
	return claudeSubEvent{Type: "control_response", Raw: raw}
}

// claudeSubSentControlRequest decodes a sent control_request line into its id
// and request object. It must run on the test goroutine.
func claudeSubSentControlRequest(t *testing.T, line string) (string, map[string]any) {
	t.Helper()
	var envelope struct {
		RequestID string         `json:"request_id"`
		Request   map[string]any `json:"request"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &envelope); err != nil {
		t.Fatalf("decode sent line %q: %v", line, err)
	}
	return envelope.RequestID, envelope.Request
}

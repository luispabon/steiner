package provider

import (
	"context"
)

// claudeSubPendingCall plumbing: the stream-time tool-use registrations a
// session holds, their stale-pending settlement, and the conversions the
// delivery path needs. The drain these functions use lives in
// claudesub_drain.go.

// claudeSubFinishPending settles the stream-time tool calls this request did not
// answer. It returns the number of stale calls it drained to the interrupted
// turn's aborted result: a call retired as terminal-stale, and a drain that
// failed, count zero.
func claudeSubFinishPending(ctx context.Context, s *claudeSubSession, req ChatRequest) (int, error) {
	if len(s.pending) == 0 {
		return 0, nil
	}
	provided := make(map[string]struct{})
	for _, msg := range req.Messages {
		if msg.Role == MessageRoleTool {
			provided[msg.ToolCallID] = struct{}{}
		}
	}
	var stale []claudeSubPendingCall
	for _, pending := range s.pending {
		if _, ok := provided[pending.ID]; !ok {
			stale = append(stale, pending)
		}
	}
	if len(stale) == 0 {
		return 0, nil
	}
	if s.delivery.terminal {
		// The previous delivery already read the turn's terminal result, so the
		// CLI is idle and has no turn to interrupt: an interrupt here would only
		// produce a stray result. The stale handles are retired without claiming
		// a drain or assuming any tool ran, and the request then goes through the
		// ordinary mismatch and restore rules.
		for _, pending := range stale {
			s.host.resolve(pending.Handle, claudeSubToolResult{Text: "tool call interrupted", IsError: true})
			claudeSubRemovePending(s, pending.ID)
		}
		s.host.cancelUnresolved(s.pending)
		return 0, nil
	}
	// The interrupted turn still reports its own terminal result. Draining to it
	// under the same proof the cancel path uses both stops the turn and empties the
	// queue, so a later call never reads this turn's result as its own. The drain
	// runs on a context the caller cannot abort, so the proof completes even when
	// the request is cancelled.
	if err := claudeSubDrainToAbortedResult(ctx, s); err != nil {
		// The CLI may still produce this turn's events, so the process is never
		// reused: the existing unsafe-session path retires it on the next use and
		// reports the replacement.
		s.abandon()
		return 0, err
	}
	s.sync.markInterrupted()
	for _, pending := range stale {
		s.host.resolve(pending.Handle, claudeSubToolResult{Text: "tool call interrupted", IsError: true})
		claudeSubRemovePending(s, pending.ID)
	}
	// A call the CLI opened for an id steiner never registered can never receive a
	// result now either, so it is retired with the stale ones. The calls this
	// request does provide keep their handles for the delivery that resolves them.
	s.host.cancelUnresolved(s.pending)
	return len(stale), nil
}

func claudeSubPendingByID(s *claudeSubSession, id string) *claudeSubPendingCall {
	for i := range s.pending {
		if s.pending[i].ID == id {
			return &s.pending[i]
		}
	}
	return nil
}

func claudeSubRemovePending(s *claudeSubSession, id string) {
	for i := range s.pending {
		if s.pending[i].ID == id {
			s.pending = append(s.pending[:i], s.pending[i+1:]...)
			return
		}
	}
}

func claudeSubToolResultFromMessage(msg Message) claudeSubToolResult {
	return claudeSubToolResult{Text: msg.Content, Images: msg.Images, IsError: false}
}

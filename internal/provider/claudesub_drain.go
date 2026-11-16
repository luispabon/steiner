package provider

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// claudeSubDrainTimeout bounds one interrupted-turn drain, including the
// interrupt acknowledgement and the wait for the aborted result. Tests shorten
// it.
var claudeSubDrainTimeout = 5 * time.Second

// claudeSubDrainAbortedSubtype is the result subtype an interrupted turn
// reports. It is the fallback proof for a CLI version that publishes no
// terminal_reason.
const claudeSubDrainAbortedSubtype = "error_during_execution"

var (
	// errClaudeSubDrainRevoked reports that the session ended while the drain
	// waited, so the drain stopped instead of outliving it.
	errClaudeSubDrainRevoked = errors.New("claude_subscription session closed during the interrupt drain")
	// errClaudeSubDrainConnectionClosed reports that the CLI connection closed
	// before the drained turn reported its result.
	errClaudeSubDrainConnectionClosed = errors.New("claude CLI connection closed during the interrupt drain")
	// errClaudeSubDrainEventAfterResult reports that the CLI emitted an event
	// after the aborted result that proves its turn ended, so nothing after it can
	// be attributed to a later call.
	errClaudeSubDrainEventAfterResult = errors.New("claude CLI sent events after the aborted result")
)

// claudeSubDelivery records what one delivery's event stream produced, so a
// cancelled delivery can tell whether its process is provably clean. The
// delivery that owns it resets it before it writes anything.
type claudeSubDelivery struct {
	// entries is the ledger length the delivery started from, taken once its
	// planned record was adopted. Everything the delivery appends after that is
	// a user or tool entry it wrote itself.
	entries int
	// terminal is set once the turn's terminal result was decoded.
	terminal bool
	// assistant is set once an assistant message of this delivery was recorded.
	assistant bool
}

// claudeSubRecoverInterrupted settles a delivery the caller stopped after it
// reached the CLI. When the CLI acknowledges an interrupt and ends the turn with
// its own aborted result, the process holds a transcript steiner can prove and
// is reused: open tool calls are retired, and the record accepts the partial the
// agent kept plus its interruption note. Any other outcome, and every delivery
// that cannot be proven clean, abandons the process as before.
func claudeSubRecoverInterrupted(ctx context.Context, s *claudeSubSession) {
	if claudeSubInterruptedDrainable(s) && claudeSubDrainToAbortedResult(ctx, s) == nil {
		claudeSubDiscardPending(s)
		s.sync.markDrained(s.delivery.entries)
		return
	}
	s.abandon()
}

// claudeSubInterruptedDrainable reports whether a cancelled delivery may be
// drained instead of abandoning its process. A drain proves the CLI is clean
// only when the turn's terminal result has not been read, the delivery recorded
// no assistant message, the record holds nothing but the user and tool entries
// this delivery appended, and the session can still accept an interrupt.
func claudeSubInterruptedDrainable(s *claudeSubSession) bool {
	if s.unsafeForReuse() || s.isGone() || s.overage() != nil ||
		s.delivery.terminal || s.delivery.assistant {
		return false
	}
	return claudeSubDeliveryTail(s.sync.entries, s.delivery.entries)
}

// claudeSubDiscardPending retires every open MCP call of the session that holds
// no stored result, and clears the session's stream-time registrations. The
// interrupted turn's tool uses were never run by the CLI, or the CLI cancelled
// them, so no result is recorded and none of those calls may survive into a
// later request. The caller holds the whole-call lock.
func claudeSubDiscardPending(s *claudeSubSession) {
	if s.host != nil {
		s.host.cancelUnresolved(nil)
	}
	s.pending = nil
}

// claudeSubDrainToAbortedResult sends an interrupt and reads the session's events
// to the turn's aborted terminal result. It emits nothing to the caller and
// records nothing in the ledger: it only proves the CLI stopped the turn steiner
// started. A nil return is that proof. The interrupt runs on a context the
// cancelled caller cannot abort, and the whole drain shares one deadline bounded
// by claudeSubDrainTimeout. A session that goes away, a closed connection, a
// timeout, an overage or usage limit, a malformed envelope, and any result other
// than the CLI's own aborted error all return the reason the proof failed.
func claudeSubDrainToAbortedResult(ctx context.Context, s *claudeSubSession) error {
	drainCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), claudeSubDrainTimeout)
	defer cancel()
	if _, err := s.control.interrupt(drainCtx); err != nil {
		return fmt.Errorf("interrupt the claude_subscription turn: %w", err)
	}
	// Only a result envelope has to decode: what the CLI emits between the
	// acknowledgement and the result belongs to a stream steiner already stopped
	// reading, and its ordering is not steiner's to police.
	decoder := newClaudeSubDecoder(claudeSubDecodeHooks{})
	for {
		if err := s.overage(); err != nil {
			return err
		}
		ev, ok, revoked := s.queue.popInterruptible(drainCtx, s.gone)
		if revoked {
			return errClaudeSubDrainRevoked
		}
		if !ok {
			if err := s.overage(); err != nil {
				return err
			}
			if err := drainCtx.Err(); err != nil {
				return fmt.Errorf("drain the claude_subscription turn: %w", err)
			}
			return errClaudeSubDrainConnectionClosed
		}
		if ev.SessionID != "" {
			s.sync.cliSessionID = ev.SessionID
		}
		if claudeSubEventIsOverage(ev) {
			if err := s.overage(); err != nil {
				return err
			}
			return errClaudeSubPaidExtraUsage
		}
		if limit := claudeSubEventUsageLimit(ev); limit != nil {
			return limit
		}
		if ev.Type != "result" {
			continue
		}
		decoded, err := decoder.decode(ev)
		if err != nil {
			// A result this decoder cannot explain may still have closed the turn,
			// so the process is no longer provably clean: fail the proof.
			return fmt.Errorf("decode the interrupted claude_subscription result: %w", err)
		}
		for _, item := range decoded {
			if item.Kind != claudeSubDecodeResult {
				continue
			}
			if !claudeSubDrainAborted(item.Result) {
				return fmt.Errorf("interrupted claude_subscription turn ended with subtype %q and terminal reason %q", item.Result.Subtype, item.Result.TerminalReason)
			}
			// The CLI emits nothing after an aborted result until the next user
			// input (probe timelines E1 to E6, CLI 2.1.316). An event already
			// queued here means that rule did not hold, so nothing after the result
			// can be attributed and the process is not clean.
			if _, queued := s.queue.tryPop(); queued {
				return errClaudeSubDrainEventAfterResult
			}
			return nil
		}
	}
}

// claudeSubDrainAborted reports whether r proves the CLI aborted the turn steiner
// interrupted. Only an error result the CLI itself labels as aborted counts: a
// success result means the turn finished before the interrupt landed, and no
// other failure proves the transcript is clean. A CLI version that publishes no
// terminal reason is accepted only through its interruption subtype.
func claudeSubDrainAborted(r *claudeSubResult) bool {
	if r == nil || !r.IsError {
		return false
	}
	switch r.TerminalReason {
	case "aborted_streaming", "aborted_tools":
		return true
	case "":
		return r.Subtype == claudeSubDrainAbortedSubtype
	default:
		return false
	}
}

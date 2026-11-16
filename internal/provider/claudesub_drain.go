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
// is reused: the turn's stream-time tool-use registrations are retired, and the
// record accepts the partial the agent kept plus its interruption note. Any
// other outcome, and every delivery that cannot be proven clean, abandons the
// process as before.
func claudeSubRecoverInterrupted(ctx context.Context, s *claudeSubSession) {
	if claudeSubInterruptedDrainable(s) && claudeSubDrainInterrupted(ctx, s) == nil {
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

// claudeSubDiscardPending retires the MCP calls the interrupted turn registered
// while its tool_use blocks streamed, without recording a result: the CLI never
// called them, so they must not survive into a later request. The caller holds
// the whole-call lock.
func claudeSubDiscardPending(s *claudeSubSession) {
	if s.host != nil {
		for _, pending := range s.pending {
			s.host.cancelCall(pending.Handle)
		}
	}
	s.pending = nil
}

// claudeSubDrainInterrupted sends an interrupt and reads the session's events to
// the turn's aborted terminal result. It emits nothing to the caller and records
// nothing in the ledger: it only proves the CLI stopped the turn steiner started.
// A nil return is that proof. The interrupt runs on a context the cancelled
// caller cannot abort, and the whole drain shares one deadline bounded by
// claudeSubDrainTimeout. A session that goes away, a closed connection, a
// timeout, an overage or usage limit, a malformed envelope, and any result other
// than the CLI's own aborted error all return the reason the proof failed.
func claudeSubDrainInterrupted(ctx context.Context, s *claudeSubSession) error {
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
			return fmt.Errorf("decode the interrupted claude_subscription result: %w", err)
		}
		for _, item := range decoded {
			if item.Kind != claudeSubDecodeResult {
				continue
			}
			if !claudeSubDrainAborted(item.Result) {
				return fmt.Errorf("interrupted claude_subscription turn ended with subtype %q and terminal reason %q", item.Result.Subtype, item.Result.TerminalReason)
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

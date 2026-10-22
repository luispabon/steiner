package provider

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// claudeSubUsageProbeTimeout bounds the startup and the single get_usage request
// of one fresh-process usage confirmation. Teardown is not covered: the pool
// bounds that separately with claudeSubSessionCloseTimeout. A variable so tests
// can shorten it.
var claudeSubUsageProbeTimeout = 8 * time.Second

// claudeSubUsageProbeKeyPrefix is the reserved session-key namespace of a usage
// confirmation. The leading NUL byte keeps it out of every transport session key
// namespace, because those are steiner-generated identifiers without NUL. The
// stdout recorder hashes the key, so the byte is safe to carry, and the sequence
// suffix keeps every attempt distinct.
const claudeSubUsageProbeKeyPrefix = "\x00usage-gate-probe-"

// confirmUsageFresh asks a brand-new CLI process for the account's usage state
// and applies the strict gate to that reply. The CLI caches a usage read it
// could not complete in-process and then answers get_usage from header-seeded
// rate limits, so the live process can stay unconfirmable long after the
// endpoint recovers. A fresh process has no such cache. Exactly one attempt:
// one process starts, answers one get_usage, and is retired.
//
// The pool-wide semaphore serializes confirmations so a burst of refused turns
// never starts a crowd of probe processes. The probe fails closed on any
// startup, transport, or decode failure: only a parsed reply whose
// rate_limits_available is true and extra_usage.is_enabled is false returns nil.
func (p *ClaudeSubscriptionPool) confirmUsageFresh(ctx context.Context) error {
	select {
	case p.usageProbe.sem <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	case <-p.ctx.Done():
		return errClaudeSubPoolClosed
	}
	// Registered first so it runs last: the slot stays taken until the probe
	// process is torn down.
	defer func() { <-p.usageProbe.sem }()
	err := p.runUsageProbe(ctx)
	// A probe step that failed only because the pool shut down reports the closed
	// error, never the cancellation shutdown produced.
	if err != nil && p.ctx.Err() != nil {
		return errClaudeSubPoolClosed
	}
	return err
}

// runUsageProbe performs the one confirmation: startup, a single get_usage, and
// teardown of the probe process.
func (p *ClaudeSubscriptionPool) runUsageProbe(ctx context.Context) error {
	probeCtx, cancel := context.WithTimeout(ctx, claudeSubUsageProbeTimeout)
	defer cancel()
	// Pool shutdown cancels the probe, so a confirmation never outlives the pool.
	stopShutdown := context.AfterFunc(p.ctx, cancel)
	defer stopShutdown()

	// Control-only get_usage needs no model, tools, or system prompt, and the
	// probe session is never persisted.
	key := fmt.Sprintf("%s%d", claudeSubUsageProbeKeyPrefix, p.usageProbe.seq.Add(1))
	h, err := p.acquire(probeCtx, key, claudeSubStartSpec{Persist: false})
	if err != nil {
		return err
	}
	defer p.retireAndRelease(key, h)

	raw, err := h.control.getUsage(probeCtx)
	if err != nil {
		return err
	}
	if err := claudeSubUsageGate(raw); err != nil {
		return err
	}
	// Never authorize through a cancellation or pool shutdown that landed while
	// the confirmation ran.
	if err := ctx.Err(); err != nil {
		return err
	}
	if !p.isRunning() {
		return errClaudeSubPoolClosed
	}
	return nil
}

// claudeSubUsageProbeFailure is a fail-closed refusal: its message is the live
// refusal's message, while its chain also carries the failed confirmation so the
// probe's cause stays observable without changing what the user reads.
type claudeSubUsageProbeFailure struct{ live, probe error }

func (e *claudeSubUsageProbeFailure) Error() string { return e.live.Error() }

// Unwrap exposes both errors, so the refusal still matches the not-ready sentinel
// and a caller can also inspect why the confirmation failed.
func (e *claudeSubUsageProbeFailure) Unwrap() []error { return []error{e.live, e.probe} }

// claudeSubUsageProbeOutcome decides the gate result from the live poll's error
// and the fresh-process confirmation's error. A confirmation that passes clears
// the refusal. A cancelled, timed-out, or pool-closed confirmation keeps its own
// error, because that attempt never answered the question, and a definite
// credits-enabled reply keeps its refusal. Every other confirmation failure
// stays fail-closed with the live refusal's message while carrying the probe
// error in its chain. A failed confirmation never authorizes a turn.
func claudeSubUsageProbeOutcome(liveErr, probeErr error) error {
	switch {
	case probeErr == nil:
		return nil
	case errors.Is(probeErr, context.Canceled),
		errors.Is(probeErr, context.DeadlineExceeded),
		errors.Is(probeErr, errClaudeSubPoolClosed),
		errors.Is(probeErr, errClaudeSubUsageCreditsEnabled):
		return probeErr
	}
	if liveErr == nil {
		return probeErr
	}
	return &claudeSubUsageProbeFailure{live: liveErr, probe: probeErr}
}

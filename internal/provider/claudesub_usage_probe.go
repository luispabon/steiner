package provider

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// claudeSubUsageProbeTimeout bounds one fresh-process usage confirmation:
// startup, the single get_usage request, and teardown. A variable so tests can
// shorten it.
var claudeSubUsageProbeTimeout = 8 * time.Second

// claudeSubUsageProbeKeyPrefix names the ephemeral session of one usage
// confirmation. Session keys are caller-owned transport keys (or the fixed
// default and summary suffixes), so this prefix cannot collide with one in
// practice; the sequence suffix keeps every attempt distinct.
const claudeSubUsageProbeKeyPrefix = "usage-gate-probe-"

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

// claudeSubUsageProbeOutcome decides the gate result from the live poll's error
// and the fresh-process confirmation's error. A confirmation that passes clears
// the refusal. A cancelled, timed-out, or pool-closed confirmation keeps its own
// error, because that attempt never answered the question. A definite
// credits-enabled reply keeps its refusal. Every other confirmation failure
// leaves the live fail-closed error unchanged, so the refusal still names the
// unconfirmable state that was observed.
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
	return liveErr
}

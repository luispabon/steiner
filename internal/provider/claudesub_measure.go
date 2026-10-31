package provider

import (
	"context"
	"time"
)

// claude_subscription process dispositions reported in
// ClaudeSubCallDiagnostics.Process.
const (
	// claudeSubProcessReuse means a live process accepted the request.
	claudeSubProcessReuse = "reuse"
	// claudeSubProcessResume means a new process resumed a stored CLI session.
	claudeSubProcessResume = "resume"
	// claudeSubProcessSpawn means a new process started without a resume target.
	claudeSubProcessSpawn = "spawn"
)

// claude_subscription reseed reasons reported in
// ClaudeSubCallDiagnostics.ReseedReason. Each value names a branch that makes a
// call reseed: the rendered transcript goes to a process that holds no record
// of the request's history.
const (
	// claudeSubReseedNoState: no stored state to resume.
	claudeSubReseedNoState = "no_state"
	// claudeSubReseedFingerprint: the stored prompt and tools fingerprint
	// differs from the request's.
	claudeSubReseedFingerprint = "fingerprint"
	// claudeSubReseedFinalUUID: the stored final message or its uuid is missing
	// or is not reproduced by the request history.
	claudeSubReseedFinalUUID = "final_uuid"
	// claudeSubReseedPrefix: the request history does not carry the stored
	// prefix.
	claudeSubReseedPrefix = "prefix"
	// claudeSubReseedPlan: the request is not a valid append to the record it
	// would have used (live record, resumed record, or a replaced summary
	// process).
	claudeSubReseedPlan = "plan"
	// claudeSubReseedCheckpoint: the live process checkpoint no longer matches
	// the stored state.
	claudeSubReseedCheckpoint = "checkpoint"
	// claudeSubReseedOwnerFork: the stored state is owned by another transport
	// session, so the live process is retired and the request forks the state.
	claudeSubReseedOwnerFork = "owner_fork"
	// claudeSubReseedExited: the live process had exited and was evicted.
	claudeSubReseedExited = "exited"
	// claudeSubReseedRetry: an earlier attempt on the same key left no usable
	// record, so the call starts over on a fresh process.
	claudeSubReseedRetry = "retry"
	// claudeSubReseedHelper: the call runs on an ephemeral summary helper that
	// always starts from the rendered transcript.
	claudeSubReseedHelper = "helper"
)

// claudeSubCallMetrics accumulates the measurements claudeSubStream reports on
// the call's terminal chunk. It is per call, not per session, and is reached
// through the call context (withClaudeSubMetrics) because the helpers that learn
// each fact already receive that context. A nil *claudeSubCallMetrics records
// nothing, so direct claudeSubTurn callers need no setup.
type claudeSubCallMetrics struct {
	process           string
	reseedReason      string
	usageGate         time.Duration
	usageGatePolls    int
	usageProbeSpawned bool
}

// claudeSubMetricsKey keys the call's measurement sink in its context.
type claudeSubMetricsKey struct{}

// withClaudeSubMetrics returns ctx carrying m as the call's measurement sink.
func withClaudeSubMetrics(ctx context.Context, m *claudeSubCallMetrics) context.Context {
	return context.WithValue(ctx, claudeSubMetricsKey{}, m)
}

// claudeSubMetricsFrom returns the call's measurement sink, or nil when the
// call does not measure.
func claudeSubMetricsFrom(ctx context.Context) *claudeSubCallMetrics {
	m, _ := ctx.Value(claudeSubMetricsKey{}).(*claudeSubCallMetrics)
	return m
}

// diagnostics returns the measurement as call diagnostics, or nil when the
// call measured nothing.
func (m *claudeSubCallMetrics) diagnostics() *CallDiagnostics {
	if m == nil {
		return nil
	}
	if m.process == "" && m.reseedReason == "" && m.usageGate == 0 && m.usageGatePolls == 0 && !m.usageProbeSpawned {
		return nil
	}
	return &CallDiagnostics{ClaudeSub: &ClaudeSubCallDiagnostics{
		Process:           m.process,
		ReseedReason:      m.reseedReason,
		UsageGateMillis:   m.usageGate.Milliseconds(),
		UsageGatePolls:    m.usageGatePolls,
		UsageProbeSpawned: m.usageProbeSpawned,
	}}
}

// noteProcess records how s was obtained for this call when the call recorded no
// explicit disposition. It covers calls that deliver without the attach state
// machine (advisor, summary): a primed process served an earlier turn, so it is
// a reuse; an unprimed process that started from a resume target is a resume;
// anything else is a spawn.
func (m *claudeSubCallMetrics) noteProcess(s *claudeSubSession) {
	if m == nil || m.process != "" {
		return
	}
	switch {
	case s.primed:
		m.process = claudeSubProcessReuse
	case s.resume.SessionID != "":
		m.process = claudeSubProcessResume
	default:
		m.process = claudeSubProcessSpawn
	}
}

// setProcess records an explicit disposition the session state cannot show.
func (m *claudeSubCallMetrics) setProcess(process string) {
	if m == nil {
		return
	}
	m.process = process
}

// noteReseed records the branch that makes the call reseed.
func (m *claudeSubCallMetrics) noteReseed(reason string) {
	if m == nil || reason == "" {
		return
	}
	m.reseedReason = reason
}

// clearReseed drops a recorded reseed branch once the call settled without
// reseeding.
func (m *claudeSubCallMetrics) clearReseed() {
	if m == nil {
		return
	}
	m.reseedReason = ""
}

// resetDelivery drops what an earlier delivery attempt of this call recorded
// about how it was served (its process disposition and its reseed branch), so a
// later attempt that actually serves the call is what gets reported. The usage
// gate's time and poll count accumulate across every attempt and stay, and so
// does usageProbeSpawned: a failed attempt really did wait on the gate and may
// really have spawned a probe process for this call.
func (m *claudeSubCallMetrics) resetDelivery() {
	if m == nil {
		return
	}
	m.process = ""
	m.reseedReason = ""
}

// noteUsageGate records the time and poll count of one delivery attempt's
// extra-usage gate. Both accumulate, because every attempt in the call waited.
func (m *claudeSubCallMetrics) noteUsageGate(elapsed time.Duration, polls int) {
	if m == nil {
		return
	}
	m.usageGate += elapsed
	m.usageGatePolls += polls
}

// noteUsageProbe records that a fresh probe process was started for this call.
func (m *claudeSubCallMetrics) noteUsageProbe() {
	if m == nil {
		return
	}
	m.usageProbeSpawned = true
}

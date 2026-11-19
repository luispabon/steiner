package agent

import "sync/atomic"

// interactiveResumePending latches when an interactive session load installs
// a restored conversation, for either the CLI --resume path or an in-TUI
// resume. Prefix baselines are process-local (CacheBaselineStore), so a
// resumed session normally starts with no predecessor to compare against.
// The first cache record that claims the latch reports that missing baseline
// instead of leaving it indistinguishable from a baseline that never ran.
var interactiveResumePending atomic.Bool

// NoteInteractiveResume records that an interactive session was resumed from
// persisted state. Call it only after a resume actually replaced the live
// conversation; forks load their new session through a different path and
// must not arm the latch.
func NoteInteractiveResume() {
	interactiveResumePending.Store(true)
}

// claimResumeBaselineUnavailable consumes the resume latch for one cache
// record. Only a parent record whose comparison actually ran may claim it: a
// delegated child has its own CacheBaselineKey, and a request with no usable
// baseline cannot tell an absent predecessor from a disabled comparison. The
// flag reports that the baseline was unavailable after a resume; it makes no
// claim about why the provider did or did not serve the request from cache.
func claimResumeBaselineUnavailable(req RunRequest, stats requestCacheStats) bool {
	if req.AgentID != "" || !stats.comparisonEnabled {
		return false
	}
	if !interactiveResumePending.CompareAndSwap(true, false) {
		return false
	}
	return !stats.predecessorKnown
}

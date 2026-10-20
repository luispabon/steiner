package agent

// Test-only helpers. No production code calls these; the tests in this
// package exercise them directly.

// SteerInboxDrain adapts a steer queue drain to DrainInbox. Every non-empty
// drain is a wake delivery.
func SteerInboxDrain(drain func() []SteerMessage) func() InboxDrain {
	if drain == nil {
		return nil
	}
	return func() InboxDrain {
		steers := drain()
		if len(steers) == 0 {
			return InboxDrain{}
		}
		merged := MergeSteers(steers)
		return InboxDrain{Message: &merged, Wake: true, UserText: merged.Content, Images: merged.Images}
	}
}

// HighestFidelityCandidate returns the first candidate that satisfies the
// caller's fit predicate. If none fit, it returns the highest-fidelity
// deterministic fallback.
func (l ConversationLineage) HighestFidelityCandidate(fits func([]Message) bool) (ConversationCandidate, bool) {
	candidates := l.Candidates()
	if len(candidates) == 0 {
		return ConversationCandidate{}, false
	}
	if fits == nil {
		return candidates[0], true
	}
	for _, candidate := range candidates {
		if fits(candidate.Messages) {
			return candidate, true
		}
	}
	return candidates[0], true
}

package tui

import "github.com/luispabon/steiner/internal/output"

// applyContextBudgetEvent records the context budget and clears replay
// activity when the budget belongs to a loaded session.
func (m *Model) applyContextBudgetEvent(payload output.ContextBudgetEvent) {
	if output.ContextDiagnosticKind(payload) == "session_loaded" {
		m.clearReplayActivity()
	}
	m.applyContextBudget(payload)
}

// applyContextDiagnosticsEvent fans a diagnostics envelope out to the budget,
// compaction, and session health handlers it carries.
func (m *Model) applyContextDiagnosticsEvent(payload output.ContextDiagnosticsEvent) {
	if payload.Kind == "session_loaded" {
		m.clearReplayActivity()
	}
	if budget, ok := output.AsContextBudgetEvent(payload); ok {
		m.applyContextBudget(budget)
	}
	if compaction, ok := output.AsContextCompactionEvent(payload); ok {
		m.applyCompactionEvent(compaction)
	}
	if health, ok := output.AsContextSessionHealthEvent(payload); ok {
		m.applySessionHealthEvent(health)
	}
}

// applyCompactionEvent updates compaction state, activity, and status context
// for a context compaction event. Shared by the direct ContextCompactionEvent
// case and the ContextDiagnosticsEvent envelope.
func (m *Model) applyCompactionEvent(payload output.ContextCompactionEvent) {
	cs := newCompactionState(payload)
	m.setCompaction(cs)
	switch {
	case cs.Active():
		m.activity = m.activity.waiting("compacting context", compactingLabel(payload))
	case payload.Severity == "skipped":
		// Nothing was compacted; the run continues in the same waiting state a model call uses.
		m.activity = m.activity.waiting("waiting", "")
	default:
		m.activity = m.activity.static("context compacted", compactedLabel(payload))
		m.applyContextBudget(payload.BudgetSnapshot())
	}
	m.status.context = appendStatusContext(m.status.context, compactionStatusFragment(payload))
	// Append separator + summary text when compaction finishes with a summary.
	if !cs.Active() && payload.SummaryText != "" {
		m.content.AppendCompactionResult("Compaction", payload.SummaryText)
	}
}

// applySessionHealthEvent updates status context for a context session health
// event. Shared by the direct ContextSessionHealthEvent case and the
// ContextDiagnosticsEvent envelope.
func (m *Model) applySessionHealthEvent(payload output.ContextSessionHealthEvent) {
	if !m.compaction.Active() {
		m.status.context = appendStatusContext(m.status.context, sessionHealthStatusFragment(payload))
	}
}

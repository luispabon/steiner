package quota

import "time"

const (
	// projectionMinSpan is the least time between the baseline and current
	// observations for a projection.
	projectionMinSpan = 15 * time.Minute
	// projectionMinRise is the least usage rise, in percentage points for a
	// window or as a fraction of Limit for a budget, for a projection.
	projectionMinRise = 2.0
	// projectionResetSlop is how far apart two reset times may be and still
	// describe the same window or budget period.
	projectionResetSlop = time.Minute
	// claudeSubscriptionProviderType mirrors config.ProviderTypeClaudeSubscription;
	// internal/quota is a leaf package and does not import internal/config.
	claudeSubscriptionProviderType = "claude_subscription"
	// claudeSubscriptionWindowDuration is the rolling five-hour window whose
	// usage starts at zero when the window resets.
	claudeSubscriptionWindowDuration = 5 * time.Hour
)

// Projection estimates when a window or budget is used up at the rate seen
// between a baseline observation and the current snapshot.
type Projection struct {
	// OK is true when history supports a projection.
	OK bool
	// EmptyAt is the estimated time usage reaches its full capacity.
	EmptyAt time.Time
	// BeforeReset is true when EmptyAt is before the current reset time.
	BeforeReset bool
}

// ProjectWindow projects the window at windowIndex of current against the
// oldest history point for the same account, label, and reset period. It
// returns a zero Projection when there is no such point, the window has no
// reset time, or the span or rise is below threshold. A Claude subscription
// five-hour window instead projects from its average usage since the inferred
// window start, unless the window has reset or that average is unusable.
// Times are computed from current.FetchedAt, never the wall clock.
func ProjectWindow(history []Snapshot, current Snapshot, windowIndex int) Projection {
	if windowIndex < 0 || windowIndex >= len(current.Windows) {
		return Projection{}
	}
	cur := current.Windows[windowIndex]
	if cur.ResetsAt.IsZero() {
		return Projection{}
	}
	if current.ProviderType == claudeSubscriptionProviderType && cur.Duration == claudeSubscriptionWindowDuration {
		if !current.FetchedAt.Before(cur.ResetsAt) {
			// The window has reset; usage now belongs to the next window.
			return Projection{}
		}
		if start := cur.ResetsAt.Add(-cur.Duration); current.FetchedAt.After(start) {
			if projection, ok := projectFromWindowStart(cur, current.FetchedAt); ok {
				return projection
			}
			// The window average is unusable; fall back to stored history.
		}
	}
	base, baseUsed, ok := oldestMatch(history, current.AccountKey, func(h Snapshot) (float64, bool) {
		for _, w := range h.Windows {
			if w.Label == cur.Label && withinReset(w.ResetsAt, cur.ResetsAt) {
				return w.UsedPercent, true
			}
		}
		return 0, false
	})
	if !ok {
		return Projection{}
	}
	emptyAt, ok := estimateEmpty(base, current, baseUsed, cur.UsedPercent, 100, projectionMinRise)
	if !ok {
		return Projection{}
	}
	return Projection{OK: true, EmptyAt: emptyAt, BeforeReset: emptyAt.Before(cur.ResetsAt)}
}

// projectFromWindowStart projects exhaustion from the average usage of the
// current window, assuming zero usage at the window start. The start is
// inferred from the reset time, not observed.
func projectFromWindowStart(cur Window, now time.Time) (Projection, bool) {
	span := now.Sub(cur.ResetsAt.Add(-cur.Duration))
	if span < projectionMinSpan || cur.UsedPercent < projectionMinRise {
		return Projection{}, false
	}
	remaining := max(100-cur.UsedPercent, 0)
	elapsed := time.Duration(float64(span) * remaining / cur.UsedPercent)
	emptyAt := now.Add(elapsed)
	return Projection{OK: true, EmptyAt: emptyAt, BeforeReset: emptyAt.Before(cur.ResetsAt)}, true
}

// ProjectBudget projects the budget at budgetIndex of current against the oldest
// history point for the same account, label, and reset period. Budgets without
// a positive Limit or a reset time never project. The rise must be at least 2%
// of Limit. Times are computed from current.FetchedAt, never the wall clock.
func ProjectBudget(history []Snapshot, current Snapshot, budgetIndex int) Projection {
	if budgetIndex < 0 || budgetIndex >= len(current.Budgets) {
		return Projection{}
	}
	cur := current.Budgets[budgetIndex]
	if cur.Limit == nil || *cur.Limit <= 0 || cur.ResetsAt == nil {
		return Projection{}
	}
	limit := *cur.Limit
	base, baseUsed, ok := oldestMatch(history, current.AccountKey, func(h Snapshot) (float64, bool) {
		for _, b := range h.Budgets {
			if b.Label == cur.Label && b.ResetsAt != nil && withinReset(*b.ResetsAt, *cur.ResetsAt) {
				return b.Used, true
			}
		}
		return 0, false
	})
	if !ok {
		return Projection{}
	}
	emptyAt, ok := estimateEmpty(base, current, baseUsed, cur.Used, limit, limit*projectionMinRise/100)
	if !ok {
		return Projection{}
	}
	return Projection{OK: true, EmptyAt: emptyAt, BeforeReset: emptyAt.Before(*cur.ResetsAt)}
}

// oldestMatch returns the earliest history point for accountKey for which
// usedIn reports a match, with the usage it recorded.
func oldestMatch(history []Snapshot, accountKey string, usedIn func(Snapshot) (float64, bool)) (Snapshot, float64, bool) {
	var (
		best     Snapshot
		bestUsed float64
		found    bool
	)
	for _, h := range history {
		if h.AccountKey != accountKey {
			continue
		}
		used, ok := usedIn(h)
		if !ok {
			continue
		}
		if !found || h.FetchedAt.Before(best.FetchedAt) {
			best, bestUsed, found = h, used, true
		}
	}
	return best, bestUsed, found
}

// estimateEmpty extrapolates linearly from the base observation to current and
// returns when usage reaches capacity. It fails when the span is under
// projectionMinSpan or the rise is under minRise. minRise must be positive.
func estimateEmpty(base, current Snapshot, baseUsed, curUsed, capacity, minRise float64) (time.Time, bool) {
	span := current.FetchedAt.Sub(base.FetchedAt)
	rise := curUsed - baseUsed
	if span < projectionMinSpan || rise < minRise {
		return time.Time{}, false
	}
	remaining := max(capacity-curUsed, 0)
	elapsed := time.Duration(float64(span) * remaining / rise)
	return current.FetchedAt.Add(elapsed), true
}

// withinReset reports whether two reset times are within projectionResetSlop.
func withinReset(a, b time.Time) bool {
	d := a.Sub(b)
	if d < 0 {
		d = -d
	}
	return d <= projectionResetSlop
}

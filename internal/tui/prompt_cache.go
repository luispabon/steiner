package tui

import "time"

// promptCachePhase is the state of the main prompt cache expiry estimate.
type promptCachePhase int

const (
	// promptCacheOff means no estimate is tracked (fallback disabled or no
	// successful main request yet).
	promptCacheOff promptCachePhase = iota
	// promptCacheWarm means more than a minute is left before the estimate.
	promptCacheWarm
	// promptCacheNear means a minute or less is left before the estimate.
	promptCacheNear
	// promptCacheExpired means the estimate has passed.
	promptCacheExpired
)

// promptCacheStateAt returns the whole minutes left before coldAt and the
// phase at now. A zero coldAt means tracking is off. Expiry is strict, matching
// compactionCacheCold: the deadline instant itself still reports one minute
// left. minutes rounds the remaining time up and is never zero before expiry.
func promptCacheStateAt(now, coldAt time.Time) (int, promptCachePhase) {
	if coldAt.IsZero() {
		return 0, promptCacheOff
	}
	if now.After(coldAt) {
		return 0, promptCacheExpired
	}
	remaining := coldAt.Sub(now)
	minutes := int((remaining + time.Minute - 1) / time.Minute)
	if minutes < 1 {
		minutes = 1
	}
	if remaining <= time.Minute {
		return minutes, promptCacheNear
	}
	return minutes, promptCacheWarm
}

// syncPromptCacheAt projects the cache expiry estimate onto the footer and the
// sidebar CONTEXT card at now.
func (m *Model) syncPromptCacheAt(now time.Time) {
	minutes, phase := promptCacheStateAt(now, m.promptCacheColdAt)
	m.status.promptCacheExpired = phase == promptCacheExpired
	m.sidebar.cacheMinutes = minutes
	m.sidebar.cachePhase = phase
}

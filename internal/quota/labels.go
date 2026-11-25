package quota

import (
	"fmt"
	"math"
	"time"
)

// BudgetLabelSpend is the Budget.Label of a spend-only budget: spend is
// reported with no limit. Providers emit it, and the TUI renders it without a
// label prefix.
const BudgetLabelSpend = "spend"

// namedWindows lists standard window durations with their labels. A duration
// matches an entry when it is within 5% of that entry's duration.
var namedWindows = []struct {
	duration time.Duration
	label    string
}{
	{5 * time.Hour, "5h"},
	{24 * time.Hour, "daily"},
	{7 * 24 * time.Hour, "weekly"},
	{30 * 24 * time.Hour, "monthly"},
}

// ShortDuration formats d concisely for quota displays: 1d17h, 2h05m, 15m,
// or <1m under a minute (including non-positive d). Each unit truncates, so a
// countdown never overstates the time left.
func ShortDuration(d time.Duration) string {
	const day = 24 * time.Hour
	switch {
	case d >= day:
		return fmt.Sprintf("%dd%02dh", d/day, d%day/time.Hour)
	case d >= time.Hour:
		return fmt.Sprintf("%dh%02dm", d/time.Hour, d%time.Hour/time.Minute)
	case d >= time.Minute:
		return fmt.Sprintf("%dm", d/time.Minute)
	default:
		return "<1m"
	}
}

// WindowLabel returns a short label for a rate-limit window of length d.
// Durations within 5% of 5h, 24h, 7d, or 30d return "5h", "daily", "weekly",
// or "monthly". Other durations are humanised, for example "2h" or "3d". A
// non-positive duration returns "usage".
func WindowLabel(d time.Duration) string {
	if d <= 0 {
		return "usage"
	}
	for _, n := range namedWindows {
		tolerance := float64(n.duration) * 0.05
		if math.Abs(float64(d-n.duration)) <= tolerance {
			return n.label
		}
	}
	switch {
	case d >= 24*time.Hour:
		return fmt.Sprintf("%dd", int(math.Round(d.Hours()/24)))
	case d >= time.Hour:
		return fmt.Sprintf("%dh", int(math.Round(d.Hours())))
	default:
		return fmt.Sprintf("%dm", max(1, int(math.Round(d.Minutes()))))
	}
}

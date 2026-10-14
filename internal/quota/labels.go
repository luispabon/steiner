package quota

import (
	"fmt"
	"math"
	"time"
)

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

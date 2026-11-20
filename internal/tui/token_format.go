package tui

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// formatTokens renders a token count in compact form: raw below 1000, a
// one-decimal k/M/B value above, and "-" for zero or negative counts.
func formatTokens(n int) string {
	switch {
	case n <= 0:
		return "-"
	case n < 1_000:
		return strconv.Itoa(n)
	}
	tenths, suffix := scaledTenths(n, 1_000, "k")
	if tenths >= 10_000 {
		tenths, suffix = scaledTenths(n, 1_000_000, "M")
	}
	if tenths >= 10_000 {
		tenths, suffix = scaledTenths(n, 1_000_000_000, "B")
	}
	return fmt.Sprintf("%d.%d%s", tenths/10, tenths%10, suffix)
}

// scaledTenths returns n/divisor in tenths, rounded half up. Integer arithmetic
// keeps boundary values such as 999950 exact, where float division can fall
// either side of the half.
func scaledTenths(n, divisor int, suffix string) (int, string) {
	return (n + divisor/20) / (divisor / 10), suffix
}

// formatPercent renders a rate in [0,1] as one-decimal percent, or "-" when
// the rate is undefined.
func formatPercent(rate float64, ok bool) string {
	if !ok {
		return "-"
	}
	return fmt.Sprintf("%.1f%%", rate*100)
}

// barString renders a width-wide bar with filled cells proportional to
// value/total. A non-positive total leaves the bar empty; a positive value
// always fills at least one cell.
func barString(value, total, width int) string {
	if width <= 0 {
		return ""
	}
	if total <= 0 || value <= 0 {
		return strings.Repeat("░", width)
	}
	filled := min(max(int(math.Round(float64(value)/float64(total)*float64(width))), 1), width)
	return strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
}

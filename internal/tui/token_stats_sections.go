package tui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/luispabon/steiner/internal/usagestats"
)

// tokenStatsSpacing separates the fixed-width columns of the code-block strips.
const tokenStatsSpacing = "  "

// writeTokenHeadline renders the calendar-month total, as a block banner when
// width allows and as a bold line otherwise, followed by its summary lines.
func writeTokenHeadline(sb *strings.Builder, month usagestats.Report, from, now time.Time, width int) {
	dateRange := fmt.Sprintf("last calendar month · %s to %s (UTC)", shortMonthDay(from), shortMonthDay(now))
	total := month.Total.Total()
	if total <= 0 {
		fmt.Fprintf(sb, "%s\n\n%s\n\n", dateRange, tokenStatsEmptyWindow)
		return
	}
	label := formatTokens(total)
	if width >= bannerWidth(label)+tokenStatsBannerMargin {
		writeCodeBlock(sb, renderBanner(label))
		fmt.Fprintf(sb, "*tokens* · %s\n\n", dateRange)
	} else {
		fmt.Fprintf(sb, "**%s tokens** · %s\n\n", label, dateRange)
	}
	fmt.Fprintf(sb, "%d requests%s\n", month.Total.Requests, hitRateClause(month.Total))
	fmt.Fprintf(sb, "cached %s · uncached %s · output %s\n\n",
		formatTokens(month.Total.CacheReadTokens),
		formatTokens(uncachedInput(month.Total)),
		formatTokens(month.Total.CompletionTokens))
}

// writeThisWeekSection renders one row per UTC day of the current Monday-to-
// Sunday week, with the bar scaled to the week's busiest day.
func writeThisWeekSection(sb *strings.Builder, rec *usagestats.Recorder, now time.Time) {
	monday := tokenStatsWeekStart(now)
	sunday := monday.AddDate(0, 0, 6)
	fmt.Fprintf(sb, "### This week (Mon %s to Sun %s, UTC)\n\n", shortMonthDay(monday), shortMonthDay(sunday))

	days := rec.Daily(monday, sunday)
	busiest := 0
	weekTotal := 0
	for _, day := range days {
		busiest = max(busiest, day.Total())
		weekTotal += day.Total()
	}

	today := tokenStatsDayStart(now)
	lines := make([]string, 0, len(days)+1)
	for _, day := range days {
		label := fmt.Sprintf("%s %2d", day.Start.Format("Mon"), day.Start.Day())
		if day.Start.After(today) {
			lines = append(lines, label+tokenStatsSpacing+"·")
			continue
		}
		line := fmt.Sprintf("%s%s%s%s%7s", label, tokenStatsSpacing,
			barString(day.Total(), busiest, tokenStatsBarWidth), tokenStatsSpacing, formatTokens(day.Total()))
		if day.Start.Equal(today) {
			line += tokenStatsSpacing + "(today, partial)"
		}
		lines = append(lines, line)
	}
	lines = append(lines, fmt.Sprintf("week: %s", formatTokens(weekTotal)))
	writeCodeBlock(sb, lines)
}

// writePreviousWeeksSection renders one row per earlier Monday-to-Sunday week
// still inside the store's retention window, newest first, with the bar scaled
// to the busiest week shown. It is omitted when no earlier week has data.
func writePreviousWeeksSection(sb *strings.Builder, rec *usagestats.Recorder, now time.Time) {
	retentionStart := tokenStatsDayStart(now.AddDate(0, 0, -tokenStatsRetentionDays))
	currentMonday := tokenStatsWeekStart(now)

	var weeks []usagestats.PeriodTotals
	for _, week := range rec.Weekly(retentionStart, now) {
		if week.Start.Before(currentMonday) {
			weeks = append(weeks, week)
		}
	}
	slices.Reverse(weeks)

	busiest := 0
	for _, week := range weeks {
		busiest = max(busiest, week.Total())
	}
	if busiest == 0 {
		return
	}

	sb.WriteString("### Previous weeks\n\n")
	lines := make([]string, 0, len(weeks))
	for _, week := range weeks {
		// "_2" space-pads the day so every span is the same width.
		span := week.Start.UTC().Format("Jan _2") + " to " + week.Start.AddDate(0, 0, 6).UTC().Format("Jan _2")
		line := fmt.Sprintf("%-16s%s%7s%s%s", span, tokenStatsSpacing, formatTokens(week.Total()),
			tokenStatsSpacing, barString(week.Total(), busiest, tokenStatsBarWidth))
		if week.Start.Before(retentionStart) {
			line += tokenStatsSpacing + "(partial)"
		}
		lines = append(lines, line)
	}
	writeCodeBlock(sb, lines)
}

// writeCodeBlock writes lines inside a fenced block so block glyphs keep their
// alignment through markdown rendering.
func writeCodeBlock(sb *strings.Builder, lines []string) {
	sb.WriteString("```text\n")
	for _, line := range lines {
		sb.WriteString(line)
		sb.WriteString("\n")
	}
	sb.WriteString("```\n\n")
}

// shortMonthDay renders t's UTC month and day of month, e.g. "Oct 20".
func shortMonthDay(t time.Time) string {
	return t.UTC().Format("Jan 2")
}

// tokenStatsDayStart returns the midnight UTC that starts t's UTC day.
func tokenStatsDayStart(t time.Time) time.Time {
	return t.UTC().Truncate(24 * time.Hour)
}

// tokenStatsWeekStart returns the Monday 00:00 UTC that starts t's UTC week.
func tokenStatsWeekStart(t time.Time) time.Time {
	day := tokenStatsDayStart(t)
	return day.AddDate(0, 0, -((int(day.Weekday()) + 6) % 7))
}

// uncachedInput returns the prompt tokens that were not served from cache,
// matching the prompt-side input total.
func uncachedInput(t usagestats.Totals) int {
	return t.InputTokens + t.CacheCreateTokens
}

// hitRateClause renders " · 93.7% of input from cache", or nothing when the hit
// rate is undefined.
func hitRateClause(t usagestats.Totals) string {
	rate, ok := t.HitRate()
	if !ok {
		return ""
	}
	return " · " + formatPercent(rate, ok) + " of input from cache"
}

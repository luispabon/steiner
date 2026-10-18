package tui

import (
	"fmt"
	"math"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/luispabon/steiner/internal/quota"
	"github.com/luispabon/steiner/internal/tui/theme"
)

const (
	usageBarCells     = 24
	usageLabelWidth   = 13
	usageWarnPercent  = 75
	usageErrorPercent = 90
	usageEmptyText    = "No configured providers report subscription usage."
	usageDay          = 24 * time.Hour
	usageWeek         = 7 * usageDay
)

// usageLevel classifies a usage percentage for styling.
type usageLevel int

const (
	usageLevelNormal usageLevel = iota
	usageLevelWarn
	usageLevelError
)

// usageFrame holds every input of one body render. The renderer reads nothing
// else, so equal frames produce equal lines.
type usageFrame struct {
	views        []quota.AccountView
	now          time.Time
	refreshing   bool
	spinnerFrame int
	styles       *theme.Styles
	width        int
}

func usageLevelFor(pct float64) usageLevel {
	switch {
	case pct >= usageErrorPercent:
		return usageLevelError
	case pct >= usageWarnPercent:
		return usageLevelWarn
	default:
		return usageLevelNormal
	}
}

func usageLevelStyle(styles *theme.Styles, pct float64) lipgloss.Style {
	switch usageLevelFor(pct) {
	case usageLevelError:
		return styles.ErrorStyle
	case usageLevelWarn:
		return styles.WarningStyle
	default:
		return styles.SuccessStyle
	}
}

// usageBar returns a usageBarCells wide bar filled in proportion to pct.
func usageBar(pct float64) string {
	filled := int(math.Round(min(max(pct, 0), 100) / 100 * usageBarCells))
	return strings.Repeat("█", filled) + strings.Repeat("░", usageBarCells-filled)
}

// usageMeter returns the bar for pct, styled by pct, followed by text.
func usageMeter(f usageFrame, pct float64, text string) string {
	return usageLevelStyle(f.styles, pct).Render(usageBar(pct) + " " + text)
}

// usageBodyLines renders the overlay body, one entry per terminal row.
func usageBodyLines(f usageFrame) []string {
	if len(f.views) == 0 {
		return []string{f.styles.FgMute.Render(usageEmptyText)}
	}
	var lines []string
	for i, v := range f.views {
		if i > 0 {
			lines = append(lines, f.styles.FgMute.Render(strings.Repeat("─", f.width)))
		}
		lines = append(lines, usageAccountLines(f, v)...)
	}
	return lines
}

// usageAccountLines renders one account: a header row, then its windows,
// budgets, spend and credits when usage was read.
func usageAccountLines(f usageFrame, v quota.AccountView) []string {
	marker := "  "
	if v.Active {
		marker = f.styles.SuccessStyle.Render("●") + " "
	}
	left := marker + f.styles.CardLabel.Render(v.DisplayAlias)
	lines := []string{usageAlignRight(left, usageHeaderRight(f, v), f.width)}
	if v.Latest == nil || v.Unavailable != "" {
		return lines
	}
	snap := v.Latest
	for i, w := range snap.Windows {
		lines = append(lines, usageWindowLine(f, w))
		lines = appendTrend(lines, f, v.WindowProjections, i)
	}
	var spend []string
	for i, b := range snap.Budgets {
		if b.Limit == nil {
			text := fmt.Sprintf("$%.2f", b.Used)
			if b.Label != quota.BudgetLabelSpend {
				text = b.Label + " " + text
			}
			spend = append(spend, text)
			continue
		}
		lines = append(lines, usageBudgetLine(f, b))
		lines = appendTrend(lines, f, v.BudgetProjections, i)
	}
	if len(spend) > 0 {
		lines = append(lines, usageRow(lipgloss.NewStyle().Bold(true).Render("spend"), strings.Join(spend, " · ")))
	}
	if snap.Credits != "" {
		lines = append(lines, usageRow("credits", snap.Credits))
	}
	return lines
}

// appendTrend appends the trend row for projs[i] when that projection is usable.
func appendTrend(lines []string, f usageFrame, projs []quota.Projection, i int) []string {
	if i >= len(projs) || !projs[i].OK {
		return lines
	}
	if projs[i].BeforeReset {
		text := fmt.Sprintf("  └─ trend: empty by %s (before reset) ", usageResetTime(projs[i].EmptyAt, f.now))
		return append(lines, f.styles.WarningStyle.Render(text)+f.styles.ErrorStyle.Render("✗"))
	}
	return append(lines, "  └─ trend: lasts past reset "+f.styles.SuccessStyle.Render("✓"))
}

// usageRow renders a label padded to the label column, followed by value.
// A label at or past the column still gets one space before value.
func usageRow(label, value string) string {
	pad := max(usageLabelWidth-lipgloss.Width(label), 1)
	return "  " + label + strings.Repeat(" ", pad) + value
}

func usagePeriodRow(label, value string) string {
	return usageRow(lipgloss.NewStyle().Bold(true).Render(label), value)
}

func usageWindowLine(f usageFrame, w quota.Window) string {
	line := usagePeriodRow(w.Label, usageMeter(f, w.UsedPercent, fmt.Sprintf("%3.0f%%", w.UsedPercent)))
	if !w.ResetsAt.IsZero() {
		line += "  " + usageResetText(f, w.ResetsAt)
	}
	return line
}

func usageBudgetLine(f usageFrame, b quota.Budget) string {
	pct := 0.0
	if *b.Limit > 0 {
		pct = b.Used / *b.Limit * 100
	}
	line := usagePeriodRow(b.Label, usageMeter(f, pct, fmt.Sprintf("$%.2f / $%.2f", b.Used, *b.Limit)))
	if b.Period != "" {
		line += "  " + b.Period
	}
	if b.ResetsAt != nil {
		line += "  " + usageResetText(f, *b.ResetsAt)
	}
	return line
}

// usageHeaderRight renders the right side of an account header: the static
// unavailable reason, or the plan and freshness.
func usageHeaderRight(f usageFrame, v quota.AccountView) string {
	if v.Unavailable != "" {
		return f.styles.FgMute.Render(v.Unavailable)
	}
	var parts []string
	if v.Latest != nil && v.Latest.Plan != "" {
		parts = append(parts, f.styles.CardLabel.Render(v.Latest.Plan))
	}
	if fresh := usageFreshness(f, v); fresh != "" {
		parts = append(parts, fresh)
	}
	return strings.Join(parts, " · ")
}

// usageFreshness renders the refresh state, the age of the latest snapshot and
// any fetch error.
func usageFreshness(f usageFrame, v quota.AccountView) string {
	var parts []string
	switch {
	case v.Refreshing || f.refreshing:
		spinner := spinnerFrames[f.spinnerFrame%len(spinnerFrames)]
		parts = append(parts, f.styles.Accent.Render(spinner)+" refreshing…")
	case v.Latest != nil:
		parts = append(parts, "updated "+usageRelative(f.now.Sub(v.Latest.FetchedAt))+" ago")
	}
	if v.Err != "" {
		parts = append(parts, f.styles.ErrorStyle.Render("unavailable: "+v.Err))
	}
	return strings.Join(parts, " · ")
}

// usageResetText renders a reset time with the time left until it.
func usageResetText(f usageFrame, t time.Time) string {
	return usageResetTime(t, f.now) + " (" + usageRelative(t.Sub(f.now)) + ")"
}

// usageResetTime formats t in now's location: 15:04 on the same day, Mon 15:04
// within seven days, and 2 Jan beyond that.
func usageResetTime(t, now time.Time) string {
	t = t.In(now.Location())
	y1, m1, d1 := t.Date()
	y2, m2, d2 := now.Date()
	switch {
	case y1 == y2 && m1 == m2 && d1 == d2:
		return t.Format("15:04")
	case t.Before(now.Add(usageWeek)):
		return t.Format("Mon 15:04")
	default:
		return t.Format("2 Jan")
	}
}

// usageRelative formats d concisely: 4d, 2h05m, 15m, or <1m under a minute.
func usageRelative(d time.Duration) string {
	switch {
	case d >= usageDay:
		return fmt.Sprintf("%dd", d/usageDay)
	case d >= time.Hour:
		return fmt.Sprintf("%dh%02dm", d/time.Hour, d%time.Hour/time.Minute)
	case d >= time.Minute:
		return fmt.Sprintf("%dm", d/time.Minute)
	default:
		return "<1m"
	}
}

// usageAlignRight places right at the end of width. When both sides cannot
// fit, right is truncated first and then left, so the row never exceeds width.
// A width of zero or less joins the non-empty sides with one space.
func usageAlignRight(left, right string, width int) string {
	if width <= 0 {
		if right == "" {
			return left
		}
		return left + " " + right
	}
	if right == "" {
		return usageFit(left, width)
	}
	left = usageFit(left, width-2)
	room := width - lipgloss.Width(left) - 1
	if room <= 0 {
		return left
	}
	right = usageFit(right, room)
	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	return left + strings.Repeat(" ", gap) + right
}

// usageFit truncates s to width cells with an ellipsis, closing any open style
// when it cuts.
func usageFit(s string, width int) string {
	if lipgloss.Width(s) <= width {
		return s
	}
	return ansi.Truncate(s, max(width, 0), "…") + "\x1b[0m"
}

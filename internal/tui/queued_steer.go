package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/luispabon/steiner/internal/agent"
)

// queuedSteerMaxLines caps the number of message-text rows shown in the
// queued-message box; anything beyond that collapses into one overflow row.
const queuedSteerMaxLines = 3

// queuedSteerHeight returns the number of terminal rows the queued-message
// box occupies at the given width, or 0 when nothing is queued. layout and
// relayoutInput must subtract exactly this.
func (m *Model) queuedSteerHeight(width int) int {
	rendered := m.renderQueuedSteerBox(width)
	if rendered == "" {
		return 0
	}
	return lipgloss.Height(rendered)
}

// renderQueuedSteerBox renders the pinned queued-message box, or "" when
// nothing is queued. queuedSteerHeight derives its answer directly from this
// render's output, so the two can never disagree.
func (m *Model) renderQueuedSteerBox(width int) string {
	if m.steers == nil {
		return ""
	}
	msgs := m.steers.Snapshot()
	if len(msgs) == 0 {
		return ""
	}

	if width < 14 {
		text := strings.ReplaceAll(msgs[0].Text, "\n", " ")
		row := lipgloss.NewStyle().MaxWidth(max(1, width)).Render("queued: " + text)
		return m.styles.FgDim.Render(row)
	}

	textWidth := width - 4
	if textWidth < 1 {
		textWidth = 1
	}
	lines := wrapQueuedSteerLines(msgs, textWidth)

	textStyle := lipgloss.NewStyle().Italic(true).Foreground(m.styles.FgDim.GetForeground())
	styledContent := textStyle.Render(strings.Join(lines, "\n"))

	boxStyle := lipgloss.NewStyle().
		Background(lipgloss.Color(m.styles.Palette.ContentBG)).
		Padding(0, 1).
		Border(lipgloss.NormalBorder()).
		BorderForeground(m.styles.FgDim.GetForeground()).
		Width(width)

	boxed := boxStyle.Render(styledContent)

	boxLines := strings.Split(boxed, "\n")
	if len(boxLines) > 0 {
		interiorWidth := lipgloss.Width(boxLines[0]) - 2
		if interiorWidth < 0 {
			interiorWidth = 0
		}
		title := queuedSteerTitle(len(msgs))
		titleWidth := lipgloss.Width(title)
		fillCount := interiorWidth - titleWidth
		if fillCount < 0 {
			fillCount = 0
		}
		titleLine := "╭" + title + strings.Repeat("─", fillCount) + "╮"
		titleStyle := lipgloss.NewStyle().
			Background(lipgloss.Color(m.styles.Palette.ContentBG)).
			Foreground(m.styles.FgDim.GetForeground())
		boxLines[0] = titleStyle.Render(titleLine)
	}

	return strings.Join(boxLines, "\n")
}

// queuedSteerPlaceholder builds the composer placeholder shown while a run is
// busy and one or more steer messages are queued.
func queuedSteerPlaceholder(count int) string {
	noun := "message"
	if count != 1 {
		noun = "messages"
	}
	return fmt.Sprintf("%d %s queued — ctrl+g to edit, esc to interrupt", count, noun)
}

func queuedSteerTitle(count int) string {
	if count > 1 {
		return fmt.Sprintf("─ queued (%d) ─", count)
	}
	return "─ queued ─"
}

// wrapQueuedSteerLines wraps every queued message to textWidth and joins them
// with a blank separator row, capping the result at queuedSteerMaxLines
// terminal rows of text. Each source line can itself wrap to several
// terminal rows at textWidth, so the cap is applied to the flattened rows,
// not to the source lines. When wrapping overflows that budget, it keeps the
// first queuedSteerMaxLines rows and appends one overflow row: "+N more",
// where N is the number of hidden rows that carry actual text — blank rows
// (the separator between messages, or a blank line embedded in merged text)
// don't count, since they're not content the user is missing. Counting text
// rows rather than messages keeps this correct after ctrl+g take-back:
// MergeSteers can fold several queued messages into one, and a row-based
// count still reports the hidden content accurately once that boundary is
// gone.
func wrapQueuedSteerLines(msgs []agent.SteerMessage, textWidth int) []string {
	lineStyle := lipgloss.NewStyle().Width(textWidth)
	var rows []string
	for i, msg := range msgs {
		if i > 0 {
			rows = append(rows, "")
		}
		text := strings.TrimRight(msg.Text, "\n")
		for _, line := range strings.Split(text, "\n") {
			rendered := lineStyle.Render(line)
			rows = append(rows, strings.Split(rendered, "\n")...)
		}
	}

	if len(rows) <= queuedSteerMaxLines {
		return rows
	}

	lines := append([]string(nil), rows[:queuedSteerMaxLines]...)
	hidden := 0
	for _, r := range rows[queuedSteerMaxLines:] {
		if strings.TrimSpace(r) != "" {
			hidden++
		}
	}
	overflow := "…"
	if hidden > 0 {
		overflow = fmt.Sprintf("+%d more", hidden)
	}
	return append(lines, overflow)
}

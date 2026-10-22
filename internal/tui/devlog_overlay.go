package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/luispabon/steiner/internal/tui/theme"
)

// devLogOverlayChromeLines accounts for the header, dividers and footer rows
// that surround the scrollable body when sizing the visible window from
// terminal height.
const devLogOverlayChromeLines = 6

// devLogOverlay renders the Alt+E and /errors overlay listing recorded
// developer error log entries. It opens at the newest entry and keeps following
// the tail until the user scrolls away from it.
type devLogOverlay struct {
	OverlayShell
	scrollOffset int
	follow       bool
	styles       *theme.Styles
}

func newDevLogOverlay(styles *theme.Styles) devLogOverlay {
	return devLogOverlay{
		OverlayShell: OverlayShell{}.WithPreferredWidth(80),
		styles:       styles,
	}
}

// openDevLogOverlay opens the overlay at the tail and marks all entries seen.
func (m *Model) openDevLogOverlay() {
	m.devLogOverlay.OverlayShell = m.devLogOverlay.openShell().WithDimensions(m.width, m.height)
	m.devLogOverlay.scrollOffset = 0
	m.devLogOverlay.follow = true
	m.devLog.unseen = 0
	m.syncDevLogStatus()
}

func (o devLogOverlay) Close() devLogOverlay {
	o.OverlayShell = o.closeShell()
	return o
}

// buildLines flattens the entries into the plain lines View renders and
// scrolling operates on. Entries are separated by a blank line.
func (o devLogOverlay) buildLines(entries []devLogEntry) []string {
	if len(entries) == 0 {
		return []string{o.styles.FgMute.Render("No internal errors recorded.")}
	}
	wrapStyle := lipgloss.NewStyle().Width(max(o.InnerWidth(), 1))
	var lines []string
	for i, e := range entries {
		if i > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, o.severityStyle(e.Severity).Render(e.header()))
		lines = append(lines, strings.Split(wrapStyle.Render(e.Message), "\n")...)
	}
	return lines
}

// severityStyle returns the header style for a severity.
func (o devLogOverlay) severityStyle(sev devLogSeverity) lipgloss.Style {
	switch sev {
	case devLogError:
		return o.styles.ErrorStyle
	case devLogWarning:
		return o.styles.WarningStyle
	default:
		return o.styles.FgMute
	}
}

// header returns the plain header line for an entry: time, severity and the
// identifying fields that are set.
func (e devLogEntry) header() string {
	fields := []string{e.at.Format("15:04:05"), e.Severity.String()}
	for _, v := range []string{e.Source, e.EventType, e.AgentType} {
		if v != "" {
			fields = append(fields, v)
		}
	}
	if e.CallID != "" {
		fields = append(fields, "call="+e.CallID)
	}
	return strings.Join(fields, " ")
}

// devLogText returns the plain-text form of the entries for the clipboard. Each
// entry is its header line followed by its message, and entries are separated by
// a blank line.
func devLogText(entries []devLogEntry) string {
	blocks := make([]string, 0, len(entries))
	for _, e := range entries {
		blocks = append(blocks, e.header()+"\n"+e.Message)
	}
	return strings.Join(blocks, "\n\n")
}

// visibleHeight returns the number of body lines that fit the terminal height.
func (o devLogOverlay) visibleHeight() int {
	return o.bodyVisibleHeight(devLogOverlayChromeLines)
}

// offset returns the scroll offset to render: the tail while following,
// otherwise the stored offset clamped to the valid range.
func (o devLogOverlay) offset(total, visible int) int {
	maxOffset := max(total-visible, 0)
	if o.follow {
		return maxOffset
	}
	return min(o.scrollOffset, maxOffset)
}

func (o devLogOverlay) View(entries []devLogEntry) string {
	if !o.IsOpen() {
		return ""
	}

	lines := o.buildLines(entries)
	visible := o.visibleHeight()
	start := o.offset(len(lines), visible)
	end := min(len(lines), start+visible)
	body := lipgloss.JoinVertical(lipgloss.Left, lines[start:end]...)

	footer := o.RenderFooter(fmt.Sprintf("↑/↓ scroll · y copy · esc to close  |  %d/%d", end, len(lines)))

	full := lipgloss.JoinVertical(lipgloss.Left,
		"Internal errors",
		o.Divider(),
		body,
		o.Divider(),
		footer,
	)
	return o.RenderWithBg(o.styles.PaletteOverlay, full, theme.BgElev)
}

// Update handles scroll, close and copy keys. Alt+E closes the overlay, as it
// opens it. Copy returns a command that writes every entry to the clipboard.
func (o devLogOverlay) Update(msg tea.Msg, entries []devLogEntry) (devLogOverlay, tea.Cmd) {
	if !o.IsOpen() {
		return o, nil
	}
	if keyMsg, ok := msg.(tea.KeyPressMsg); ok {
		if keyMsg.Text == "y" {
			return o, copyToClipboard(devLogText(entries))
		}
		if isAlt(keyMsg, 'e') {
			return o.Close(), nil
		}
	}

	total := len(o.buildLines(entries))
	visible := o.visibleHeight()
	result := o.handleScrollKey(msg, o.offset(total, visible), visible, total)
	if !result.handled {
		return o, nil
	}
	if result.close {
		return o.Close(), nil
	}
	o.scrollOffset = result.offset
	o.follow = result.offset >= max(total-visible, 0)
	return o, nil
}

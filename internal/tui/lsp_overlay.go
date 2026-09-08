package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/luispabon/steiner/internal/tui/theme"
)

// lspOverlayChromeLines accounts for the header, dividers and footer rows
// that surround the scrollable body when sizing the visible window from
// terminal height.
const lspOverlayChromeLines = 6

// lspOverlayTimeFormat is the fixed absolute-time format used for
// started/last-used timestamps.
const lspOverlayTimeFormat = "2006-01-02 15:04:05"

// lspOverlay renders the /lsp server status overlay: a scrollable list of
// every LSP session (server+root pair) with its status, root and timing.
// Unlike the sidebar, sessions are not deduped by server name here — root is
// the useful distinguisher between sessions of the same server.
type lspOverlay struct {
	OverlayShell
	sessions     []LSPServerStatus
	lspEnabled   bool
	lines        []string
	scrollOffset int
	styles       *theme.Styles
}

func newLSPOverlay(styles *theme.Styles) lspOverlay {
	return lspOverlay{
		OverlayShell: OverlayShell{}.WithPreferredWidth(70),
		styles:       styles,
	}
}

// Open returns a copy of the overlay in the open state, with the session
// list sorted and flattened into display lines.
func (o lspOverlay) Open(sessions []LSPServerStatus, enabled bool) lspOverlay {
	o.OverlayShell = o.openShell()
	o.sessions = sortLSPServerStatuses(sessions)
	o.lspEnabled = enabled
	o.scrollOffset = 0
	o.lines = o.buildLines()
	return o
}

func (o lspOverlay) Close() lspOverlay {
	o.OverlayShell = o.closeShell()
	return o
}

// buildLines flattens the session list into the plain lines View renders and
// scrolling operates on.
func (o lspOverlay) buildLines() []string {
	var lines []string
	if !o.lspEnabled {
		lines = append(lines, o.styles.FgMute.Render("LSP is disabled in config."))
	}
	if len(o.sessions) == 0 {
		lines = append(lines, "no LSP servers configured")
		return lines
	}
	for i, sess := range o.sessions {
		if i > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, o.renderServerLines(sess)...)
	}
	return lines
}

// renderServerLines renders the status line for one session, plus any
// indented error text (failed) or timing info (ready/stopped) beneath it.
func (o lspOverlay) renderServerLines(sess LSPServerStatus) []string {
	bulletStyle := o.styles.FgMute
	switch sess.Status {
	case "ready":
		bulletStyle = o.styles.SuccessStyle
	case "failed":
		bulletStyle = o.styles.ErrorStyle
	}

	header := fmt.Sprintf("%s %s  %s  (%s)", bulletStyle.Render("●"), sess.Name, lspStateDisplayLabel(sess.Status), sess.Root)
	lines := []string{header}

	indentWidth := max(o.InnerWidth()-2, 1)
	switch sess.Status {
	case "failed":
		errText := sess.Error
		if errText == "" {
			errText = "unknown error"
		}
		wrapped := lipgloss.NewStyle().Width(indentWidth).Render(errText)
		for _, l := range strings.Split(wrapped, "\n") {
			lines = append(lines, "  "+o.styles.ErrorStyle.Render(l))
		}
	case "ready", "stopped":
		if !sess.StartedAt.IsZero() {
			lines = append(lines, "  "+o.styles.FgMute.Render("started "+sess.StartedAt.Format(lspOverlayTimeFormat)))
		}
		if !sess.LastUsed.IsZero() {
			lines = append(lines, "  "+o.styles.FgMute.Render("last used "+sess.LastUsed.Format(lspOverlayTimeFormat)))
		}
	}
	return lines
}

// lspStateDisplayLabel maps a raw status string to its display label,
// passing unknown values through unchanged (e.g. a future "not started").
func lspStateDisplayLabel(status string) string {
	switch status {
	case "declared":
		return "Declared"
	case "starting":
		return "Starting"
	case "ready":
		return "Ready"
	case "failed":
		return "Failed"
	case "stopped":
		return "Stopped"
	case "not started":
		return "Not started"
	case "disabled":
		return "Disabled"
	default:
		return status
	}
}

// visibleHeight returns the number of body lines that fit in the current
// terminal height.
func (o lspOverlay) visibleHeight() int {
	h := o.height - lspOverlayChromeLines
	if h < 3 {
		h = 3
	}
	if h > 30 {
		h = 30
	}
	return h
}

func (o lspOverlay) maxScrollOffset() int {
	offset := len(o.lines) - o.visibleHeight()
	if offset < 0 {
		offset = 0
	}
	return offset
}

func (o lspOverlay) scroll(delta int) lspOverlay {
	o.scrollOffset += delta
	if o.scrollOffset < 0 {
		o.scrollOffset = 0
	}
	if maxOffset := o.maxScrollOffset(); o.scrollOffset > maxOffset {
		o.scrollOffset = maxOffset
	}
	return o
}

func (o lspOverlay) scrollTo(offset int) lspOverlay {
	o.scrollOffset = 0
	return o.scroll(offset)
}

func (o lspOverlay) View() string {
	if !o.IsOpen() {
		return ""
	}

	header := "LSP Servers"
	divider := o.Divider()

	visible := o.visibleHeight()
	total := len(o.lines)
	end := min(total, o.scrollOffset+visible)
	body := lipgloss.JoinVertical(lipgloss.Left, o.lines[o.scrollOffset:end]...)

	footerDivider := o.Divider()
	footer := o.RenderFooter(fmt.Sprintf("↑/↓ scroll · esc to close  |  %d/%d", end, total))

	full := lipgloss.JoinVertical(lipgloss.Left,
		header,
		divider,
		body,
		footerDivider,
		footer,
	)
	return o.RenderWithBg(o.styles.PaletteOverlay, full, theme.BgElev)
}

//nolint:dupl // same scroll/key-handling as mcpOverlay.Update; types differ
func (o lspOverlay) Update(msg tea.Msg) (lspOverlay, tea.Cmd) {
	if !o.IsOpen() {
		return o, nil
	}
	keyMsg, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return o, nil
	}
	switch keyMsg.Code {
	case tea.KeyEsc, tea.KeyEnter:
		return o.Close(), nil
	case tea.KeyUp:
		return o.scroll(-1), nil
	case tea.KeyDown:
		return o.scroll(1), nil
	case tea.KeyPgUp:
		return o.scroll(-o.visibleHeight()), nil
	case tea.KeyPgDown:
		return o.scroll(o.visibleHeight()), nil
	case tea.KeyHome:
		return o.scrollTo(0), nil
	case tea.KeyEnd:
		return o.scrollTo(len(o.lines)), nil
	}
	switch keyMsg.Text {
	case "k":
		return o.scroll(-1), nil
	case "j":
		return o.scroll(1), nil
	}
	return o, nil
}

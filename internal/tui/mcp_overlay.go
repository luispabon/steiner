package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/luispabon/steiner/internal/tui/theme"
)

// mcpOverlayChromeLines accounts for the header, dividers and footer rows
// that surround the scrollable body when sizing the visible window from
// terminal height.
const mcpOverlayChromeLines = 6

// mcpOverlay renders the /mcp server status overlay: a scrollable list of
// every configured MCP server with its connection state, transport and
// tool names.
type mcpOverlay struct {
	OverlayShell
	servers      []MCPServerStatus
	mcpEnabled   bool
	lines        []string
	scrollOffset int
	styles       theme.Styles
}

func newMCPOverlay(styles theme.Styles) mcpOverlay {
	return mcpOverlay{
		OverlayShell: OverlayShell{}.WithPreferredWidth(70),
		styles:       styles,
	}
}

// Open returns a copy of the overlay in the open state, with the server
// list flattened into display lines.
func (o mcpOverlay) Open(servers []MCPServerStatus, enabled bool) mcpOverlay {
	o.OverlayShell = o.openShell()
	o.servers = servers
	o.mcpEnabled = enabled
	o.scrollOffset = 0
	o.lines = o.buildLines()
	return o
}

func (o mcpOverlay) Close() mcpOverlay {
	o.OverlayShell = o.closeShell()
	return o
}

// buildLines flattens the server list into the plain lines View renders and
// scrolling operates on.
func (o mcpOverlay) buildLines() []string {
	var lines []string
	if !o.mcpEnabled {
		lines = append(lines, o.styles.FgMute.Render("MCP is disabled in config."))
	}
	if len(o.servers) == 0 {
		lines = append(lines, "no MCP servers configured")
		return lines
	}
	for i, srv := range o.servers {
		if i > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, o.renderServerLines(srv)...)
	}
	return lines
}

// renderServerLines renders the status line for one server, plus any
// indented error text (failed) or tool list (connected) beneath it.
func (o mcpOverlay) renderServerLines(srv MCPServerStatus) []string {
	bulletStyle := o.styles.FgMute
	switch srv.State {
	case "connected":
		bulletStyle = o.styles.SuccessStyle
	case "failed":
		bulletStyle = o.styles.ErrorStyle
	}

	header := fmt.Sprintf("%s %s  %s  (%s)", bulletStyle.Render("●"), srv.Name, stateDisplayLabel(srv.State), srv.Transport)
	lines := []string{header}

	indentWidth := max(o.InnerWidth()-2, 1)
	switch srv.State {
	case "failed":
		errText := srv.Error
		if errText == "" {
			errText = "unknown error"
		}
		wrapped := lipgloss.NewStyle().Width(indentWidth).Render(errText)
		for _, l := range strings.Split(wrapped, "\n") {
			lines = append(lines, "  "+o.styles.ErrorStyle.Render(l))
		}
	case "connected":
		if len(srv.Tools) == 0 {
			lines = append(lines, "  "+o.styles.FgMute.Render("no tools advertised"))
			break
		}
		label := fmt.Sprintf("%d tool", len(srv.Tools))
		if len(srv.Tools) != 1 {
			label += "s"
		}
		label += ": " + strings.Join(srv.Tools, ", ")
		wrapped := lipgloss.NewStyle().Width(indentWidth).Render(label)
		for _, l := range strings.Split(wrapped, "\n") {
			lines = append(lines, "  "+l)
		}
	}
	return lines
}

func stateDisplayLabel(state string) string {
	switch state {
	case "connected":
		return "Connected"
	case "failed":
		return "Failed"
	case "disabled":
		return "Disabled"
	default:
		return state
	}
}

// visibleHeight returns the number of body lines that fit in the current
// terminal height.
func (o mcpOverlay) visibleHeight() int {
	h := o.height - mcpOverlayChromeLines
	if h < 3 {
		h = 3
	}
	if h > 30 {
		h = 30
	}
	return h
}

func (o mcpOverlay) maxScrollOffset() int {
	offset := len(o.lines) - o.visibleHeight()
	if offset < 0 {
		offset = 0
	}
	return offset
}

func (o mcpOverlay) scroll(delta int) mcpOverlay {
	o.scrollOffset += delta
	if o.scrollOffset < 0 {
		o.scrollOffset = 0
	}
	if maxOffset := o.maxScrollOffset(); o.scrollOffset > maxOffset {
		o.scrollOffset = maxOffset
	}
	return o
}

func (o mcpOverlay) scrollTo(offset int) mcpOverlay {
	o.scrollOffset = 0
	return o.scroll(offset)
}

func (o mcpOverlay) View() string {
	if !o.IsOpen() {
		return ""
	}

	header := "MCP Servers"
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

func (o mcpOverlay) Update(msg tea.Msg) (mcpOverlay, tea.Cmd) {
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

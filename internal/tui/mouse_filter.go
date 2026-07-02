package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

// Internal mouse message types for v2 dispatch via View.OnMouse.
type mouseClickMsg struct {
	x, y       int
	clickCount int
}

type mouseMotionMsg struct {
	x, y int
}

type mouseReleaseMsg struct {
	x, y int
}

type mouseWheelMsg struct {
	direction string // "up" or "down"
}

// classifyMouse implements View.OnMouse handler for v2 mouse events.
// It classifies MouseClickMsg, MouseMotionMsg, MouseReleaseMsg, and MouseWheelMsg,
// extracting coordinates and emitting internal messages for Update to apply state mutations.
// Motion events are only forwarded while the left button is held so ordinary
// pointer movement does not create update churn.
func classifyMouse(msg tea.MouseMsg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.MouseClickMsg:
		mouse := msg.Mouse()
		if mouse.Button == tea.MouseLeft {
			return func() tea.Msg { return mouseClickMsg{x: mouse.X, y: mouse.Y} }
		}

	case tea.MouseWheelMsg:
		mouse := msg.Mouse()
		switch mouse.Button {
		case tea.MouseWheelUp:
			return func() tea.Msg { return mouseWheelMsg{direction: "up"} }
		case tea.MouseWheelDown:
			return func() tea.Msg { return mouseWheelMsg{direction: "down"} }
		}

	case tea.MouseMotionMsg:
		mouse := msg.Mouse()
		if mouse.Button == tea.MouseLeft {
			return func() tea.Msg { return mouseMotionMsg{x: mouse.X, y: mouse.Y} }
		}

	case tea.MouseReleaseMsg:
		mouse := msg.Mouse()
		if mouse.Button == tea.MouseLeft {
			return func() tea.Msg { return mouseReleaseMsg{x: mouse.X, y: mouse.Y} }
		}
	}

	return nil
}

func shouldIgnoreLeakedMouseRunes(msg tea.KeyPressMsg, recentWheel bool) bool {
	if msg.Text == "" {
		return false
	}
	fragment := msg.Text
	return isLeakedMouseFragment(fragment) || (recentWheel && isLeakedMousePrefixFragment(fragment))
}

func isLeakedMouseFragment(fragment string) bool {
	if fragment == "" || strings.TrimSpace(fragment) != fragment {
		return false
	}

	fragment, suffix := stripMouseSuffix(fragment)
	fragment, prefix, ok := stripMousePrefix(fragment)
	if !ok || fragment == "" || !mouseDigitsAndSeparatorsOnly(fragment) {
		return false
	}
	parts := strings.Split(fragment, ";")
	if !mousePartsValid(parts) {
		return false
	}
	return mouseFragmentShapeAllowed(prefix, suffix, len(parts))
}

func stripMouseSuffix(fragment string) (string, string) {
	if strings.HasSuffix(fragment, "M") || strings.HasSuffix(fragment, "m") {
		return fragment[:len(fragment)-1], fragment[len(fragment)-1:]
	}
	return fragment, ""
}

func stripMousePrefix(fragment string) (string, string, bool) {
	switch {
	case strings.HasPrefix(fragment, "[<"):
		return fragment[2:], "[<", true
	case strings.HasPrefix(fragment, "<"):
		return fragment[1:], "<", true
	case strings.HasPrefix(fragment, "["):
		return "", "", false
	default:
		return fragment, "", true
	}
}

func mouseDigitsAndSeparatorsOnly(fragment string) bool {
	for _, r := range fragment {
		if (r < '0' || r > '9') && r != ';' {
			return false
		}
	}
	return true
}

func mousePartsValid(parts []string) bool {
	for _, part := range parts {
		if part == "" {
			return false
		}
	}
	return true
}

func mouseFragmentShapeAllowed(prefix, suffix string, partCount int) bool {
	switch prefix {
	case "[<", "<":
		switch partCount {
		case 1, 2:
			return suffix == ""
		case 3:
			return suffix == "M" || suffix == "m"
		default:
			return false
		}
	case "":
		return partCount == 3 && (suffix == "M" || suffix == "m")
	default:
		return false
	}
}

func isLeakedMousePrefixFragment(fragment string) bool {
	if fragment == "" || strings.TrimSpace(fragment) != fragment {
		return false
	}
	switch {
	case strings.Trim(fragment, "[") == "":
		return true
	case fragment == "[<":
		return true
	default:
		return false
	}
}

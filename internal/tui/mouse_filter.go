package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

func shouldIgnoreLeakedMouseRunes(msg tea.KeyMsg, recentWheel bool) bool {
	if msg.Type != tea.KeyRunes || len(msg.Runes) == 0 {
		return false
	}
	fragment := string(msg.Runes)
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

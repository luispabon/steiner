package tui

import "strings"

type inputAction struct {
	handled      bool
	quit         bool
	clear        bool
	listSkills   bool
	submit       string
	toggleSkill  string
	toggleEnable bool
}

func parseInput(value string, enabledSkills map[string]bool) inputAction {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return inputAction{}
	}
	if !strings.HasPrefix(trimmed, "/") {
		return inputAction{submit: trimmed}
	}

	switch {
	case trimmed == "/exit":
		return inputAction{handled: true, quit: true}
	case trimmed == "/clear":
		return inputAction{handled: true, clear: true}
	case trimmed == "/skills":
		return inputAction{handled: true, listSkills: true}
	case strings.HasPrefix(trimmed, "/skill "):
		name := strings.TrimSpace(strings.TrimPrefix(trimmed, "/skill "))
		enable := true
		switch {
		case strings.HasPrefix(name, "+"):
			name = strings.TrimSpace(strings.TrimPrefix(name, "+"))
		case strings.HasPrefix(name, "-"):
			name = strings.TrimSpace(strings.TrimPrefix(name, "-"))
			enable = false
		default:
			enable = !enabledSkills[name]
		}
		if name == "" {
			return inputAction{}
		}
		return inputAction{
			handled:      true,
			toggleSkill:  name,
			toggleEnable: enable,
		}
	default:
		return inputAction{submit: trimmed}
	}
}

package tui

import (
	"strings"
)

type inputAction struct {
	handled        bool
	quit           bool
	clear          bool
	compaction     bool
	inspectContext bool
	listSkills     bool
	listModels     bool
	submit         string
	toggleSkill    string
	toggleEnable   bool
	switchModel    string
	setAccent      string
	toggleThinking bool
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
	case trimmed == "/compact":
		return inputAction{handled: true, compaction: true}
	case trimmed == "/context":
		return inputAction{handled: true, inspectContext: true}
	case trimmed == "/skills":
		return inputAction{handled: true, listSkills: true}
	case trimmed == "/models":
		return inputAction{handled: true, listModels: true}
	case trimmed == "/thinking":
		return inputAction{handled: true, toggleThinking: true}
	case strings.HasPrefix(trimmed, "/accent "):
		preset := strings.TrimSpace(strings.TrimPrefix(trimmed, "/accent "))
		if preset == "" {
			return inputAction{}
		}
		return inputAction{handled: true, setAccent: preset}
	case strings.HasPrefix(trimmed, "/model "):
		name := strings.TrimSpace(strings.TrimPrefix(trimmed, "/model "))
		if name == "" {
			return inputAction{}
		}
		return inputAction{handled: true, switchModel: name}
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

// buildCompletionCandidates returns all candidates matching the current input prefix.
// Candidates are built-in slash commands plus "/skill <name>" and "/model <name>" variants.
func buildCompletionCandidates(prefix string, skillNames []string, modelNames []string) []string {
	base := []string{"/exit", "/clear", "/compact", "/context", "/skills", "/skill", "/models", "/model", "/thinking",
		"/accent amber", "/accent rose", "/accent magenta", "/accent violet", "/accent cyan", "/accent mint", "/accent lime"}
	for _, name := range skillNames {
		base = append(base, "/skill +"+name, "/skill -"+name, "/skill "+name)
	}
	for _, name := range modelNames {
		base = append(base, "/model "+name)
	}
	var matches []string
	for _, c := range base {
		if strings.HasPrefix(c, prefix) {
			matches = append(matches, c)
		}
	}
	return matches
}

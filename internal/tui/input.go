package tui

import (
	"strings"
)

type inputAction struct {
	quit                 bool
	clear                bool
	compaction           bool
	inspectConfig        bool
	listSkills           bool
	listFiles            bool
	listFilesPath        string
	submit               string
	toggleSkill          string
	toggleEnable         bool
	switchModel          string
	setAccent            string
	toggleThinking       bool
	requestSessionPicker bool
	forkSession          bool
	openModelPicker      bool
	invokeSkill          string // skill name for direct invocation
	invokeSkillArgs      string // optional args to pass with skill invocation
}

func parseInput(value string) inputAction {
	return parseInputWithSkills(value, nil, nil)
}

func parseInputWithSkills(value string, enabledSkills map[string]bool, skillNames []string) inputAction {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return inputAction{}
	}
	if !strings.HasPrefix(trimmed, "/") {
		return inputAction{submit: trimmed}
	}

	// Try built-in commands first
	if action, ok := parseBuiltinCommand(trimmed); ok {
		return action
	}

	if action, ok := parseArgumentCommand(trimmed, enabledSkills); ok {
		return action
	}

	if action, ok := parseSkillInvocation(trimmed, skillNames); ok {
		return action
	}

	return inputAction{submit: trimmed}
}

// parseBuiltinCommand handles simple single-word slash commands.
func parseBuiltinCommand(trimmed string) (inputAction, bool) {
	switch trimmed {
	case "/exit":
		return inputAction{quit: true}, true
	case "/clear":
		return inputAction{clear: true}, true
	case "/compact":
		return inputAction{compaction: true}, true
	case "/config":
		return inputAction{inspectConfig: true}, true
	case "/fork":
		return inputAction{forkSession: true}, true
	case "/resume":
		return inputAction{requestSessionPicker: true}, true
	case "/skills":
		return inputAction{listSkills: true}, true
	case "/model":
		return inputAction{openModelPicker: true}, true
	case "/thinking":
		return inputAction{toggleThinking: true}, true
	case "/ls":
		return inputAction{listFiles: true}, true
	default:
		return inputAction{}, false
	}
}

// parseSkillInvocation checks if the input is a direct skill invocation.
func parseSkillInvocation(trimmed string, skillNames []string) (inputAction, bool) {
	for _, skillName := range skillNames {
		command := "/" + skillName
		switch {
		case trimmed == command:
			return inputAction{invokeSkill: skillName}, true
		case strings.HasPrefix(trimmed, command+" "):
			rest := strings.TrimSpace(strings.TrimPrefix(trimmed, command))
			return inputAction{
				invokeSkill:     skillName,
				invokeSkillArgs: rest,
			}, true
		}
	}
	return inputAction{}, false
}

func parseArgumentCommand(trimmed string, enabledSkills map[string]bool) (inputAction, bool) {
	switch {
	case strings.HasPrefix(trimmed, "/accent "):
		preset := strings.TrimSpace(strings.TrimPrefix(trimmed, "/accent "))
		if preset == "" {
			return inputAction{}, true
		}
		return inputAction{setAccent: preset}, true
	case strings.HasPrefix(trimmed, "/ls "):
		path := strings.TrimSpace(strings.TrimPrefix(trimmed, "/ls "))
		if path == "" {
			return inputAction{}, true
		}
		return inputAction{listFiles: true, listFilesPath: path}, true
	case strings.HasPrefix(trimmed, "/model "):
		name := strings.TrimSpace(strings.TrimPrefix(trimmed, "/model "))
		if name == "" {
			return inputAction{}, true
		}
		return inputAction{switchModel: name}, true
	case strings.HasPrefix(trimmed, "/skill "):
		return parseSkillCommand(strings.TrimSpace(strings.TrimPrefix(trimmed, "/skill ")), enabledSkills), true
	default:
		return inputAction{}, false
	}
}

func parseSkillCommand(name string, enabledSkills map[string]bool) inputAction {
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
		toggleSkill:  name,
		toggleEnable: enable,
	}
}

// matchCommandPrefix checks whether text starts with a known built-in command or skill name.
// It returns the matched prefix including the trailing space when applicable.
func matchCommandPrefix(text string, skillNames []string) (string, bool) {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "/") {
		return "", false
	}
	builtins := []string{"/exit", "/clear", "/compact", "/config", "/fork", "/resume", "/skills", "/skill", "/ls", "/model", "/thinking", "/accent"}
	for _, cmd := range builtins {
		if trimmed == cmd {
			return cmd, true
		}
		if strings.HasPrefix(trimmed, cmd+" ") {
			return cmd + " ", true
		}
	}
	for _, name := range skillNames {
		cmd := "/" + name
		if trimmed == cmd {
			return cmd, true
		}
		if strings.HasPrefix(trimmed, cmd+" ") {
			return cmd + " ", true
		}
	}
	return "", false
}

// buildCompletionCandidates returns all candidates matching the current input prefix.
// Candidates are built-in slash commands plus "/skill <name>" variants.
func buildCompletionCandidates(prefix string, skillNames []string, _ []string) []string {
	base := []string{"/exit", "/clear", "/compact", "/config", "/fork", "/resume", "/skills", "/skill", "/ls", "/model", "/thinking",
		"/accent amber", "/accent rose", "/accent magenta", "/accent violet", "/accent cyan", "/accent mint", "/accent lime"}
	for _, name := range skillNames {
		base = append(base, "/skill +"+name, "/skill -"+name, "/skill "+name)
	}
	var matches []string
	for _, c := range base {
		if strings.HasPrefix(c, prefix) {
			matches = append(matches, c)
		}
	}
	return matches
}

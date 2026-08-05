package tui

import (
	"strings"
)

type inputAction struct {
	quit                       bool
	clear                      bool
	compaction                 bool
	inspectConfig              bool
	openCacheStats             bool
	listSkills                 bool
	listFiles                  bool
	listFilesPath              string
	showMCP                    bool
	submit                     string
	toggleSkill                string
	toggleEnable               bool
	switchModel                string
	setAccent                  string
	toggleThinking             bool
	requestSessionPicker       bool
	requestOneshotResumePicker bool
	forkSession                bool
	openModelPicker            bool
	openAccentPicker           bool
	invokeSkill                string // skill name for direct invocation
	invokeSkillArgs            string // optional args to pass with skill invocation
	launchOneshotTask          string // task for /oneshot <task>
	resumeOneshotID            string // run id for /oneshot --resume <id>
	toggleMode                 bool   // true for /mode with no argument
	setMode                    string // "plan" or "build" for /mode <mode>
	invalidMode                string // unrecognized /mode argument
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

// parseBuiltinCommand handles simple single-word slash commands and
// arg-variant commands (e.g. /accent rose) via the registry.
func parseBuiltinCommand(trimmed string) (inputAction, bool) {
	for _, sc := range slashCommands {
		if sc.OverlayOnly {
			continue
		}
		if trimmed == sc.ID {
			return sc.Build(""), true
		}
		if len(sc.ArgVariants) > 0 {
			// Check for exact variant match like "/accent rose"
			for _, v := range sc.ArgVariants {
				if trimmed == sc.ID+" "+v {
					return sc.Build(v), true
				}
			}
			// Check for prefix match like "/accent something"
			if strings.HasPrefix(trimmed, sc.ID+" ") {
				arg := strings.TrimSpace(strings.TrimPrefix(trimmed, sc.ID))
				return sc.Build(arg), true
			}
		}
	}
	return inputAction{}, false
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

type argHandler func(string, map[string]bool) (inputAction, bool)

func parseArgumentCommand(trimmed string, enabledSkills map[string]bool) (inputAction, bool) {
	handlers := []struct {
		prefix  string
		handler argHandler
	}{
		{"/accent ", handleAccent},
		{"/ls ", handleListFiles},
		{"/model ", handleModel},
		{"/skill ", handleSkill},
		{"/oneshot ", handleOneshot},
	}
	for _, h := range handlers {
		if !strings.HasPrefix(trimmed, h.prefix) {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(trimmed, h.prefix))
		if rest == "" {
			return inputAction{}, true
		}
		return h.handler(rest, enabledSkills)
	}
	return inputAction{}, false
}

func handleAccent(rest string, _ map[string]bool) (inputAction, bool) {
	entry := lookupCommand("/accent")
	if entry != nil {
		return entry.Build(rest), true
	}
	return inputAction{setAccent: rest}, true
}

func handleListFiles(rest string, _ map[string]bool) (inputAction, bool) {
	entry := lookupCommand("/ls")
	if entry != nil {
		return entry.Build(rest), true
	}
	return inputAction{listFiles: true, listFilesPath: rest}, true
}

func handleModel(rest string, _ map[string]bool) (inputAction, bool) {
	entry := lookupCommand("/model")
	if entry != nil {
		return entry.Build(rest), true
	}
	return inputAction{switchModel: rest}, true
}

func handleSkill(rest string, enabledSkills map[string]bool) (inputAction, bool) {
	enable := true
	switch {
	case strings.HasPrefix(rest, "+"):
		rest = strings.TrimSpace(strings.TrimPrefix(rest, "+"))
	case strings.HasPrefix(rest, "-"):
		rest = strings.TrimSpace(strings.TrimPrefix(rest, "-"))
		enable = false
	default:
		enable = !enabledSkills[rest]
	}
	if rest == "" {
		return inputAction{}, true
	}
	entry := lookupCommand("/skill")
	if entry != nil {
		prefix := "+"
		if !enable {
			prefix = "-"
		}
		return entry.Build(prefix + rest), true
	}
	return inputAction{toggleSkill: rest, toggleEnable: enable}, true
}

func handleOneshot(rest string, _ map[string]bool) (inputAction, bool) {
	if strings.HasPrefix(rest, "--resume ") {
		resumeID := strings.TrimSpace(strings.TrimPrefix(rest, "--resume "))
		if resumeID == "" {
			return inputAction{}, true
		}
		entry := lookupCommand("/oneshot")
		if entry != nil {
			return entry.Build("--resume " + resumeID), true
		}
		return inputAction{resumeOneshotID: resumeID}, true
	}
	entry := lookupCommand("/oneshot")
	if entry != nil {
		return entry.Build(rest), true
	}
	return inputAction{launchOneshotTask: rest}, true
}

// matchCommandPrefix checks whether text starts with a known built-in command or skill name.
// It returns the matched prefix including the trailing space when applicable.
func matchCommandPrefix(text string, skillNames []string, allowlistOnly bool) (string, bool) {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "/") {
		return "", false
	}
	prefixes := projectPrefixes(allowlistOnly)
	for _, p := range prefixes {
		if trimmed == p {
			return p, true
		}
	}
	if !allowlistOnly {
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
	return "", false
}

// buildCompletionCandidates returns all candidates matching the current input prefix.
// Candidates are built-in slash commands plus "/skill <name>" variants.
func buildCompletionCandidates(prefix string, skillNames []string, allowlistOnly bool) []string {
	base := projectCompletionCandidates(allowlistOnly, skillNames)
	var matches []string
	for _, c := range base {
		if strings.HasPrefix(c, prefix) {
			matches = append(matches, c)
		}
	}
	return matches
}

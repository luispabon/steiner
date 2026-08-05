package tui

import (
	"slices"
	"strings"
)

// slashCommand represents a slash command in the registry.
type slashCommand struct {
	ID          string
	Name        string
	Desc        string
	Source      string
	Allowlist   bool
	OverlayOnly bool
	ArgVariants []string
	HelpKey     string
	Build       func(arg string) inputAction
}

// helpBinding is a (key, desc) pair for the SESSION help group.
type helpBinding struct {
	key  string
	desc string
}

// slashCommands is the canonical registry of all built-in slash commands.
// Order determines display order in the overlay and help.
var slashCommands = []slashCommand{
	{
		ID:   "/cache-stats",
		Name: "Cache stats",
		Desc: "show cache hit rate stats",
		Build: func(_ string) inputAction {
			return inputAction{openCacheStats: true}
		},
	},
	{
		ID:   "/clear",
		Name: "Clear conversation",
		Desc: "reset the current session",
		Build: func(_ string) inputAction {
			return inputAction{clear: true}
		},
	},
	{
		ID:   "/compact",
		Name: "Compact context",
		Desc: "trigger compaction",
		Build: func(_ string) inputAction {
			return inputAction{compaction: true}
		},
	},
	{
		ID:   "/config",
		Name: "Inspect config",
		Desc: "show configuration",
		Build: func(_ string) inputAction {
			return inputAction{inspectConfig: true}
		},
	},
	{
		ID:        "/exit",
		Name:      "Exit",
		Desc:      "quit steiner",
		Allowlist: true,
		Build: func(_ string) inputAction {
			return inputAction{quit: true}
		},
	},
	{
		ID:   "/fork",
		Name: "Fork conversation",
		Desc: "fork current conversation into a new session",
		Build: func(_ string) inputAction {
			return inputAction{forkSession: true}
		},
	},
	{
		ID:          "/implement",
		Name:        "Implement plan",
		Desc:        "start implementation from a plan",
		OverlayOnly: true,
		Build: func(_ string) inputAction {
			return inputAction{}
		},
	},
	{
		ID:   "/ls",
		Name: "List files",
		Desc: "show directory contents",
		Build: func(arg string) inputAction {
			if arg == "" {
				return inputAction{listFiles: true}
			}
			return inputAction{listFiles: true, listFilesPath: arg}
		},
	},
	{
		ID:   "/mcp",
		Name: "MCP servers",
		Desc: "show MCP server status",
		Build: func(_ string) inputAction {
			return inputAction{showMCP: true}
		},
	},
	{
		ID:   "/model",
		Name: "Switch model",
		Desc: "pick a language model",
		Build: func(arg string) inputAction {
			if arg == "" {
				return inputAction{openModelPicker: true}
			}
			return inputAction{switchModel: arg}
		},
	},
	{
		ID:          "/mode",
		Name:        "Switch mode",
		Desc:        "toggle or set plan/build execution mode",
		ArgVariants: []string{"plan", "build"},
		HelpKey:     "shift+tab / /mode [plan|build]",
		Build: func(arg string) inputAction {
			switch arg {
			case "":
				return inputAction{toggleMode: true}
			case "plan", "build":
				return inputAction{setMode: arg}
			default:
				return inputAction{invalidMode: arg}
			}
		},
	},
	{
		ID:   "/oneshot",
		Name: "Oneshot mode",
		Desc: "run a headless task",
		Build: func(arg string) inputAction {
			if strings.HasPrefix(arg, "--resume ") {
				resumeID := strings.TrimSpace(strings.TrimPrefix(arg, "--resume "))
				return inputAction{resumeOneshotID: resumeID}
			}
			return inputAction{launchOneshotTask: arg}
		},
	},
	{
		ID:   "/oneshot-resume",
		Name: "Resume oneshot",
		Desc: "resume a oneshot run",
		Build: func(_ string) inputAction {
			return inputAction{requestOneshotResumePicker: true}
		},
	},
	{
		ID:   "/resume",
		Name: "Resume session",
		Desc: "load a previous session",
		Build: func(_ string) inputAction {
			return inputAction{requestSessionPicker: true}
		},
	},
	{
		ID:          "/review",
		Name:        "Review plan",
		Desc:        "review a completed plan",
		OverlayOnly: true,
		Build: func(_ string) inputAction {
			return inputAction{}
		},
	},
	{
		ID:   "/skill",
		Name: "Toggle skill",
		Desc: "enable or disable a skill",
		Build: func(arg string) inputAction {
			enable := true
			name := arg
			switch {
			case strings.HasPrefix(name, "+"):
				name = strings.TrimSpace(strings.TrimPrefix(name, "+"))
			case strings.HasPrefix(name, "-"):
				name = strings.TrimSpace(strings.TrimPrefix(name, "-"))
				enable = false
			default:
				// No prefix means enable
			}
			if name == "" {
				return inputAction{}
			}
			return inputAction{toggleSkill: name, toggleEnable: enable}
		},
	},
	{
		ID:   "/skills",
		Name: "List skills",
		Desc: "show available skills",
		Build: func(_ string) inputAction {
			return inputAction{listSkills: true}
		},
	},
	{
		ID:        "/thinking",
		Name:      "Toggle thinking",
		Desc:      "show or hide thinking blocks",
		Allowlist: true,
		Build: func(_ string) inputAction {
			return inputAction{toggleThinking: true}
		},
	},
	{
		ID:          "/accent",
		Name:        "Set accent",
		Desc:        "change accent color",
		Allowlist:   true,
		ArgVariants: []string{"amber", "coral", "rose", "magenta", "gold", "violet", "indigo", "blue", "cyan", "teal", "green", "mint", "lime", "random"},
		HelpKey:     "/accent [preset]",
		Build: func(arg string) inputAction {
			if arg == "" {
				return inputAction{openAccentPicker: true}
			}
			return inputAction{setAccent: arg}
		},
	},
}

// lookupCommand returns the slashCommand with the given ID, or nil.
func lookupCommand(id string) *slashCommand {
	for i := range slashCommands {
		if slashCommands[i].ID == id {
			return &slashCommands[i]
		}
	}
	return nil
}

// projectPrefixes returns the slash command prefixes for the composer
// autocomplete/matching. Skips OverlayOnly entries. If allowlistOnly is true,
// only Allowlist entries are returned. Arg-taking entries produce two entries:
// the bare command and the command with a trailing space.
func projectPrefixes(allowlistOnly bool) []string {
	var out []string
	for _, sc := range slashCommands {
		if sc.OverlayOnly {
			continue
		}
		if allowlistOnly && !sc.Allowlist {
			continue
		}
		out = append(out, sc.ID)
		// Commands with ArgVariants or that accept arbitrary arguments
		// produce a trailing-space variant.
		if takesArg(sc) {
			out = append(out, sc.ID+" ")
		}
	}
	return out
}

// commandsAcceptingArbitraryArgs lists commands that accept arbitrary
// (non-predefined) arguments, used by takesArg alongside ArgVariants.
var commandsAcceptingArbitraryArgs = []string{"/ls", "/model", "/skill", "/oneshot"}

// takesArg reports whether a slashCommand accepts an argument.
func takesArg(sc slashCommand) bool {
	if len(sc.ArgVariants) > 0 {
		return true
	}
	return slices.Contains(commandsAcceptingArbitraryArgs, sc.ID)
}

// projectCompletionCandidates returns completion candidate strings. Skips
// OverlayOnly entries. If allowlistOnly is true, only Allowlist entries are
// returned. The skillNames slice is used to dynamically expand /skill variants.
func projectCompletionCandidates(allowlistOnly bool, skillNames []string) []string {
	var out []string
	for _, sc := range slashCommands {
		if sc.OverlayOnly {
			continue
		}
		if allowlistOnly && !sc.Allowlist {
			continue
		}
		out = append(out, sc.ID)
		if sc.ID == "/skill" {
			for _, name := range skillNames {
				out = append(out, "/skill +"+name, "/skill -"+name, "/skill "+name)
			}
		} else {
			for _, v := range sc.ArgVariants {
				out = append(out, sc.ID+" "+v)
			}
		}
	}
	return out
}

// projectOverlayItems returns the list of slash overlay items. In oneshotMode,
// only Allowlist entries are returned. Skill items are appended using the
// provided skillNames and skillDescriptions.
func projectOverlayItems(oneshotMode bool, skillNames []string, skillDescriptions map[string]string) []slashOverlayItem {
	var out []slashOverlayItem
	for _, sc := range slashCommands {
		if oneshotMode && !sc.Allowlist {
			continue
		}
		out = append(out, slashOverlayItem{
			command: sc.ID,
			name:    sc.Name,
			desc:    sc.Desc,
			source:  sc.Source,
		})
	}
	// Add skill invocation items.
	for _, name := range skillNames {
		out = append(out, slashOverlayItem{
			command: "/" + name,
			desc:    strings.TrimSpace(skillDescriptions[name]),
			isSkill: true,
		})
	}
	return out
}

// helpOrder defines the display order of slash commands in the help SESSION group.
var helpOrder = []string{
	"/clear",
	"/compact",
	"/fork",
	"/resume",
	"/accent",
	"/thinking",
	"/mode",
	"/exit",
}

// projectHelpLines returns help key/desc pairs for the SESSION help group.
// Uses HelpKey when non-empty, otherwise the entry ID.
// Returns entries in the order specified by helpOrder, excluding OverlayOnly entries.
func projectHelpLines() []helpBinding {
	byID := make(map[string]*slashCommand, len(helpOrder))
	for i := range slashCommands {
		if slashCommands[i].OverlayOnly {
			continue
		}
		byID[slashCommands[i].ID] = &slashCommands[i]
	}

	var out []helpBinding
	for _, id := range helpOrder {
		sc, ok := byID[id]
		if !ok {
			continue
		}
		key := sc.HelpKey
		if key == "" {
			key = sc.ID
		}
		out = append(out, helpBinding{key: key, desc: sc.Desc})
	}
	return out
}

package agent

import (
	"fmt"
	"path/filepath"
	"strings"
)

func summarizeToolArguments(arguments map[string]any) string {
	if len(arguments) == 0 {
		return ""
	}
	parts := make([]string, 0, len(arguments))
	appendArg := func(name string) {
		value, ok := arguments[name]
		if !ok {
			return
		}
		rendered := summarizeTextPreview(fmt.Sprint(value), 40)
		parts = append(parts, fmt.Sprintf("%s=%s", name, rendered))
	}

	appendArg("path")
	appendArg("pattern")
	appendArg("command")
	appendArg("offset")
	appendArg("limit")

	if len(parts) == 0 {
		for key, value := range arguments {
			parts = append(parts, fmt.Sprintf("%s=%s", key, summarizeTextPreview(fmt.Sprint(value), 40)))
			if len(parts) == 3 {
				break
			}
		}
	}
	return strings.Join(parts, ", ")
}

func summarizeRecentToolCalls(messages []Message, limit int) []string {
	if limit <= 0 {
		return nil
	}
	var out []string
	for i := len(messages) - 1; i >= 0 && len(out) < limit; i-- {
		message := messages[i]
		if message.Role != MessageRoleAssistant || len(message.ToolCalls) == 0 {
			continue
		}
		for j := len(message.ToolCalls) - 1; j >= 0 && len(out) < limit; j-- {
			call := message.ToolCalls[j]
			summary := summarizeCallArguments(call.Name, call.Arguments)
			if summary == "" {
				out = append(out, strings.TrimSpace(call.Name))
				continue
			}
			out = append(out, strings.TrimSpace(call.Name)+" "+summary)
		}
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func summarizeCallArguments(toolName string, arguments map[string]any) string {
	if len(arguments) == 0 {
		return ""
	}
	root := summarizeSummaryRoot(arguments)
	parts := make([]string, 0, 3)
	for _, key := range []string{"cwd", "path", "pattern", "command", "offset", "limit"} {
		value, ok := arguments[key]
		if !ok {
			continue
		}
		summary := summarizeCallArgumentValue(toolName, key, value, root)
		if summary == "" {
			continue
		}
		parts = append(parts, key+"="+summary)
	}
	return strings.TrimSpace(strings.Join(parts, ", "))
}

func summarizeSummaryRoot(arguments map[string]any) string {
	if arguments == nil {
		return ""
	}
	cwd, _ := arguments["cwd"].(string)
	cwd = strings.TrimSpace(cwd)
	if filepath.IsAbs(cwd) {
		return cwd
	}
	return ""
}

func summarizeCallArgumentValue(toolName, key string, value any, root string) string {
	text := strings.TrimSpace(fmt.Sprint(value))
	switch key {
	case "cwd", "path":
		return sanitizeScratchpadPathWithRoot(text, root)
	case "command":
		if strings.EqualFold(strings.TrimSpace(toolName), "bash") {
			return summarizeBashCommand(text, root)
		}
		return summarizeTextValue(text)
	case "offset", "limit":
		return summarizeTextValue(text)
	default:
		return summarizeTextValue(text)
	}
}

func summarizeTextValue(value any) string {
	text := strings.TrimSpace(fmt.Sprint(value))
	if len(text) <= 40 {
		return text
	}
	return text[:40] + "..."
}

func summarizeBashCommand(command, root string) string {
	command = strings.TrimSpace(command)
	if command == "" {
		return ""
	}
	command = strings.Join(strings.Fields(command), " ")
	command = stripLeadingCdWrapper(command)
	command = redactRootPrefix(command, root)
	command = strings.TrimSpace(strings.Join(strings.Fields(command), " "))
	if len(command) <= 80 {
		return command
	}
	return command[:80] + "..."
}

func stripLeadingCdWrapper(command string) string {
	trimmed := strings.TrimSpace(command)
	if !strings.HasPrefix(strings.ToLower(trimmed), "cd ") {
		return command
	}
	for _, sep := range []string{" && ", " ; ", " || "} {
		if idx := strings.Index(trimmed, sep); idx >= 0 {
			return strings.TrimSpace(trimmed[idx+len(sep):])
		}
	}
	return command
}

func redactRootPrefix(text, root string) string {
	root = strings.TrimSpace(root)
	if root == "" || text == "" {
		return text
	}
	root = filepath.Clean(root)
	if text == root {
		return "."
	}
	text = strings.ReplaceAll(text, root+string(filepath.Separator), "")
	return strings.ReplaceAll(text, root, "")
}

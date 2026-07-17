package tui

import (
	"fmt"
	"strings"

	"github.com/luispabon/steiner/internal/delegation"
)

// specializedDelegateTools is the set of tool names rendered as a single
// delegation box in the TUI. It is derived from internal/delegation so the
// specialized delegate surface stays canonical across packages.
var specializedDelegateTools = specializedDelegateToolSet()

func specializedDelegateToolSet() map[string]bool {
	names := delegation.AllSpecializedDelegateTools()
	tools := make(map[string]bool, len(names))
	for _, tool := range names {
		tools[strings.ToLower(strings.TrimSpace(tool))] = true
	}
	return tools
}

// isSpecializedDelegateTool reports whether tool is a specialized delegate tool.
func isSpecializedDelegateTool(tool string) bool {
	return specializedDelegateTools[strings.ToLower(strings.TrimSpace(tool))]
}

// summarizeArgs extracts a human-readable summary from tool arguments
func summarizeArgs(tool string, args map[string]any) string {
	tool = normalizeToolName(tool)
	if args == nil {
		return tool
	}
	if tool == "follow_up" {
		return summarizeFollowUpArgs(args)
	}
	if tool == "delegate" || isSpecializedDelegateTool(tool) {
		return delegateArgText(args)
	}
	if tool == "mutate" {
		return summarizeMutateArgs(args)
	}
	if tool == "grep" {
		return summarizeGrepArgs(args)
	}
	if tool == "glob" {
		return summarizeGlobArgs(args)
	}
	if tool == "read" || tool == "read_file" {
		return summarizeReadArgs(args)
	}
	if tool == "ls" {
		return summarizeLSArgs(args)
	}
	// Try common arg keys in order
	for _, key := range []string{"command", "path", "file_path", "pattern", "query", "description", "url"} {
		if v, ok := args[key]; ok {
			return fmt.Sprintf("%v", v)
		}
	}
	// Fallback: value for the lexicographically first key, for deterministic output.
	if len(args) > 0 {
		keys := sortedKeys(args)
		return fmt.Sprintf("%v", args[keys[0]])
	}
	return tool
}

func delegateArgText(args map[string]any) string {
	if len(args) == 0 {
		return ""
	}
	for _, key := range []string{"task", "prompt", "description", "instructions", "goal"} {
		if v, ok := args[key]; ok {
			return fmt.Sprintf("%v", v)
		}
	}
	if len(args) > 0 {
		keys := sortedKeys(args)
		return fmt.Sprintf("%v", args[keys[0]])
	}
	return ""
}

func summarizeMutateArgs(args map[string]any) string {
	ops, _ := args["operations"].([]any)
	if len(ops) == 0 {
		return "mutate"
	}
	firstOp, _ := ops[0].(map[string]any)
	opType, _ := firstOp["type"].(string)

	var summary string
	if opType == "move" {
		from, _ := firstOp["from"].(string)
		to, _ := firstOp["to"].(string)
		summary = fmt.Sprintf("move %s → %s", from, to)
	} else {
		path, _ := firstOp["path"].(string)
		switch {
		case opType != "" && path != "":
			summary = opType + " " + path
		case path != "":
			summary = path
		case opType != "":
			summary = opType
		default:
			summary = "mutate"
		}
	}

	if len(ops) > 1 {
		return fmt.Sprintf("%s (+%d more)", summary, len(ops)-1)
	}
	return summary
}

func summarizeReadArgs(args map[string]any) string {
	path, _ := args["path"].(string)
	if path == "" {
		path, _ = args["file_path"].(string)
	}
	if path == "" {
		return "read"
	}

	offset := 1
	if v, ok := args["offset"].(float64); ok {
		offset = int(v)
	}

	limit := 200
	if v, ok := args["limit"].(float64); ok {
		limit = int(v)
	}

	end := offset + limit - 1
	return fmt.Sprintf("%s:%d–%d", path, offset, end)
}

func summarizeGrepArgs(args map[string]any) string {
	pattern, _ := args["pattern"].(string)
	if pattern == "" {
		return "grep"
	}

	result := fmt.Sprintf("'%s'", pattern)

	path, pathOk := args["path"].(string)
	glob, globOk := args["glob"].(string)

	if pathOk && path != "" {
		if !strings.HasPrefix(path, "./") && !strings.HasPrefix(path, "/") {
			path = "./" + path
		}

		if globOk && glob != "" {
			if strings.HasSuffix(path, "/") {
				result = fmt.Sprintf("%s in %s%s", result, path, glob)
			} else {
				result = fmt.Sprintf("%s in %s/%s", result, path, glob)
			}
		} else {
			result = fmt.Sprintf("%s in %s", result, path)
		}
	}

	return result
}

func summarizeGlobArgs(args map[string]any) string {
	pattern, _ := args["pattern"].(string)
	path, _ := args["path"].(string)

	if path != "" && pattern != "" {
		if !strings.HasPrefix(path, "./") {
			path = "./" + path
		}
		path = strings.TrimRight(path, "/")
		return path + "/" + pattern
	}

	if pattern != "" {
		return pattern
	}

	if path != "" {
		if !strings.HasPrefix(path, "./") {
			path = "./" + path
		}
		return path
	}

	return "glob"
}

func summarizeLSArgs(args map[string]any) string {
	path, _ := args["path"].(string)
	if path == "" {
		path = "."
	}
	recursive, _ := args["recursive"].(bool)
	if recursive {
		return path + " (recursive)"
	}
	return path
}

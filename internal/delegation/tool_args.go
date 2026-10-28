package delegation

import (
	"fmt"
	"sort"
	"strings"
)

// specializedToolSet holds the lowercased, trimmed names from
// AllSpecializedDelegateTools. SummarizeToolArgs uses it to render these tools
// through DelegateArgText.
var specializedToolSet = specializedToolNameSet()

func specializedToolNameSet() map[string]bool {
	names := AllSpecializedDelegateTools()
	tools := make(map[string]bool, len(names))
	for _, tool := range names {
		tools[strings.ToLower(strings.TrimSpace(tool))] = true
	}
	return tools
}

// SummarizeToolArgs returns a human-readable summary of a tool call's
// arguments, as shown in sub-agent boxes and the compacted history archive.
// The summary is not guaranteed to be one line: argument values such as a
// multi-line delegate objective can contain embedded newlines. Callers that
// need a single line normalize it themselves (the history archive does).
// It returns "" when args is empty.
func SummarizeToolArgs(tool string, args map[string]any) string {
	tool = strings.ToLower(strings.TrimSpace(tool))
	if len(args) == 0 {
		return ""
	}
	if tool == "follow_up" {
		return SummarizeFollowUpArgs(args)
	}
	if specializedToolSet[tool] {
		return DelegateArgText(args)
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
	keys := sortedKeys(args)
	return fmt.Sprintf("%v", args[keys[0]])
}

// DelegateArgText returns the flat text of a delegate call's arguments: the
// value of the first of objective, task, prompt, description, instructions, or
// goal that is present, otherwise the value of the lexically first key.
func DelegateArgText(args map[string]any) string {
	if len(args) == 0 {
		return ""
	}
	for _, key := range []string{"objective", "task", "prompt", "description", "instructions", "goal"} {
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

// StructuredDelegateBrief holds the fields a specialized delegate tool call
// sends as its structured brief.
type StructuredDelegateBrief struct {
	Objective, Context, Deliverable      string
	Constraints, SuccessCriteria, Checks []string
}

// ParseStructuredDelegateBrief extracts the objective/context/deliverable/
// constraints/success_criteria/checks fields a specialized delegate tool
// call sends (schema: internal/delegation/specialized_tools.go:82-113).
// ok is false when objective is missing/blank, signalling the caller
// should fall back to DelegateArgText's flat-text behavior instead.
func ParseStructuredDelegateBrief(args map[string]any) (StructuredDelegateBrief, bool) {
	objective, _ := args["objective"].(string)
	objective = strings.TrimSpace(objective)
	if objective == "" {
		return StructuredDelegateBrief{}, false
	}
	b := StructuredDelegateBrief{Objective: objective}
	if v, ok := args["context"].(string); ok {
		b.Context = strings.TrimSpace(v)
	}
	if v, ok := args["deliverable"].(string); ok {
		b.Deliverable = strings.TrimSpace(v)
	}
	b.Constraints = stringSliceArg(args["constraints"])
	b.SuccessCriteria = stringSliceArg(args["success_criteria"])
	b.Checks = stringSliceArg(args["checks"])
	return b, true
}

func stringSliceArg(v any) []string {
	raw, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ExtractFollowUpMessage returns the trimmed message from follow_up tool arguments.
func ExtractFollowUpMessage(args map[string]any) string {
	if args == nil {
		return ""
	}
	msg, ok := args["message"].(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(msg)
}

// SummarizeFollowUpArgs returns a human-readable summary of follow_up arguments.
func SummarizeFollowUpArgs(args map[string]any) string {
	msg := ExtractFollowUpMessage(args)
	if msg != "" {
		return msg
	}
	return "follow up"
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

// sortedKeys returns the keys of m in ascending lexicographic order.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

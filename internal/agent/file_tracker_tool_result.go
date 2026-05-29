package agent

import (
	"encoding/json"
	"fmt"
	"strings"
)

func isTestCommand(command string) bool {
	command = strings.ToLower(strings.TrimSpace(command))
	if command == "" {
		return false
	}
	switch {
	case strings.Contains(command, "go test"),
		strings.Contains(command, "pytest"),
		strings.Contains(command, "cargo test"),
		strings.Contains(command, "npm test"),
		strings.Contains(command, "pnpm test"),
		strings.Contains(command, "yarn test"),
		strings.Contains(command, "bun test"),
		strings.Contains(command, "make test"):
		return true
	}
	if strings.HasPrefix(command, "test ") || strings.Contains(command, " test ") {
		return true
	}
	return false
}

func (t *FileTracker) updateWorkingFile(path, lastAction string) workingFileUpdate {
	path = strings.TrimSpace(path)
	if path == "" {
		return workingFileUpdate{}
	}
	return workingFileUpdate{
		Path:       sanitizeScratchpadPath(path),
		LastAction: lastAction,
	}
}

func (t *FileTracker) observeMutationHeuristics(input map[string]any, content string) (workingFileUpdate, []string) {
	return t.observeMutateHeuristics(input, content)
}

func (t *FileTracker) observeMutateHeuristics(_ map[string]any, content string) (workingFileUpdate, []string) {
	var result struct {
		Paths  []string `json:"paths"`
		Output string   `json:"output"`
	}
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		return workingFileUpdate{}, nil
	}
	preview := summarizeTextPreview(result.Output, 96)
	for _, path := range result.Paths {
		if sanitized := sanitizeScratchpadPath(path); sanitized != "" {
			t.BumpGeneration(sanitized)
		}
	}
	if len(result.Paths) == 0 {
		return workingFileUpdate{}, nil
	}
	path := sanitizeScratchpadPath(result.Paths[len(result.Paths)-1])
	return t.updateWorkingFile(path, fmt.Sprintf("mutated %s: %s", path, preview)), nil
}

func (t *FileTracker) observeBashHeuristics(input map[string]any, content string) (workingFileUpdate, []string) {
	var result struct {
		ExitCode  int    `json:"exit_code"`
		Truncated bool   `json:"truncated"`
		Output    string `json:"output"`
		Message   string `json:"message"`
	}
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		return workingFileUpdate{}, nil
	}
	command := ""
	if input != nil {
		command, _ = input["command"].(string)
	}
	command = strings.TrimSpace(command)
	cwd := ""
	if input != nil {
		cwd, _ = input["cwd"].(string)
	}
	command = summarizeBashCommand(command, cwd)
	preview := summarizeTextPreview(result.Output, 96)
	if preview == "" {
		preview = strings.TrimSpace(result.Message)
	}
	if preview == "" {
		preview = fmt.Sprintf("exit_code=%d", result.ExitCode)
	}
	update := workingFileUpdate{LastAction: fmt.Sprintf("bash: %s", preview)}
	var facts []string
	if isTestCommand(command) {
		status := "failed"
		if result.ExitCode == 0 {
			status = "passed"
		}
		facts = append(facts, fmt.Sprintf("tests %s: %s", status, command))
	}
	return update, facts
}

func (t *FileTracker) observeGenericToolHeuristics(toolName string, content string) (workingFileUpdate, []string) {
	update := workingFileUpdate{LastAction: fmt.Sprintf("%s: %s", strings.TrimSpace(toolName), summarizeTextPreview(content, 80))}
	return update, nil
}

// ObserveToolResult dispatches to per-tool heuristics and returns a
// workingFileUpdate and any decision facts derived from the result.
func (t *FileTracker) ObserveToolResult(_ int, toolName string, input map[string]any, content string) (workingFileUpdate, []string) {
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "read":
		result, ok := parseReadResult(content)
		if !ok {
			return workingFileUpdate{}, nil
		}
		observation := fileObservation{Action: "full"}
		if strings.Contains(content, "file unchanged since turn") {
			observation.Action = "annotated"
		}
		return t.observeReadHeuristics(result, observation, content)
	case "mutate":
		return t.observeMutationHeuristics(input, content)
	case "bash":
		return t.observeBashHeuristics(input, content)
	default:
		return t.observeGenericToolHeuristics(toolName, content)
	}
}

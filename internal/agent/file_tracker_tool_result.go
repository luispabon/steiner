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
		Path:       sanitizeTrackedPath(path),
		LastAction: lastAction,
	}
}

func (t *FileTracker) observeMutationHeuristics(_ string, input map[string]any, content string) (workingFileUpdate, []string) {
	return t.observeMutateHeuristics(input, content)
}

func (t *FileTracker) observeMutateHeuristics(_ map[string]any, content string) (workingFileUpdate, []string) {
	var result struct {
		Created  []string     `json:"created"`
		Modified []string     `json:"modified"`
		Deleted  []string     `json:"deleted"`
		Moved    []moveResult `json:"moved"`
		Output   string       `json:"output"`
	}
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		return workingFileUpdate{}, nil
	}

	// Generation bumping for all paths now handled in recordMutationForContextManager (tool_exec.go),
	// so this method only computes the working file update.

	// Pick the working file by category precedence (moved > deleted > modified >
	// created), not operation order — the result arrays carry no operation order
	// to recover: mutate_planner.go sorts Created/Modified/Deleted independently
	// and builds Paths by ranging a map. This mirrors the pre-trim code, which
	// also had no operation order and picked the alphabetically-last touched path.
	var lastPath string
	var lastAction string

	if len(result.Created) > 0 {
		lastPath = result.Created[len(result.Created)-1]
		lastAction = "created"
	}
	if len(result.Modified) > 0 {
		lastPath = result.Modified[len(result.Modified)-1]
		lastAction = "modified"
	}
	if len(result.Deleted) > 0 {
		lastPath = result.Deleted[len(result.Deleted)-1]
		lastAction = "deleted"
	}
	if len(result.Moved) > 0 {
		lastMove := result.Moved[len(result.Moved)-1]
		lastPath = lastMove.To
		lastAction = fmt.Sprintf("moved to %s", lastMove.To)
	}

	if lastPath == "" {
		return workingFileUpdate{}, nil
	}

	path := sanitizeTrackedPath(lastPath)
	return t.updateWorkingFile(path, fmt.Sprintf("mutated %s: %s", path, lastAction)), nil
}

type moveResult struct {
	From string `json:"from"`
	To   string `json:"to"`
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
		return t.observeMutationHeuristics(toolName, input, content)
	case "bash":
		return t.observeBashHeuristics(input, content)
	default:
		return t.observeGenericToolHeuristics(toolName, content)
	}
}

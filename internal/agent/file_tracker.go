package agent

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type workingFileUpdate struct {
	Path       string
	LastAction string
}

type trackedFileRead struct {
	Path        string
	Canonical   string
	StartLine   int
	EndLine     int
	TotalLines  int
	LastTurn    int
	ContentHash uint64
	Generation  uint64
}

type readResult struct {
	Path       string `json:"path"`
	StartLine  int    `json:"start_line"`
	EndLine    int    `json:"end_line"`
	TotalLines int    `json:"total_lines"`
	Output     string `json:"output"`
}

type FileTracker struct {
	reads       map[string]trackedFileRead
	generations map[string]uint64
}

type fileObservation struct {
	Path         string
	PreviousRead trackedFileRead
	HadPrevious  bool
	Action       string
	Reason       string
	Notes        []string
}

func (t *FileTracker) ObserveRead(turn int, content string, annotationsEnabled bool) (string, fileObservation) {
	result, ok := parseReadResult(content)
	if !ok {
		return content, fileObservation{}
	}
	path := strings.TrimSpace(result.Path)
	if path == "" {
		return content, fileObservation{}
	}
	canonicalPath, ok := normalizeTrackedPath(path)
	if !ok {
		return content, fileObservation{}
	}
	if _, err := os.Stat(canonicalPath); err != nil {
		return content, fileObservation{}
	}
	hash, ok := hashFileContent(canonicalPath)
	if !ok {
		return content, fileObservation{}
	}
	if t.reads == nil {
		t.reads = make(map[string]trackedFileRead)
	}
	if t.generations == nil {
		t.generations = make(map[string]uint64)
	}

	next := trackedFileRead{
		Path:        path,
		Canonical:   canonicalPath,
		StartLine:   result.StartLine,
		EndLine:     result.EndLine,
		TotalLines:  result.TotalLines,
		LastTurn:    turn,
		ContentHash: hash,
		Generation:  t.generations[canonicalPath],
	}
	previous, ok := t.reads[canonicalPath]
	t.reads[canonicalPath] = next

	observation := fileObservation{
		Path:         path,
		PreviousRead: previous,
		HadPrevious:  ok,
		Action:       "full",
		Reason:       "first read",
	}
	if !annotationsEnabled {
		observation.Reason = "annotations disabled"
		return content, observation
	}
	if !ok {
		return content, observation
	}
	observation.Reason = "range changed"
	if previous.StartLine == next.StartLine && previous.EndLine == next.EndLine && previous.TotalLines == next.TotalLines {
		observation.Reason = "modified file"
		if previous.Generation != next.Generation {
			observation.Reason = "generation changed"
			observation.Notes = append(observation.Notes,
				fmt.Sprintf("previous_generation=%d", previous.Generation),
				fmt.Sprintf("current_generation=%d", next.Generation),
				"mtime_unchanged",
			)
			return content, observation
		}
		if previous.ContentHash == next.ContentHash {
			observation.Action = "annotated"
			observation.Reason = fmt.Sprintf("unchanged since turn %d", previous.LastTurn)
			result.Output = fileUnchangedAnnotation(previous)
			data, err := json.Marshal(result)
			if err != nil {
				return content, observation
			}
			return string(data), observation
		}
	}
	return content, observation
}

func hashFileContent(path string) (uint64, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	h := fnv.New64a()
	h.Write(data)
	return h.Sum64(), true
}

func parseReadResult(content string) (readResult, bool) {
	var result readResult
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		return readResult{}, false
	}
	return result, true
}

func (r readResult) rangeSummary() string {
	switch {
	case r.EndLine > 0 && r.TotalLines > 0:
		return fmt.Sprintf("lines %d-%d/%d", r.StartLine, r.EndLine, r.TotalLines)
	case r.TotalLines > 0:
		return fmt.Sprintf("%d lines", r.TotalLines)
	default:
		return "unknown range"
	}
}

func (t *FileTracker) Clone() FileTracker {
	if len(t.reads) == 0 && len(t.generations) == 0 {
		return FileTracker{}
	}
	out := FileTracker{
		reads:       make(map[string]trackedFileRead, len(t.reads)),
		generations: make(map[string]uint64, len(t.generations)),
	}
	for path, read := range t.reads {
		out.reads[path] = read
	}
	for path, generation := range t.generations {
		out.generations[path] = generation
	}
	return out
}

func (t *FileTracker) Summaries(limit int) []string {
	if len(t.reads) == 0 || limit <= 0 {
		return nil
	}
	reads := make([]trackedFileRead, 0, len(t.reads))
	for _, read := range t.reads {
		reads = append(reads, read)
	}
	sort.Slice(reads, func(i, j int) bool {
		if reads[i].LastTurn != reads[j].LastTurn {
			return reads[i].LastTurn > reads[j].LastTurn
		}
		if reads[i].Path != reads[j].Path {
			return reads[i].Path < reads[j].Path
		}
		return reads[i].StartLine < reads[j].StartLine
	})
	out := make([]string, 0, len(reads))
	for _, read := range reads {
		out = append(out, fmt.Sprintf("%s lines %d-%d/%d", read.Path, read.StartLine, read.EndLine, read.TotalLines))
		if len(out) == limit {
			break
		}
	}
	return out
}

// PruneBeforeTurn removes all tracked read entries whose LastTurn is strictly
// less than the given turn. Called after compaction drops old turns.
func (t *FileTracker) PruneBeforeTurn(turn int) {
	for key, entry := range t.reads {
		if entry.LastTurn < turn {
			delete(t.reads, key)
		}
	}
}

func (t *FileTracker) BumpGeneration(path string) bool {
	canonicalPath, ok := normalizeTrackedPath(path)
	if !ok {
		return false
	}
	if t.generations == nil {
		t.generations = make(map[string]uint64)
	}
	// Generation is tracked per file, not per read range, so any successful
	// mutation invalidates stale annotations for every region of the file.
	t.generations[canonicalPath]++
	return true
}

func normalizeTrackedPath(path string) (string, bool) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "", false
	}
	if !filepath.IsAbs(trimmed) {
		absPath, err := filepath.Abs(trimmed)
		if err != nil {
			return "", false
		}
		trimmed = absPath
	}
	return filepath.Clean(trimmed), true
}

func fileUnchangedAnnotation(read trackedFileRead) string {
	rangeSummary := fmt.Sprintf("lines %d-%d of %d", read.StartLine, read.EndLine, read.TotalLines)
	if read.EndLine == 0 {
		rangeSummary = fmt.Sprintf("empty file with %d lines", read.TotalLines)
	}
	return fmt.Sprintf("[file unchanged since turn %d: %s in %s]", read.LastTurn, rangeSummary, read.Path)
}

// RecordMutation implements MutationRecorder by bumping the in-memory file
// generation for a successful mutation.
func (t *FileTracker) RecordMutation(path string) {
	t.BumpGeneration(path)
}

func (t *FileTracker) updateWorkingFile(path string, toolName, lastAction string) workingFileUpdate {
	path = strings.TrimSpace(path)
	if path == "" {
		return workingFileUpdate{}
	}
	return workingFileUpdate{
		Path:       sanitizeScratchpadPath(path),
		LastAction: lastAction,
	}
}

func (t *FileTracker) observeReadHeuristics(result readResult, observation fileObservation, content string) (workingFileUpdate, []string) {
	path := sanitizeScratchpadPath(result.Path)
	if path == "" {
		return workingFileUpdate{}, nil
	}
	update := t.updateWorkingFile(path, "read", fmt.Sprintf("read %s (%s)", path, result.rangeSummary()))
	var facts []string
	if observation.Action == "annotated" || strings.Contains(content, "file unchanged since turn") {
		facts = append(facts, fmt.Sprintf("read annotation: %s", summarizeTextPreview(content, 96)))
	}
	if observation.Action == "full" && observation.Reason == "previous read no longer visible in context" {
		facts = append(facts, fmt.Sprintf("read %s: full content (previous read turn %d no longer visible)", path, observation.PreviousRead.LastTurn))
	}
	return update, facts
}

func (t *FileTracker) observeMutationHeuristics(toolName string, input map[string]any, content string) (workingFileUpdate, []string) {
	var result struct {
		Path   string `json:"path"`
		Output string `json:"output"`
	}
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		return workingFileUpdate{}, nil
	}
	path := sanitizeScratchpadPath(result.Path)
	if path == "" && input != nil {
		if rawPath, ok := input["path"].(string); ok {
			path = sanitizeScratchpadPath(rawPath)
		}
	}
	var update workingFileUpdate
	if path != "" {
		update = t.updateWorkingFile(path, toolName, fmt.Sprintf("%s %s: %s", toolVerb(toolName), path, summarizeTextPreview(result.Output, 96)))
		t.BumpGeneration(path)
	}
	var facts []string
	if toolName == "edit" {
		facts = append(facts, fmt.Sprintf("edited %s: %s", path, summarizeTextPreview(result.Output, 96)))
	}
	return update, facts
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
func (t *FileTracker) ObserveToolResult(turn int, toolName string, input map[string]any, content string) (workingFileUpdate, []string) {
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
	case "edit", "write", "apply_patch":
		return t.observeMutationHeuristics(toolName, input, content)
	case "bash":
		return t.observeBashHeuristics(input, content)
	default:
		return t.observeGenericToolHeuristics(toolName, content)
	}
}

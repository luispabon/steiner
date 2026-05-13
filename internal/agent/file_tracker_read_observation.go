package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

type fileObservation struct {
	Path         string
	PreviousRead trackedFileRead
	HadPrevious  bool
	Action       string
	Reason       string
	Notes        []string
}

// ObserveRead records a read result and may replace unchanged content with an annotation.
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
	hash, ok := t.readObservationHash(canonicalPath)
	if !ok {
		return content, fileObservation{}
	}

	previous, next, existed := t.recordTrackedRead(canonicalPath, path, result, hash, turn)
	observation := fileObservation{
		Path:         path,
		PreviousRead: previous,
		HadPrevious:  existed,
		Action:       "full",
		Reason:       "first read",
	}
	if !annotationsEnabled {
		observation.Reason = "annotations disabled"
		return content, observation
	}
	if !existed {
		return content, observation
	}
	observation, annotatedContent := t.decorateReadObservation(result, previous, next)
	if annotatedContent != "" {
		return annotatedContent, observation
	}
	return content, observation
}

func (t *FileTracker) readObservationHash(canonicalPath string) (uint64, bool) {
	if _, err := os.Stat(canonicalPath); err != nil {
		return 0, false
	}
	hash, ok := hashFileContent(canonicalPath)
	if !ok {
		return 0, false
	}
	if t.reads == nil {
		t.reads = make(map[string]trackedFileRead)
	}
	if t.generations == nil {
		t.generations = make(map[string]uint64)
	}
	return hash, true
}

func (t *FileTracker) recordTrackedRead(canonicalPath, path string, result readResult, hash uint64, turn int) (trackedFileRead, trackedFileRead, bool) {
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
	previous, existed := t.reads[canonicalPath]
	t.reads[canonicalPath] = next
	return previous, next, existed
}

func (t *FileTracker) decorateReadObservation(result readResult, previous, next trackedFileRead) (fileObservation, string) {
	observation := fileObservation{
		Path:         result.Path,
		PreviousRead: previous,
		HadPrevious:  true,
		Action:       "full",
		Reason:       "range changed",
	}
	if previous.StartLine == next.StartLine && previous.EndLine == next.EndLine && previous.TotalLines == next.TotalLines {
		observation.Reason = "modified file"
		if previous.Generation != next.Generation {
			observation.Reason = "generation changed"
			observation.Notes = append(observation.Notes,
				fmt.Sprintf("previous_generation=%d", previous.Generation),
				fmt.Sprintf("current_generation=%d", next.Generation),
				"mtime_unchanged",
			)
			return observation, ""
		}
		if previous.ContentHash == next.ContentHash {
			observation.Action = "annotated"
			observation.Reason = fmt.Sprintf("unchanged since turn %d", previous.LastTurn)
			result.Output = fileUnchangedAnnotation(previous)
			data, err := json.Marshal(result)
			if err != nil {
				return observation, ""
			}
			return observation, string(data)
		}
	}
	return observation, ""
}

func (t *FileTracker) observeReadHeuristics(result readResult, observation fileObservation, content string) (workingFileUpdate, []string) {
	path := sanitizeScratchpadPath(result.Path)
	if path == "" {
		return workingFileUpdate{}, nil
	}
	update := t.updateWorkingFile(path, fmt.Sprintf("read %s (%s)", path, result.rangeSummary()))
	var facts []string
	if observation.Action == "annotated" || strings.Contains(content, "file unchanged since turn") {
		facts = append(facts, fmt.Sprintf("read annotation: %s", summarizeTextPreview(content, 96)))
	}
	if observation.Action == "full" && observation.Reason == "previous read no longer visible in context" {
		facts = append(facts, fmt.Sprintf("read %s: full content (previous read turn %d no longer visible)", path, observation.PreviousRead.LastTurn))
	}
	return update, facts
}

func fileUnchangedAnnotation(read trackedFileRead) string {
	rangeSummary := fmt.Sprintf("lines %d-%d of %d", read.StartLine, read.EndLine, read.TotalLines)
	if read.EndLine == 0 {
		rangeSummary = fmt.Sprintf("empty file with %d lines", read.TotalLines)
	}
	return fmt.Sprintf("[file unchanged since turn %d: %s in %s]", read.LastTurn, rangeSummary, read.Path)
}

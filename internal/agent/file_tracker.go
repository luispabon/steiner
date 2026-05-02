package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type trackedFileRead struct {
	Path       string
	Canonical  string
	StartLine  int
	EndLine    int
	TotalLines int
	LastTurn   int
	ModTime    time.Time
	Generation uint64
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
	info, err := os.Stat(canonicalPath)
	if err != nil {
		return content, fileObservation{}
	}
	if t.reads == nil {
		t.reads = make(map[string]trackedFileRead)
	}
	if t.generations == nil {
		t.generations = make(map[string]uint64)
	}

	next := trackedFileRead{
		Path:       path,
		Canonical:  canonicalPath,
		StartLine:  result.StartLine,
		EndLine:    result.EndLine,
		TotalLines: result.TotalLines,
		LastTurn:   turn,
		ModTime:    info.ModTime(),
		Generation: t.generations[canonicalPath],
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
		if previous.ModTime.Equal(next.ModTime) {
			observation.Reason = "generation changed"
			if previous.Generation == next.Generation {
				observation.Action = "annotated"
				observation.Reason = fmt.Sprintf("unchanged since turn %d", previous.LastTurn)
				result.Output = fileUnchangedAnnotation(previous)
				data, err := json.Marshal(result)
				if err != nil {
					return content, observation
				}
				return string(data), observation
			}
			observation.Notes = append(observation.Notes,
				fmt.Sprintf("previous_generation=%d", previous.Generation),
				fmt.Sprintf("current_generation=%d", next.Generation),
				"mtime_unchanged",
			)
			return content, observation
		}
	}
	if observation.Reason == "modified file" {
		observation.Notes = append(observation.Notes,
			fmt.Sprintf("previous_mtime=%s", previous.ModTime.UTC().Format(time.RFC3339Nano)),
			fmt.Sprintf("current_mtime=%s", next.ModTime.UTC().Format(time.RFC3339Nano)),
		)
	}
	return content, observation
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

func (t *FileTracker) BumpGeneration(path string) bool {
	canonicalPath, ok := normalizeTrackedPath(path)
	if !ok {
		return false
	}
	if t.generations == nil {
		t.generations = make(map[string]uint64)
	}
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

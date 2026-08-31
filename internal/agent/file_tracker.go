package agent

import (
	"fmt"
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

// FileTracker records recent file reads and per-file mutation generations.
type FileTracker struct {
	reads       map[string]trackedFileRead
	generations map[string]uint64
}

// Clone returns a copy of the tracker state.
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

// Summaries returns recent read summaries in most-recent-first order.
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

// BumpGeneration marks a file as mutated and advances its generation counter.
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

// RecordMutation implements MutationRecorder by bumping the in-memory file
// generation for a successful mutation.
func (t *FileTracker) RecordMutation(path string) {
	t.BumpGeneration(path)
}

// WasObserved reports whether path has a tracked read this session. It is a
// precondition floor, not a staleness guarantee: t.reads only reflects reads
// made through the tool loop and writes recorded via RecordMutation, so a
// file changed outside that loop (bash, gofmt -w, an external editor) can
// still report observed while its on-disk content has moved on.
func (t *FileTracker) WasObserved(path string) bool {
	canonicalPath, ok := normalizeTrackedPath(path)
	if !ok {
		return false
	}
	_, observed := t.reads[canonicalPath]
	return observed
}

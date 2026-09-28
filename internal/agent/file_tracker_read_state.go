package agent

import "github.com/luispabon/steiner/internal/tool"

// ReadState reports the tracker's record of its last read of path as seen at
// currentTurn, for mutate's failure diagnostics. A path with no read entry
// yields Observed=false and TurnsSinceRead=-1.
func (t *FileTracker) ReadState(path string, currentTurn int) tool.FileReadState {
	canonicalPath, ok := normalizeTrackedPath(path)
	if !ok {
		return tool.FileReadState{TurnsSinceRead: -1}
	}
	read, observed := t.reads[canonicalPath]
	if !observed {
		return tool.FileReadState{TurnsSinceRead: -1}
	}
	state := tool.FileReadState{
		Observed:         true,
		StartLine:        read.StartLine,
		EndLine:          read.EndLine,
		TotalLines:       read.TotalLines,
		TurnsSinceRead:   max(currentTurn-read.LastTurn, 0),
		MutatedSinceRead: t.generations[canonicalPath] > read.Generation,
	}
	// An unhashable file (deleted, permission denied) leaves ChangedSinceRead
	// false rather than claiming a change; callers treat that as unknown.
	if hash, ok := hashFileContent(canonicalPath); ok {
		state.ChangedSinceRead = hash != read.ContentHash
	}
	return state
}

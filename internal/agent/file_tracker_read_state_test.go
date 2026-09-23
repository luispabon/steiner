package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/luispabon/steiner/internal/tool"
)

func observeTrackedRead(t *testing.T, tracker *FileTracker, path string, turn int) {
	t.Helper()
	content, err := json.Marshal(readResult{Path: path, StartLine: 1, EndLine: 2, TotalLines: 2, Output: "one\ntwo\n"})
	if err != nil {
		t.Fatalf("marshal read result: %v", err)
	}
	tracker.ObserveRead(turn, string(content), true)
}

func TestFileTrackerReadState(t *testing.T) {
	observed := tool.FileReadState{Observed: true, StartLine: 1, EndLine: 2, TotalLines: 2}

	tests := []struct {
		name        string
		setup       func(t *testing.T, tracker *FileTracker, path string)
		currentTurn int
		want        tool.FileReadState
	}{
		{
			name:        "never read",
			currentTurn: 3,
			want:        tool.FileReadState{TurnsSinceRead: -1},
		},
		{
			name: "read unchanged",
			setup: func(t *testing.T, tracker *FileTracker, path string) {
				observeTrackedRead(t, tracker, path, 1)
			},
			currentTurn: 3,
			want:        withTurns(observed, 2),
		},
		{
			name: "turn arithmetic floors at zero",
			setup: func(t *testing.T, tracker *FileTracker, path string) {
				observeTrackedRead(t, tracker, path, 5)
			},
			currentTurn: 3,
			want:        withTurns(observed, 0),
		},
		{
			name: "recorded mutation sets MutatedSinceRead",
			setup: func(t *testing.T, tracker *FileTracker, path string) {
				observeTrackedRead(t, tracker, path, 1)
				tracker.RecordMutation(path)
			},
			currentTurn: 2,
			want:        tool.FileReadState{Observed: true, StartLine: 1, EndLine: 2, TotalLines: 2, TurnsSinceRead: 1, MutatedSinceRead: true},
		},
		{
			name: "external rewrite sets only ChangedSinceRead",
			setup: func(t *testing.T, tracker *FileTracker, path string) {
				observeTrackedRead(t, tracker, path, 1)
				if err := os.WriteFile(path, []byte("rewritten\n"), 0o644); err != nil {
					t.Fatalf("rewrite file: %v", err)
				}
			},
			currentTurn: 2,
			want:        tool.FileReadState{Observed: true, StartLine: 1, EndLine: 2, TotalLines: 2, TurnsSinceRead: 1, ChangedSinceRead: true},
		},
		{
			name: "unhashable file leaves ChangedSinceRead false",
			setup: func(t *testing.T, tracker *FileTracker, path string) {
				observeTrackedRead(t, tracker, path, 1)
				if err := os.Remove(path); err != nil {
					t.Fatalf("remove file: %v", err)
				}
			},
			currentTurn: 2,
			want:        tool.FileReadState{Observed: true, StartLine: 1, EndLine: 2, TotalLines: 2, TurnsSinceRead: 1},
		},
		{
			name: "prune marks the path pruned",
			setup: func(t *testing.T, tracker *FileTracker, path string) {
				observeTrackedRead(t, tracker, path, 1)
				tracker.PruneBeforeTurn(2)
			},
			currentTurn: 3,
			want:        tool.FileReadState{Pruned: true, TurnsSinceRead: -1},
		},
		{
			name: "re-read clears pruned",
			setup: func(t *testing.T, tracker *FileTracker, path string) {
				observeTrackedRead(t, tracker, path, 1)
				tracker.PruneBeforeTurn(2)
				observeTrackedRead(t, tracker, path, 3)
			},
			currentTurn: 4,
			want:        withTurns(observed, 1),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "note.txt")
			if err := os.WriteFile(path, []byte("one\ntwo\n"), 0o644); err != nil {
				t.Fatalf("write file: %v", err)
			}
			tracker := FileTracker{}
			if tc.setup != nil {
				tc.setup(t, &tracker, path)
			}
			if got := tracker.ReadState(path, tc.currentTurn); got != tc.want {
				t.Errorf("ReadState(%q, %d) = %+v, want %+v", path, tc.currentTurn, got, tc.want)
			}
		})
	}
}

func TestFileTrackerReadStateEmptyPath(t *testing.T) {
	tracker := FileTracker{}
	if got := tracker.ReadState("   ", 3); got != (tool.FileReadState{TurnsSinceRead: -1}) {
		t.Errorf("ReadState(blank) = %+v, want unobserved", got)
	}
}

func TestFileTrackerClonePreservesPruned(t *testing.T) {
	path := filepath.Join(t.TempDir(), "note.txt")
	if err := os.WriteFile(path, []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	tracker := FileTracker{}
	observeTrackedRead(t, &tracker, path, 1)
	tracker.PruneBeforeTurn(2)

	clone := tracker.Clone()
	if got := clone.ReadState(path, 3); !got.Pruned {
		t.Errorf("cloned ReadState = %+v, want Pruned true", got)
	}
}

func withTurns(state tool.FileReadState, turns int) tool.FileReadState {
	state.TurnsSinceRead = turns
	return state
}

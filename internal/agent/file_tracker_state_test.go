package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileTrackerGenerationLifecycleIsFileWide(t *testing.T) {
	path := filepath.Join(t.TempDir(), "note.txt")
	if err := os.WriteFile(path, []byte("one\ntwo\nthree\nfour\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	read := func(start, end int) string {
		content, err := json.Marshal(readResult{Path: path, StartLine: start, EndLine: end, TotalLines: 4})
		if err != nil {
			t.Fatal(err)
		}
		return string(content)
	}

	var tracker FileTracker
	tracker.RecordRead(1, read(1, 2))
	tracker.RecordRead(2, read(3, 4))
	if got := tracker.ReadState(path, 2); got.StartLine != 3 || got.EndLine != 4 || got.MutatedSinceRead {
		t.Fatalf("latest read state = %+v, want lines 3-4 and no mutation", got)
	}

	if !tracker.BumpGeneration(path) {
		t.Fatal("generation bump failed")
	}
	if got := tracker.ReadState(path, 2); !got.MutatedSinceRead {
		t.Fatalf("state after file mutation = %+v, want mutation", got)
	}

	tracker.RecordRead(3, read(1, 2))
	if got := tracker.ReadState(path, 3); got.MutatedSinceRead {
		t.Fatalf("state after reread = %+v, want mutation cleared", got)
	}
	tracker.RecordMutation(path)
	if got := tracker.ReadState(path, 3); !got.MutatedSinceRead {
		t.Fatalf("state after second mutation = %+v, want mutation", got)
	}
}

type stubMutationResult struct {
	Paths   []string `json:"paths"`
	mutated bool
}

func (r stubMutationResult) WasMutated() bool { return r.mutated }

func TestFileTrackerSkipsFailedMutationGeneration(t *testing.T) {
	tests := []struct {
		name           string
		mutated        bool
		wantGeneration bool
	}{
		{name: "successful mutation bumps generation", mutated: true, wantGeneration: true},
		{name: "failed mutation leaves generation untouched", mutated: false, wantGeneration: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "note.txt")
			if err := os.WriteFile(path, []byte("one\ntwo\nthree\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			content, err := json.Marshal(readResult{Path: path, StartLine: 1, EndLine: 3, TotalLines: 3, Output: "one\ntwo\nthree\n"})
			if err != nil {
				t.Fatal(err)
			}

			cm := &ContextStateManager{}
			cm.fileTracker.RecordRead(1, string(content))

			result := stubMutationResult{Paths: []string{path}, mutated: tt.mutated}
			recordMutationForContextManager(cm, "mutate", nil, result)

			if got := len(cm.fileTracker.generations); (got != 0) != tt.wantGeneration {
				t.Fatalf("generation entries = %d, want bumped=%v", got, tt.wantGeneration)
			}
			if got := cm.fileTracker.ReadState(path, 1); got.MutatedSinceRead != tt.wantGeneration {
				t.Fatalf("MutatedSinceRead = %v, want %v", got.MutatedSinceRead, tt.wantGeneration)
			}
		})
	}
}

func TestFileTrackerObserveToolResultBash(t *testing.T) {
	tests := []struct {
		name       string
		input      map[string]any
		content    string
		wantPrefix string
		wantFact   bool
	}{
		{
			name:       "non-test command no fact",
			input:      map[string]any{"command": "ls -la"},
			content:    `{"exit_code":0,"output":"file.go","truncated":false}`,
			wantPrefix: "bash:",
			wantFact:   false,
		},
		{
			name:       "test command produces fact",
			input:      map[string]any{"command": "go test ./..."},
			content:    `{"exit_code":0,"output":"ok","truncated":false}`,
			wantPrefix: "bash:",
			wantFact:   true,
		},
		{
			name:       "failed test produces fact",
			input:      map[string]any{"command": "go test ./..."},
			content:    `{"exit_code":1,"output":"FAIL","truncated":false}`,
			wantPrefix: "bash:",
			wantFact:   true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var tracker FileTracker
			update, facts := tracker.ObserveToolResult(1, "bash", tt.input, tt.content)
			if !strings.HasPrefix(update.LastAction, tt.wantPrefix) {
				t.Errorf("LastAction = %q, want prefix %q", update.LastAction, tt.wantPrefix)
			}
			if update.Path != "" {
				t.Errorf("Path = %q, want empty for bash", update.Path)
			}
			if tt.wantFact && len(facts) == 0 {
				t.Errorf("facts empty, want a test fact")
			}
			if !tt.wantFact && len(facts) != 0 {
				t.Errorf("facts = %v, want none", facts)
			}
		})
	}
}

func TestFileTrackerReadStateAndBehavior(t *testing.T) {
	path := filepath.Join(t.TempDir(), "note.txt")
	if err := os.WriteFile(path, []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	content, _ := json.Marshal(readResult{Path: path, StartLine: 1, EndLine: 2, TotalLines: 2, Output: "one\ntwo\n"})
	base := func() FileTracker { var tracker FileTracker; tracker.RecordRead(5, string(content)); return tracker }
	t.Run("turn count floor and empty path", func(t *testing.T) {
		tracker := base()
		if got := tracker.ReadState(path, 2).TurnsSinceRead; got != 0 {
			t.Fatalf("turns = %d, want 0", got)
		}
		if state := tracker.ReadState(" ", 5); state.Observed || state.TurnsSinceRead != -1 {
			t.Fatalf("empty path state = %+v", state)
		}
	})
	t.Run("mutation", func(t *testing.T) {
		tracker := base()
		tracker.RecordMutation(path)
		if !tracker.ReadState(path, 5).MutatedSinceRead {
			t.Fatal("mutation not reported")
		}
	})
	t.Run("external rewrite", func(t *testing.T) {
		tracker := base()
		if err := os.WriteFile(path, []byte("changed\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if !tracker.ReadState(path, 5).ChangedSinceRead {
			t.Fatal("external rewrite not reported")
		}
	})
	t.Run("clone retained state", func(t *testing.T) {
		tracker := base()
		tracker.BumpGeneration(path)
		clone := tracker.Clone()
		if !clone.WasObserved(path) || !clone.ReadState(path, 7).MutatedSinceRead {
			t.Fatal("clone did not retain read and generation")
		}
	})
	t.Run("working file heuristics", func(t *testing.T) {
		tracker := base()
		if !tracker.WasObserved(path) {
			t.Fatal("read not observed")
		}
		if !tracker.BumpGeneration(path) {
			t.Fatal("generation not bumped")
		}
		update, _ := tracker.ObserveToolResult(6, "custom", nil, "result")
		if !strings.Contains(update.LastAction, "custom") {
			t.Fatalf("generic action = %q", update.LastAction)
		}
		if got := tracker.Summaries(1); len(got) != 1 || !strings.Contains(got[0], path) {
			t.Fatalf("summaries = %v", got)
		}
	})
}

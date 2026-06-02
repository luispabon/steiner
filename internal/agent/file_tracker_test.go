package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type mutationOK struct{}

func (mutationOK) WasMutated() bool { return true }

func TestFileTrackerAnnotatesUnchangedReread(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(path, []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir() error = %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })

	content := `{"path":"note.txt","start_line":1,"end_line":3,"total_lines":3,"output":"one\ntwo\nthree\n"}`
	tracker := FileTracker{}
	first, firstObservation := tracker.ObserveRead(1, content, true)
	if first != content {
		t.Fatalf("first read = %q, want unchanged full content", first)
	}
	if got, want := firstObservation.Reason, "first read"; got != want {
		t.Fatalf("first observation reason = %q, want %q", got, want)
	}

	second, secondObservation := tracker.ObserveRead(3, content, true)
	if !strings.Contains(second, "file unchanged since turn 1") {
		t.Fatalf("second read = %q, want unchanged annotation", second)
	}
	if got, want := secondObservation.Action, "annotated"; got != want {
		t.Fatalf("second observation action = %q, want %q", got, want)
	}
}

func TestFileTrackerFallsBackToFullContentWhenFileChanges(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(path, []byte("one\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir() error = %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })

	content := `{"path":"note.txt","start_line":1,"end_line":1,"total_lines":1,"output":"one\n"}`
	tracker := FileTracker{}
	_, _ = tracker.ObserveRead(1, content, true)
	if err := os.WriteFile(path, []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatalf("rewrite file: %v", err)
	}

	got, observation := tracker.ObserveRead(2, `{"path":"note.txt","start_line":1,"end_line":2,"total_lines":2,"output":"one\ntwo\n"}`, true)
	if strings.Contains(got, "file unchanged since turn") {
		t.Fatalf("changed file reread = %q, want full content", got)
	}
	if got, want := observation.Reason, "range changed"; got != want {
		t.Fatalf("observation reason = %q, want %q", got, want)
	}
}

func TestFileTrackerFallsBackToFullContentWhenGenerationChangesWithoutMtimeChange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(path, []byte("one\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir() error = %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })

	content := `{"path":"note.txt","start_line":1,"end_line":1,"total_lines":1,"output":"one\n"}`
	tracker := FileTracker{}
	if _, observation := tracker.ObserveRead(1, content, true); observation.Reason != "first read" {
		t.Fatalf("first observation reason = %q, want first read", observation.Reason)
	}
	if ok := tracker.BumpGeneration("note.txt"); !ok {
		t.Fatal("BumpGeneration() = false, want true")
	}

	got, observation := tracker.ObserveRead(2, content, true)
	if strings.Contains(got, "file unchanged since turn") {
		t.Fatalf("generation-changed reread = %q, want full content", got)
	}
	if got, want := observation.Reason, "generation changed"; got != want {
		t.Fatalf("observation reason = %q, want %q", got, want)
	}
	if !containsString(observation.Notes, "mtime_unchanged") {
		t.Fatalf("observation notes = %v, want mtime_unchanged marker", observation.Notes)
	}
}

//nolint:gocyclo
func TestFileTrackerBumpGenerationIsFileWide(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(path, []byte("alpha\nbeta\ncharlie\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir() error = %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })

	tracker := FileTracker{}
	first := `{"path":"note.txt","start_line":1,"end_line":1,"total_lines":3,"output":"alpha\n"}`
	second := `{"path":"note.txt","start_line":2,"end_line":2,"total_lines":3,"output":"beta\n"}`

	if got, observation := tracker.ObserveRead(1, first, true); got != first || observation.Reason != "first read" {
		t.Fatalf("first read = %q, reason = %q, want first read", got, observation.Reason)
	}
	if ok := tracker.BumpGeneration("note.txt"); !ok {
		t.Fatal("BumpGeneration() = false, want true")
	}

	got, observation := tracker.ObserveRead(2, second, true)
	if strings.Contains(got, "file unchanged since turn") {
		t.Fatalf("different-range reread = %q, want full content", got)
	}
	if got, want := observation.Reason, "range changed"; got != want {
		t.Fatalf("observation reason = %q, want %q", got, want)
	}

	canonicalPath, ok := normalizeTrackedPath("note.txt")
	if !ok {
		t.Fatal("normalizeTrackedPath() = false, want true")
	}
	if got, want := tracker.reads[canonicalPath].Generation, uint64(1); got != want {
		t.Fatalf("tracked generation = %d, want %d", got, want)
	}
}

func TestFileTrackerPruneBeforeTurn(t *testing.T) {
	dir := t.TempDir()
	pathA := filepath.Join(dir, "a.txt")
	pathB := filepath.Join(dir, "b.txt")
	if err := os.WriteFile(pathA, []byte("aaa\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := os.WriteFile(pathB, []byte("bbb\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir() error = %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })

	tracker := FileTracker{}
	contentA := `{"path":"a.txt","start_line":1,"end_line":1,"total_lines":1,"output":"aaa\n"}`
	contentB := `{"path":"b.txt","start_line":1,"end_line":1,"total_lines":1,"output":"bbb\n"}`

	tracker.ObserveRead(1, contentA, true)
	tracker.ObserveRead(3, contentB, true)

	tracker.PruneBeforeTurn(2)

	// a.txt was read at turn 1 (< 2), should be pruned — next read is full
	gotA, obsA := tracker.ObserveRead(4, contentA, true)
	if obsA.Reason != "first read" {
		t.Fatalf("pruned file observation = %q, want first read", obsA.Reason)
	}
	if gotA != contentA {
		t.Fatalf("pruned file read = %q, want full content", gotA)
	}

	// b.txt was read at turn 3 (>= 2), should survive — next read is annotated
	_, obsB := tracker.ObserveRead(4, contentB, true)
	if obsB.Action != "annotated" {
		t.Fatalf("surviving file observation = %q, want annotated", obsB.Action)
	}
}

func TestFileTrackerSurvivesManagerLifecycle(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(path, []byte("one\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir() error = %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })

	manager := &ContextStateManager{}
	content := `{"path":"note.txt","start_line":1,"end_line":1,"total_lines":1,"output":"one\n"}`
	if got := manager.ObserveToolResult(1, "read", nil, content); got != content {
		t.Fatalf("first manager read = %q, want full content", got)
	}
	if _, err := manager.PrepareTurnState(context.Background(), RunState{TurnCount: 1}); err != nil {
		t.Fatalf("PrepareTurnState() error = %v", err)
	}
	manager.RecordMutation("note.txt")
	got := manager.ObserveToolResult(2, "read", nil, content)
	if strings.Contains(got, "file unchanged since turn 1") {
		t.Fatalf("second manager read after mutation = %q, want full content", got)
	}
	got = manager.ObserveToolResult(3, "read", nil, content)
	if !strings.Contains(got, "file unchanged since turn 2") {
		t.Fatalf("third manager read = %q, want annotation", got)
	}
}

func TestFileTrackerDetectsBashMutation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(path, []byte("original\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	oldWD, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })

	content := `{"path":"note.txt","start_line":1,"end_line":1,"total_lines":1,"output":"original\n"}`
	tracker := FileTracker{}
	tracker.ObserveRead(1, content, true)

	// Simulate bash mutation (no generation bump)
	if err := os.WriteFile(path, []byte("mutated\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Re-read with different content in JSON
	content2 := `{"path":"note.txt","start_line":1,"end_line":1,"total_lines":1,"output":"mutated\n"}`
	got, obs := tracker.ObserveRead(2, content2, true)
	if strings.Contains(got, "file unchanged since turn") {
		t.Fatalf("bash-mutated reread = %q, want full content (not annotation)", got)
	}
	if obs.Reason != "modified file" {
		t.Fatalf("observation reason = %q, want modified file", obs.Reason)
	}
}

func TestFileTrackerInvalidatesAfterMutationKinds(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(path, []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir() error = %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })

	content := `{"path":"note.txt","start_line":1,"end_line":3,"total_lines":3,"output":"one\ntwo\nthree\n"}`

	cm := &ContextStateManager{}
	if got, obs := cm.fileTracker.ObserveRead(1, content, true); got != content || obs.Reason != "first read" {
		t.Fatalf("first read = %q, reason = %q, want full content / first read", got, obs.Reason)
	}

	mutateResult := struct {
		Paths []string `json:"paths"`
		*mutationOK
	}{Paths: []string{"note.txt"}, mutationOK: &mutationOK{}}
	recordMutationForContextManager(cm, "mutate", nil, mutateResult)
	if len(cm.fileTracker.generations) != 1 {
		t.Fatalf("generation entries = %d, want 1 after mutate", len(cm.fileTracker.generations))
	}

	got, obs := cm.fileTracker.ObserveRead(2, content, true)
	if strings.Contains(got, "file unchanged since turn") {
		t.Fatalf("mutated reread = %q, want full content", got)
	}
	if obs.Reason != "generation changed" {
		t.Fatalf("observation reason = %q, want generation changed", obs.Reason)
	}
}

func TestFileTrackerRecordMutation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(path, []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	oldWD, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })

	tracker := FileTracker{}
	if ok := tracker.BumpGeneration("note.txt"); !ok {
		t.Fatal("BumpGeneration returned false")
	}
	canonicalPath, _ := normalizeTrackedPath("note.txt")
	genBefore := tracker.generations[canonicalPath]

	tracker.RecordMutation("note.txt")
	genAfter := tracker.generations[canonicalPath]
	if genAfter != genBefore+1 {
		t.Fatalf("generation after RecordMutation = %d, want %d", genAfter, genBefore+1)
	}
}

func TestFileTrackerObserveToolResultRead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(path, []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	oldWD, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })

	tests := []struct {
		name       string
		content    string
		wantPath   string
		wantFact   string
		wantNoFact bool
	}{
		{
			name:       "full content no annotation",
			content:    `{"path":"note.txt","start_line":1,"end_line":2,"total_lines":2,"output":"one\ntwo\n"}`,
			wantPath:   "note.txt",
			wantNoFact: true,
		},
		{
			name:     "annotation in content",
			content:  fmt.Sprintf(`{"path":"note.txt","start_line":1,"end_line":2,"total_lines":2,"output":"%s"}`, "[file unchanged since turn 1: lines 1-2 of 2 in note.txt]"),
			wantPath: "note.txt",
			wantFact: "read annotation:",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tracker := FileTracker{}
			update, facts := tracker.ObserveToolResult(1, "read", nil, tc.content)
			if update.Path != tc.wantPath {
				t.Errorf("update.Path = %q, want %q", update.Path, tc.wantPath)
			}
			if tc.wantNoFact && len(facts) != 0 {
				t.Errorf("facts = %v, want none", facts)
			}
			if tc.wantFact != "" {
				found := false
				for _, f := range facts {
					if strings.Contains(f, tc.wantFact) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("facts = %v, want one containing %q", facts, tc.wantFact)
				}
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
			content:    `{"exit_code":0,"output":"file.go","truncated":false,"message":""}`,
			wantPrefix: "bash:",
			wantFact:   false,
		},
		{
			name:       "test command produces fact",
			input:      map[string]any{"command": "go test ./..."},
			content:    `{"exit_code":0,"output":"ok","truncated":false,"message":""}`,
			wantPrefix: "bash:",
			wantFact:   true,
		},
		{
			name:       "failed test produces fact",
			input:      map[string]any{"command": "go test ./..."},
			content:    `{"exit_code":1,"output":"FAIL","truncated":false,"message":""}`,
			wantPrefix: "bash:",
			wantFact:   true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tracker := FileTracker{}
			update, facts := tracker.ObserveToolResult(1, "bash", tc.input, tc.content)
			if !strings.HasPrefix(update.LastAction, tc.wantPrefix) {
				t.Errorf("LastAction = %q, want prefix %q", update.LastAction, tc.wantPrefix)
			}
			if update.Path != "" {
				t.Errorf("Path = %q, want empty for bash", update.Path)
			}
			if tc.wantFact && len(facts) == 0 {
				t.Errorf("facts empty, want test fact")
			}
			if !tc.wantFact && len(facts) != 0 {
				t.Errorf("facts = %v, want none", facts)
			}
		})
	}
}

func TestFileTrackerObserveToolResultGeneric(t *testing.T) {
	tracker := FileTracker{}
	update, facts := tracker.ObserveToolResult(1, "glob", nil, `{"matches":["a.go","b.go"]}`)
	if !strings.HasPrefix(update.LastAction, "glob:") {
		t.Errorf("LastAction = %q, want prefix glob:", update.LastAction)
	}
	if update.Path != "" {
		t.Errorf("Path = %q, want empty for generic tool", update.Path)
	}
	if len(facts) != 0 {
		t.Errorf("facts = %v, want none", facts)
	}
}

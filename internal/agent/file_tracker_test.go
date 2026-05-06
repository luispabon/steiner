package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	time.Sleep(10 * time.Millisecond)
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

	manager := &SmartContextManager{}
	content := `{"path":"note.txt","start_line":1,"end_line":1,"total_lines":1,"output":"one\n"}`
	if got := manager.IngestToolResult(1, "read", content); got != content {
		t.Fatalf("first manager read = %q, want full content", got)
	}
	if _, err := manager.PreAssembly(nil, RunState{TurnCount: 1}); err != nil {
		t.Fatalf("PreAssembly() error = %v", err)
	}
	manager.RecordMutation("note.txt")
	got := manager.IngestToolResult(2, "read", content)
	if strings.Contains(got, "file unchanged since turn 1") {
		t.Fatalf("second manager read after mutation = %q, want full content", got)
	}
	got = manager.IngestToolResult(3, "read", content)
	if !strings.Contains(got, "file unchanged since turn 2") {
		t.Fatalf("third manager read = %q, want annotation", got)
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

	tests := []string{"edit", "write", "apply_patch"}
	for _, toolName := range tests {
		t.Run(toolName, func(t *testing.T) {
			cm := &SmartContextManager{}
			if got, obs := cm.fileTracker.ObserveRead(1, content, true); got != content || obs.Reason != "first read" {
				t.Fatalf("first read = %q, reason = %q, want full content / first read", got, obs.Reason)
			}

			recordMutationForContextManager(cm, toolName, map[string]any{"path": "note.txt"}, mutationOK{})
			if len(cm.fileTracker.generations) != 1 {
				t.Fatalf("generation entries = %d, want 1 after %s", len(cm.fileTracker.generations), toolName)
			}

			got, obs := cm.fileTracker.ObserveRead(2, content, true)
			if strings.Contains(got, "file unchanged since turn") {
				t.Fatalf("mutated reread = %q, want full content", got)
			}
			if obs.Reason != "generation changed" {
				t.Fatalf("observation reason = %q, want generation changed", obs.Reason)
			}
		})
	}
}

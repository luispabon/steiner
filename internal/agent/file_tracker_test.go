package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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

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
	first := tracker.ObserveRead(1, content, true)
	if first != content {
		t.Fatalf("first read = %q, want unchanged full content", first)
	}

	second := tracker.ObserveRead(3, content, true)
	if !strings.Contains(second, "file unchanged since turn 1") {
		t.Fatalf("second read = %q, want unchanged annotation", second)
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
	_ = tracker.ObserveRead(1, content, true)
	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(path, []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatalf("rewrite file: %v", err)
	}

	got := tracker.ObserveRead(2, `{"path":"note.txt","start_line":1,"end_line":2,"total_lines":2,"output":"one\ntwo\n"}`, true)
	if strings.Contains(got, "file unchanged since turn") {
		t.Fatalf("changed file reread = %q, want full content", got)
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
	got := manager.IngestToolResult(2, "read", content)
	if !strings.Contains(got, "file unchanged since turn 1") {
		t.Fatalf("second manager read = %q, want annotation", got)
	}
}

package tui

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/tui/theme"
)

func TestDetectGitSnapshotIncludesModifiedFiles(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init")
	runGit(t, repo, "config", "user.name", "Test User")
	runGit(t, repo, "config", "user.email", "test@example.com")

	path := filepath.Join(repo, "note.txt")
	if err := os.WriteFile(path, []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "note.txt")
	runGit(t, repo, "commit", "-m", "init")

	if err := os.WriteFile(path, []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	snap := detectGitSnapshot(context.Background(), repo)
	if !snap.ready {
		t.Fatal("snapshot.ready = false, want true")
	}
	if !snap.dirty {
		t.Fatal("snapshot.dirty = false, want true")
	}
	if got, want := len(snap.modifiedFiles), 1; got != want {
		t.Fatalf("len(snapshot.modifiedFiles) = %d, want %d", got, want)
	}
	if got, want := snap.modifiedFiles[0].Path, "note.txt"; got != want {
		t.Fatalf("modified path = %q, want %q", got, want)
	}
	if got, want := snap.modifiedFiles[0].Added, 1; got != want {
		t.Fatalf("added = %d, want %d", got, want)
	}
	if got, want := snap.modifiedFiles[0].Deleted, 0; got != want {
		t.Fatalf("deleted = %d, want %d", got, want)
	}
}

func TestSidebarLinesIncludeModifiedFilesSection(t *testing.T) {
	styles := theme.Default().LipGlossStyles()
	sidebar := sidebarState{
		workingDir: "/tmp/project",
		branch:     "main",
		dirty:      true,
		modifiedFiles: []gitModifiedFile{
			{Path: "internal/tui/model_test.go", Added: 11},
			{Path: "internal/tui/sidebar.go", Deleted: 3},
		},
		styles: styles,
	}

	joined := strings.Join(sidebar.lines(38), "\n")
	for _, want := range []string{
		"Modified files:",
		"internal/tui/model_test.go",
		"+11",
		"internal/tui/sidebar.go",
		"-3",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("sidebar = %q, want %q", joined, want)
		}
	}
	if strings.Contains(joined, "Skills") {
		t.Fatalf("sidebar = %q, want no Skills section", joined)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
}

package tui

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDetectGitSnapshotDoesNotRewriteIndex(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	runGit(t, repo, "init", "-b", "main")
	runGit(t, repo, "config", "user.name", "Test User")
	runGit(t, repo, "config", "user.email", "test@example.com")

	names := []string{"a.txt", "b.txt", "c.txt"}
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(repo, name), []byte(name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-m", "init")

	future := time.Now().Add(time.Hour)
	for _, name := range names {
		if err := os.Chtimes(filepath.Join(repo, name), future, future); err != nil {
			t.Fatal(err)
		}
	}

	indexPath := filepath.Join(repo, ".git", "index")
	before, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}

	snap := detectGitSnapshot(context.Background(), repo, func(err error) { t.Errorf("unexpected error: %v", err) })
	if !snap.ready {
		t.Fatal("snapshot.ready = false, want true")
	}

	after, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("git status rewrote .git/index; detection must not take optional locks")
	}
}

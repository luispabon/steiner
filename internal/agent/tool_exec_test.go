package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteTargetExistedBefore(t *testing.T) {
	dir := t.TempDir()
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir() error = %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(oldWD)
	})

	existingPath := filepath.Join(dir, "existing.txt")
	if err := os.WriteFile(existingPath, []byte("ok"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if got := writeTargetExistedBefore("read", map[string]any{"path": "existing.txt"}); got != nil {
		t.Fatalf("non-write tool existence = %v, want nil", got)
	}

	existing := writeTargetExistedBefore("write", map[string]any{"path": "existing.txt"})
	if existing == nil || !*existing {
		t.Fatalf("existing relative target = %v, want true", existing)
	}

	missing := writeTargetExistedBefore("write", map[string]any{"path": "missing.txt"})
	if missing == nil || *missing {
		t.Fatalf("missing relative target = %v, want false", missing)
	}
}

func TestRecordMutationForContextManager(t *testing.T) {
	cm := &SmartContextManager{}

	recordMutationForContextManager(cm, "read", map[string]any{"path": "note.txt"})
	if got := cm.fileTracker.generations; len(got) != 0 {
		t.Fatalf("read generations = %v, want empty", got)
	}

	recordMutationForContextManager(cm, "write", map[string]any{"path": "note.txt"})
	if got := len(cm.fileTracker.generations); got != 1 {
		t.Fatalf("generation entries = %d, want 1", got)
	}

	recordMutationForContextManager(cm, "edit", map[string]any{"path": "note.txt"})
	for _, generation := range cm.fileTracker.generations {
		if generation != 2 {
			t.Fatalf("generation = %d, want 2", generation)
		}
	}
}

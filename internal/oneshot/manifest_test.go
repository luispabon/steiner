package oneshot

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestManifestStoreReadWrite(t *testing.T) {
	dir := t.TempDir()
	store := NewManifestStore(filepath.Join(dir, "run.json"))

	manifest := Manifest{
		RunID:        "abc123",
		Slug:         "build-parser",
		Task:         "Build the parser",
		Branch:       "oneshot/build-parser-abc123",
		WorktreePath: filepath.Join(dir, "worktree"),
		ModelSnapshot: ModelSnapshot{
			DefaultModel: "gpt-4.1",
			PhaseModels: map[Phase]string{
				PhasePlan:      "gpt-4.1",
				PhaseImplement: "gpt-4.1",
			},
		},
		CurrentPhase: PhasePlan,
		PhaseStatuses: map[Phase]PhaseStatus{
			PhasePlan: PhaseStatusRunning,
		},
		PhaseSessionIDs: map[Phase]string{
			PhasePlan: "session-1",
		},
		CommitMilestones: []CommitMilestone{
			{Phase: PhasePlan, Commit: "deadbeef", Message: "plan", RecordedAt: time.Unix(10, 0).UTC()},
		},
	}

	if err := store.Write(manifest); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	loaded, err := store.Read()
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if loaded.RunID != manifest.RunID || loaded.Slug != manifest.Slug || loaded.Branch != manifest.Branch {
		t.Fatalf("loaded manifest mismatch: %#v", loaded)
	}
	if got, want := loaded.PhaseStatuses[PhasePlan], PhaseStatusRunning; got != want {
		t.Fatalf("PhaseStatuses[plan] = %q, want %q", got, want)
	}
	if got, want := loaded.PhaseSessionIDs[PhasePlan], "session-1"; got != want {
		t.Fatalf("PhaseSessionIDs[plan] = %q, want %q", got, want)
	}

	if _, err := os.Stat(filepath.Join(dir, "run.json")); err != nil {
		t.Fatalf("manifest file missing: %v", err)
	}
}

func TestManifestStoreReadLegacyManifest(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "legacy.json")
	legacy := `{"run_id":"abc123","slug":"legacy","task":"legacy task","branch":"oneshot/legacy-abc123","worktree_path":"/tmp/worktree","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}`
	if err := os.WriteFile(path, []byte(legacy), 0o644); err != nil {
		t.Fatalf("write legacy manifest: %v", err)
	}

	manifest, err := NewManifestStore(path).Read()
	if err != nil {
		t.Fatalf("Read legacy manifest failed: %v", err)
	}
	if manifest.WorktreeBase != "" {
		t.Fatalf("WorktreeBase = %q, want empty", manifest.WorktreeBase)
	}
}

func TestManifestStoreReadNonexistent(t *testing.T) {
	dir := t.TempDir()
	store := NewManifestStore(filepath.Join(dir, "missing.json"))

	if _, err := store.Read(); err == nil {
		t.Fatal("Read on nonexistent path: expected error, got nil")
	}
}

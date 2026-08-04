package oneshot

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestManifestStoreReadWriteUpdate(t *testing.T) {
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

	if err := store.Update(func(m *Manifest) error {
		m.CurrentPhase = PhaseImplement
		m.PhaseStatuses[PhasePlan] = PhaseStatusDone
		m.PhaseStatuses[PhaseImplement] = PhaseStatusRunning
		m.PhaseSessionIDs[PhaseImplement] = "session-2"
		m.CommitMilestones = append(m.CommitMilestones, CommitMilestone{
			Phase:      PhaseImplement,
			Commit:     "cafebabe",
			Message:    "implement",
			RecordedAt: time.Unix(20, 0).UTC(),
		})
		return nil
	}); err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	updated, err := store.Read()
	if err != nil {
		t.Fatalf("Read after update failed: %v", err)
	}
	if got, want := updated.CurrentPhase, PhaseImplement; got != want {
		t.Fatalf("CurrentPhase = %q, want %q", got, want)
	}
	if got, want := updated.PhaseStatuses[PhasePlan], PhaseStatusDone; got != want {
		t.Fatalf("updated PhaseStatuses[plan] = %q, want %q", got, want)
	}
	if got, want := updated.PhaseStatuses[PhaseImplement], PhaseStatusRunning; got != want {
		t.Fatalf("updated PhaseStatuses[implement] = %q, want %q", got, want)
	}
	if got, want := len(updated.CommitMilestones), 2; got != want {
		t.Fatalf("CommitMilestones len = %d, want %d", got, want)
	}
	if _, err := os.Stat(filepath.Join(dir, "run.json")); err != nil {
		t.Fatalf("manifest file missing: %v", err)
	}
}

func TestManifestStoreReadNonexistent(t *testing.T) {
	dir := t.TempDir()
	store := NewManifestStore(filepath.Join(dir, "missing.json"))

	if _, err := store.Read(); err == nil {
		t.Fatal("Read on nonexistent path: expected error, got nil")
	}
}

// TestManifestStoreUpdateNonexistentInitializes documents the deliberate
// divergence from Read: Update's internal readLocked tolerates a missing
// file (os.IsNotExist) and starts from a zero Manifest instead of failing.
func TestManifestStoreUpdateNonexistentInitializes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "missing.json")
	store := NewManifestStore(path)

	if err := store.Update(func(m *Manifest) error {
		m.RunID = "from-update"
		return nil
	}); err != nil {
		t.Fatalf("Update on nonexistent path: %v", err)
	}

	loaded, err := store.Read()
	if err != nil {
		t.Fatalf("Read after Update: %v", err)
	}
	if got, want := loaded.RunID, "from-update"; got != want {
		t.Fatalf("RunID = %q, want %q", got, want)
	}
}

func TestManifestStoreReadMalformedJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "malformed.json")
	if err := os.WriteFile(path, []byte("not json"), 0o644); err != nil {
		t.Fatalf("write malformed file: %v", err)
	}
	store := NewManifestStore(path)

	if _, err := store.Read(); err == nil {
		t.Fatal("Read on malformed JSON: expected error, got nil")
	}
}

func TestManifestStoreUpdateMalformedJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "malformed.json")
	if err := os.WriteFile(path, []byte("not json"), 0o644); err != nil {
		t.Fatalf("write malformed file: %v", err)
	}
	store := NewManifestStore(path)

	err := store.Update(func(m *Manifest) error {
		m.RunID = "should-not-apply"
		return nil
	})
	if err == nil {
		t.Fatal("Update on malformed JSON: expected error, got nil")
	}
}

func TestManifestStoreConcurrentUpdates(t *testing.T) {
	dir := t.TempDir()
	store := NewManifestStore(filepath.Join(dir, "run.json"))

	if err := store.Write(Manifest{RunID: "concurrent"}); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	n := 20
	var wg sync.WaitGroup
	wg.Add(n)

	for i := 0; i < n; i++ {
		go func(idx int) {
			defer wg.Done()
			_ = store.Update(func(m *Manifest) error {
				m.CommitMilestones = append(m.CommitMilestones, CommitMilestone{
					Phase:  PhaseImplement,
					Commit: fmt.Sprintf("commit-%d", idx),
				})
				return nil
			})
		}(i)
	}

	wg.Wait()

	loaded, err := store.Read()
	if err != nil {
		t.Fatalf("Read after concurrent updates: %v", err)
	}
	if got, want := len(loaded.CommitMilestones), n; got != want {
		t.Fatalf("CommitMilestones len = %d, want %d (a lock defect would lose concurrent updates)", got, want)
	}
}

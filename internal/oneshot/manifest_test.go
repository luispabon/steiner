package oneshot

import (
	"os"
	"path/filepath"
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

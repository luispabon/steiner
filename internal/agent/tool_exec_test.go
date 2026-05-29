package agent

import (
	"testing"
)

type failedMutation struct{}

func (f *failedMutation) WasMutated() bool { return false }

type successMutation struct{}

func (s *successMutation) WasMutated() bool { return true }

func TestRecordMutationForContextManager(t *testing.T) {
	cm := &SmartContextManager{}

	recordMutationForContextManager(cm, "read", map[string]any{"path": "note.txt"}, &successMutation{})
	if got := cm.fileTracker.generations; len(got) != 0 {
		t.Fatalf("read generations = %v, want empty", got)
	}

	recordMutationForContextManager(cm, "mutate", nil, struct {
		Paths []string `json:"paths"`
	}{Paths: []string{"note.txt", "other.txt"}})
	notePath, _ := normalizeTrackedPath("note.txt")
	otherPath, _ := normalizeTrackedPath("other.txt")
	if got, want := cm.fileTracker.generations[notePath], uint64(1); got != want {
		t.Fatalf("note generation after mutate = %d, want %d", got, want)
	}
	if got, want := cm.fileTracker.generations[otherPath], uint64(1); got != want {
		t.Fatalf("other generation after mutate = %d, want %d", got, want)
	}

	t.Run("mutate with failed mutation does not bump generation", func(t *testing.T) {
		cm := &SmartContextManager{}
		result := struct {
			Paths []string `json:"paths"`
			*failedMutation
		}{Paths: []string{"note.txt"}, failedMutation: &failedMutation{}}
		recordMutationForContextManager(cm, "mutate", nil, result)
		if got := len(cm.fileTracker.generations); got != 0 {
			t.Fatalf("generation entries after failed mutate = %d, want 0", got)
		}
	})
}

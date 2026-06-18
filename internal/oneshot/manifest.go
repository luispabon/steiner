package oneshot

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Phase identifies a top-level oneshot phase.
type Phase string

// Phase constants identify the three ordered oneshot phases.
const (
	PhasePlan      Phase = "plan"
	PhaseImplement Phase = "implement"
	PhaseReview    Phase = "review"
)

// PhaseStatus captures the lifecycle state of a phase.
type PhaseStatus string

// PhaseStatus constants describe the lifecycle state of a phase within a run.
const (
	PhaseStatusPending PhaseStatus = "pending"
	PhaseStatusRunning PhaseStatus = "running"
	PhaseStatusDone    PhaseStatus = "done"
	PhaseStatusFailed  PhaseStatus = "failed"
	PhaseStatusSkipped PhaseStatus = "skipped"
)

// ModelSnapshot captures the model aliases used for a run.
type ModelSnapshot struct {
	DefaultModel string           `json:"default_model,omitempty"`
	PhaseModels  map[Phase]string `json:"phase_models,omitempty"`
}

// CommitMilestone records a commit boundary reached during the run.
type CommitMilestone struct {
	Phase      Phase     `json:"phase"`
	Commit     string    `json:"commit"`
	Message    string    `json:"message,omitempty"`
	RecordedAt time.Time `json:"recorded_at"`
}

// Manifest is the durable on-disk record for a oneshot run.
type Manifest struct {
	RunID            string                `json:"run_id"`
	Slug             string                `json:"slug"`
	Task             string                `json:"task"`
	Branch           string                `json:"branch"`
	WorktreePath     string                `json:"worktree_path"`
	ModelSnapshot    ModelSnapshot         `json:"model_snapshot"`
	CurrentPhase     Phase                 `json:"current_phase,omitempty"`
	PhaseStatuses    map[Phase]PhaseStatus `json:"phase_statuses,omitempty"`
	PhaseSessionIDs  map[Phase]string      `json:"phase_session_ids,omitempty"`
	CommitMilestones []CommitMilestone     `json:"commit_milestones,omitempty"`
	CreatedAt        time.Time             `json:"created_at"`
	UpdatedAt        time.Time             `json:"updated_at"`
}

// ManifestStore reads and writes a manifest atomically.
type ManifestStore struct {
	path string
	mu   sync.Mutex
}

// NewManifestStore constructs a store for a manifest path.
func NewManifestStore(path string) *ManifestStore {
	return &ManifestStore{path: path}
}

// Read loads the manifest from disk.
func (s *ManifestStore) Read() (Manifest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.path)
	if err != nil {
		return Manifest{}, fmt.Errorf("read manifest: %w", err)
	}

	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return Manifest{}, fmt.Errorf("unmarshal manifest: %w", err)
	}
	ensureManifestMaps(&manifest)
	return manifest, nil
}

// Write stores the manifest atomically.
func (s *ManifestStore) Write(manifest Manifest) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	stampManifest(&manifest)
	return writeManifestAtomic(s.path, manifest)
}

// Update loads the manifest, applies the callback, and writes it back atomically.
func (s *ManifestStore) Update(update func(*Manifest) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	manifest, err := s.readLocked()
	if err != nil {
		return err
	}
	if err := update(&manifest); err != nil {
		return err
	}
	stampManifest(&manifest)
	return writeManifestAtomic(s.path, manifest)
}

func (s *ManifestStore) readLocked() (Manifest, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return Manifest{}, nil
		}
		return Manifest{}, fmt.Errorf("read manifest: %w", err)
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return Manifest{}, fmt.Errorf("unmarshal manifest: %w", err)
	}
	return manifest, nil
}

func ensureManifestMaps(manifest *Manifest) {
	if manifest.PhaseStatuses == nil {
		manifest.PhaseStatuses = map[Phase]PhaseStatus{}
	}
	if manifest.PhaseSessionIDs == nil {
		manifest.PhaseSessionIDs = map[Phase]string{}
	}
	if manifest.ModelSnapshot.PhaseModels == nil {
		manifest.ModelSnapshot.PhaseModels = map[Phase]string{}
	}
}

func stampManifest(manifest *Manifest) {
	now := time.Now().UTC()
	if manifest.CreatedAt.IsZero() {
		manifest.CreatedAt = now
	}
	manifest.UpdatedAt = now
	ensureManifestMaps(manifest)
}

func writeManifestAtomic(path string, manifest Manifest) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create manifest directory: %w", err)
	}

	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-manifest-*")
	if err != nil {
		return fmt.Errorf("create manifest temp file: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("write manifest temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("close manifest temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("rename manifest temp file: %w", err)
	}
	return nil
}

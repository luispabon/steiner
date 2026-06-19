package oneshot

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const resumableLockStaleAfter = defaultLockStaleAfter

// ResumableRun describes a manifest-backed run that can be resumed.
type ResumableRun struct {
	RunID         string                `json:"run_id"`
	Slug          string                `json:"slug,omitempty"`
	Task          string                `json:"task,omitempty"`
	Branch        string                `json:"branch,omitempty"`
	WorktreePath  string                `json:"worktree_path,omitempty"`
	ResumePhase   Phase                 `json:"resume_phase"`
	PhaseStatuses map[Phase]PhaseStatus `json:"phase_statuses,omitempty"`
	LockState     string                `json:"lock_state"`
	Status        string                `json:"status"`
	CreatedAt     time.Time             `json:"created_at,omitempty"`
	UpdatedAt     time.Time             `json:"updated_at,omitempty"`
}

// ListRuns returns all resumable runs discovered under the oneshot state directory.
func ListRuns(projectRoot string) ([]ResumableRun, error) {
	return ListResumableRuns(projectRoot)
}

// ListResumableRuns returns resumable runs discovered under the oneshot state directory.
func ListResumableRuns(projectRoot string) ([]ResumableRun, error) {
	stateDir := filepath.Join(projectRoot, steinerDirName, oneshotStateDirName)
	entries, err := os.ReadDir(stateDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read oneshot state dir: %w", err)
	}

	runs := make([]ResumableRun, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		manifestPath := filepath.Join(stateDir, entry.Name(), "run.json")
		manifest, err := readManifestFile(manifestPath)
		if err != nil {
			return nil, err
		}

		resumePhase, ok := firstIncompletePhase(manifest)
		if !ok {
			continue
		}

		identity := RunIdentity{ID: manifest.RunID, Slug: manifest.Slug}
		lockState, err := resumableRunLockState(identity.LockPath(projectRoot))
		if err != nil {
			return nil, err
		}
		if lockState == lockStateLive {
			continue
		}

		runs = append(runs, ResumableRun{
			RunID:         strings.TrimSpace(manifest.RunID),
			Slug:          strings.TrimSpace(manifest.Slug),
			Task:          strings.TrimSpace(manifest.Task),
			Branch:        pickFirst(strings.TrimSpace(manifest.Branch), identity.BranchName()),
			WorktreePath:  strings.TrimSpace(manifest.WorktreePath),
			ResumePhase:   resumePhase,
			PhaseStatuses: clonePhaseStatuses(manifest.PhaseStatuses),
			LockState:     lockState.String(),
			Status:        resumableRunStatus(resumePhase, lockState),
			CreatedAt:     manifest.CreatedAt,
			UpdatedAt:     manifest.UpdatedAt,
		})
	}

	sort.SliceStable(runs, func(i, j int) bool {
		if runs[i].UpdatedAt.Equal(runs[j].UpdatedAt) {
			return runs[i].RunID > runs[j].RunID
		}
		return runs[i].UpdatedAt.After(runs[j].UpdatedAt)
	})

	return runs, nil
}

type lockState string

const (
	lockStateAbsent lockState = "absent"
	lockStateLive   lockState = "live"
	lockStateStale  lockState = "stale"
)

func (s lockState) String() string {
	return string(s)
}

func readManifestFile(path string) (Manifest, error) {
	data, err := os.ReadFile(path)
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

func resumableRunLockState(path string) (lockState, error) {
	record, info, err := readLockRecord(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return lockStateAbsent, nil
		}
		return lockStateAbsent, err
	}
	if isLockStale(record, info, resumableLockStaleAfter) {
		return lockStateStale, nil
	}
	return lockStateLive, nil
}

func resumableRunStatus(phase Phase, state lockState) string {
	switch state {
	case lockStateStale:
		return fmt.Sprintf("resume at %s (stale lock)", phase)
	default:
		return fmt.Sprintf("resume at %s", phase)
	}
}

func clonePhaseStatuses(statuses map[Phase]PhaseStatus) map[Phase]PhaseStatus {
	if len(statuses) == 0 {
		return map[Phase]PhaseStatus{}
	}
	out := make(map[Phase]PhaseStatus, len(statuses))
	for phase, status := range statuses {
		out[phase] = status
	}
	return out
}

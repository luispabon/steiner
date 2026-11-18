package diagnostics

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"

	"github.com/luispabon/steiner/internal/fsatomic"
)

// Rotation is a transaction rather than a multi-rename sequence. The marker
// records the phase and the generations the rotation owns, so a process killed
// mid-rotation leaves enough behind for the next holder of the stream lock to
// finish it instead of interleaving with half-shifted generations.
const (
	// rotationMarkerVersion is the marker layout this build understands.
	rotationMarkerVersion = 1
	// rotationPhaseStaging means sources may still be moving to stage paths.
	rotationPhaseStaging = "staging"
	// rotationPhaseInstalling means every source reached its stage path and the
	// stages may be moving to their destinations.
	rotationPhaseInstalling = "installing"
	// rotationMarkerSuffix names the marker file beside its stream.
	rotationMarkerSuffix = ".rotate.json"
	// rotationStageSuffix names the temporary stage of one generation, so a
	// stage never collides with a live generation name.
	rotationStageSuffix = ".rot"
)

// rotationMarker is the on-disk record of an in-flight rotation. Generations
// lists every generation the transaction owns, including the active file as
// generation 0.
type rotationMarker struct {
	Version     int    `json:"version"`
	Phase       string `json:"phase"`
	Generations []int  `json:"generations"`
}

// renameFile and removeFile are os.Rename and os.Remove. Tests replace them to
// inject rotation failures.
var (
	renameFile = os.Rename
	removeFile = os.Remove
)

// rotationMarkerPath names the marker that guards path's rotation.
func rotationMarkerPath(path string) string {
	return path + rotationMarkerSuffix
}

// rotationStagePath names the stage holding one generation during a rotation.
func rotationStagePath(path string, gen int) string {
	if gen == 0 {
		return path + rotationStageSuffix
	}
	return fmt.Sprintf("%s.%d%s", path, gen, rotationStageSuffix)
}

// generationPath names one generation of a stream, where generation 0 is the
// active file.
func generationPath(path string, gen int) string {
	if gen == 0 {
		return path
	}
	return fmt.Sprintf("%s.%d", path, gen)
}

// rotationDestPath names where a staged generation is installed. Generations
// shift up by one and the active file becomes generation 1.
func rotationDestPath(path string, gen int) string {
	return generationPath(path, gen+1)
}

// writeRotationMarker replaces the marker for path with m. The marker write is
// atomic, so a reader sees either the previous marker or the new one.
func writeRotationMarker(path string, m rotationMarker) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("write rotation marker: %w", err)
	}
	data = append(data, '\n')
	if err := fsatomic.WriteFile(rotationMarkerPath(path), data, 0o600); err != nil {
		return fmt.Errorf("write rotation marker: %w", err)
	}
	return nil
}

// readRotationMarker returns the marker for path, reporting false when there is
// none. A marker this build cannot act on is an error: guessing at its state
// could move a generation twice.
func readRotationMarker(path string) (rotationMarker, bool, error) {
	data, err := os.ReadFile(rotationMarkerPath(path))
	if errors.Is(err, os.ErrNotExist) {
		return rotationMarker{}, false, nil
	}
	if err != nil {
		return rotationMarker{}, false, fmt.Errorf("read rotation marker: %w", err)
	}
	var m rotationMarker
	if err := json.Unmarshal(data, &m); err != nil {
		return rotationMarker{}, false, fmt.Errorf("read rotation marker: %w", err)
	}
	if m.Version != rotationMarkerVersion {
		return rotationMarker{}, false, fmt.Errorf("read rotation marker: unsupported version %d", m.Version)
	}
	if m.Phase != rotationPhaseStaging && m.Phase != rotationPhaseInstalling {
		return rotationMarker{}, false, fmt.Errorf("read rotation marker: unknown phase %q", m.Phase)
	}
	return m, true, nil
}

// existingGenerations lists the generations of path that exist, ascending, with
// generation 0 first when the active file is present.
func existingGenerations(path string) ([]int, error) {
	var gens []int
	for gen := 0; gen <= maxStreamGenerations; gen++ {
		exists, err := pathExists(generationPath(path, gen))
		if err != nil {
			return nil, fmt.Errorf("inspect diagnostics generation %d: %w", gen, err)
		}
		if exists {
			gens = append(gens, gen)
		}
	}
	return gens, nil
}

// pathExists reports whether path names an existing file or directory. Errors
// other than a missing path are returned, because they leave the rotation state
// unknown.
func pathExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

// recoverRotation completes an interrupted rotation of path. The marker, not a
// rename sequence, says how far the transaction got, so repeating a recovery is
// safe: an already staged source and an already installed stage are both
// skipped. The active file is left for the caller to open. Caller must hold the
// stream lock.
func recoverRotation(path string) error {
	m, ok, err := readRotationMarker(path)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	if m.Phase == rotationPhaseStaging {
		if _, err := stageRotation(path, m); err != nil {
			return err
		}
		m.Phase = rotationPhaseInstalling
		if err := writeRotationMarker(path, m); err != nil {
			return err
		}
	}
	return completeRotation(path, m)
}

// abandonRotation drops the marker of a transaction that moved nothing, so a
// blocked rotation leaves the stream appending past the cap under the deferral
// policy instead of leaving every later record to a recovery that fails the same
// way. It returns cause unchanged. Caller must hold the stream lock.
func abandonRotation(path string, cause error) error {
	if err := removeRotationMarker(path); err != nil {
		return fmt.Errorf("%w; %w", cause, err)
	}
	return cause
}

// removeRotationMarker deletes the marker for path. A marker that is already
// gone is not an error. Caller must hold the stream lock.
func removeRotationMarker(path string) error {
	if err := removeFile(rotationMarkerPath(path)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove rotation marker: %w", err)
	}
	return nil
}

// stageRotation moves every generation the marker lists to its stage path and
// reports how many it moved. An empty source position that is already staged is
// skipped, so a repeated staging pass is idempotent. Caller must hold the stream
// lock.
func stageRotation(path string, m rotationMarker) (int, error) {
	moved := 0
	for _, gen := range m.Generations {
		src := generationPath(path, gen)
		stage := rotationStagePath(path, gen)
		srcExists, err := pathExists(src)
		if err != nil {
			return moved, fmt.Errorf("recover diagnostics rotation: inspect generation %d: %w", gen, err)
		}
		stageExists, err := pathExists(stage)
		if err != nil {
			return moved, fmt.Errorf("recover diagnostics rotation: inspect generation %d stage: %w", gen, err)
		}
		switch {
		case stageExists && srcExists:
			return moved, fmt.Errorf("recover diagnostics rotation: generation %d has both source and stage", gen)
		case stageExists:
			// Already staged by an earlier attempt.
			continue
		case !srcExists:
			return moved, fmt.Errorf("recover diagnostics rotation: generation %d is missing", gen)
		}
		if err := renameFile(src, stage); err != nil {
			return moved, fmt.Errorf("recover diagnostics rotation: stage generation %d: %w", gen, err)
		}
		moved++
	}
	return moved, nil
}

// installRotation moves staged generations to their destinations and discards
// the oldest one, highest generation first and the active file last, so no
// installation overwrites a stage another step still needs. An installed stage
// is skipped, which makes a repeated installation idempotent. Caller must hold
// the stream lock.
func installRotation(path string, m rotationMarker) error {
	gens := make([]int, 0, len(m.Generations))
	for _, gen := range m.Generations {
		if gen != maxStreamGenerations {
			gens = append(gens, gen)
		}
	}
	slices.Sort(gens)
	slices.Reverse(gens)
	for _, gen := range gens {
		if err := installGeneration(path, gen); err != nil {
			return err
		}
	}
	if !slices.Contains(m.Generations, maxStreamGenerations) {
		return nil
	}
	// The oldest generation falls off the end of the policy, so it is dropped
	// only after every generation that survives is installed.
	if err := removeFile(rotationStagePath(path, maxStreamGenerations)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("recover diagnostics rotation: discard generation %d: %w", maxStreamGenerations, err)
	}
	return nil
}

// installGeneration moves one staged generation to the position its successor
// vacated. A generation already at its destination is skipped. Caller must hold
// the stream lock.
func installGeneration(path string, gen int) error {
	stage := rotationStagePath(path, gen)
	dest := rotationDestPath(path, gen)
	stageExists, err := pathExists(stage)
	if err != nil {
		return fmt.Errorf("recover diagnostics rotation: inspect generation %d stage: %w", gen, err)
	}
	destExists, err := pathExists(dest)
	if err != nil {
		return fmt.Errorf("recover diagnostics rotation: inspect generation %d destination: %w", gen, err)
	}
	switch {
	case stageExists && !destExists:
		if err := renameFile(stage, dest); err != nil {
			return fmt.Errorf("recover diagnostics rotation: install generation %d: %w", gen, err)
		}
	case destExists && !stageExists:
		// Already installed by an earlier attempt.
	case stageExists:
		return fmt.Errorf("recover diagnostics rotation: generation %d has both stage and destination", gen)
	default:
		return fmt.Errorf("recover diagnostics rotation: generation %d is missing its stage and destination", gen)
	}
	return nil
}

// completeRotation installs the staged generations and removes the marker, the
// two steps a recovery and a live rotation share so neither path can drift.
// Caller must hold the stream lock.
func completeRotation(path string, m rotationMarker) error {
	if err := installRotation(path, m); err != nil {
		return err
	}
	return removeRotationMarker(path)
}

// rotateStream rotates s through the marker transaction: it recovers any
// interrupted rotation, records the generations it owns, stages them, installs
// them and opens a fresh active file. A failure before the transaction moves a
// single generation returns after deferRotation, so the stream keeps appending
// past the cap instead of dropping every later record. Once a generation has
// moved the marker stays and the stream is closed, because reopening the active
// path would look like generation 0 to the next recovery: this record is dropped
// and the next holder of the stream lock completes the transaction first.
// Caller must hold the stream lock and the writer's mutex.
func rotateStream(s *stream) error {
	if err := recoverRotation(s.path); err != nil {
		// Recovery may already have staged a generation, so reopening the active
		// path here would create a second generation 0 that every later staging
		// attempt rejects: leave the stream closed until recovery succeeds.
		if s.file != nil {
			// Best-effort: the descriptor names a file recovery may have moved.
			_ = s.file.Close()
			s.file = nil
		}
		return err
	}
	gens, err := existingGenerations(s.path)
	if err != nil {
		return s.deferRotation(err)
	}
	if len(gens) == 0 {
		s.resetRotation()
		return s.reopen()
	}
	m := rotationMarker{Version: rotationMarkerVersion, Phase: rotationPhaseStaging, Generations: gens}
	if err := writeRotationMarker(s.path, m); err != nil {
		return s.deferRotation(err)
	}
	// Windows refuses to rename a file another handle has open, so the
	// descriptor is closed before any source moves.
	if s.file != nil {
		closeErr := s.file.Close()
		s.file = nil
		if closeErr != nil {
			return s.deferRotation(fmt.Errorf("close diagnostics stream for rotation: %w", closeErr))
		}
	}
	// A staging failure that moved nothing leaves the generations exactly as
	// they were, so the transaction is abandoned and the deferral policy bounds
	// the retry. Once a source has moved the marker stays: reopening the active
	// path would leave a fresh file where generation 0 used to be, which a later
	// recovery cannot tell from a generation that is both staged and present, so
	// the stream stays closed and this record is dropped.
	moved, stageErr := stageRotation(s.path, m)
	if stageErr != nil {
		if moved == 0 {
			return s.deferRotation(abandonRotation(s.path, stageErr))
		}
		return stageErr
	}
	m.Phase = rotationPhaseInstalling
	if err := writeRotationMarker(s.path, m); err != nil {
		return err
	}
	if err := completeRotation(s.path, m); err != nil {
		return err
	}
	s.resetRotation()
	if err := s.reopen(); err != nil {
		return s.deferRotation(err)
	}
	return nil
}

package builtin

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func (p *mutatePlanner) commit() (dirtyPaths []string, err error) {
	snapshots := make(map[string]*mutateFileState, len(p.states))
	states := make([]*mutateFileState, 0, len(p.states))
	for path, state := range p.states {
		if state.touched {
			copyState := *state
			copyState.content = append([]byte(nil), state.original...)
			snapshots[path] = &copyState
			states = append(states, state)
		}
	}
	sort.Slice(states, func(i, j int) bool { return states[i].path < states[j].path })

	committed := make([]*mutateFileState, 0, len(states))
	for _, state := range states {
		var writeErr error
		if state.exists {
			if state.needsParent {
				writeErr = os.MkdirAll(filepath.Dir(state.path), 0o755)
			}
			if writeErr == nil {
				mode := state.originalMode
				if mode == 0 {
					mode = 0o644
				}
				writeErr = writeFileAtomic(state.path, state.content, mode)
			}
		} else {
			writeErr = os.Remove(state.path)
			if errors.Is(writeErr, os.ErrNotExist) {
				writeErr = nil
			}
		}
		if writeErr != nil {
			failedPaths, rollbackErr := rollbackMutate(committed, snapshots)
			if rollbackErr != nil {
				return failedPaths, errors.Join(writeErr, fmt.Errorf("rollback failed: %w", rollbackErr))
			}
			return nil, writeErr
		}
		committed = append(committed, state)
	}
	return nil, nil
}

func rollbackMutate(committed []*mutateFileState, snapshots map[string]*mutateFileState) ([]string, error) {
	var errs []string
	var failedPaths []string
	for i := len(committed) - 1; i >= 0; i-- {
		snapshot := snapshots[committed[i].path]
		if snapshot == nil {
			continue
		}
		var err error
		if snapshot.originalExists {
			if snapshot.originalIsDir {
				continue
			}
			mode := snapshot.originalMode
			if mode == 0 {
				mode = 0o644
			}
			err = os.WriteFile(snapshot.path, snapshot.original, mode)
		} else {
			err = os.Remove(snapshot.path)
			if errors.Is(err, os.ErrNotExist) {
				err = nil
			}
		}
		if err != nil {
			errs = append(errs, err.Error())
			failedPaths = append(failedPaths, snapshot.displayPath)
		}
	}
	if len(errs) > 0 {
		return failedPaths, errors.New(strings.Join(errs, "; "))
	}
	return nil, nil
}

// writeFileAtomic writes content to path atomically using a temp file in the same directory,
// then renaming it to the target path. This ensures that if the process dies mid-write,
// the original file remains untouched.
func writeFileAtomic(path string, content []byte, mode os.FileMode) (err error) {
	dir := filepath.Dir(path)
	f, createErr := os.CreateTemp(dir, ".mutate-*")
	if createErr != nil {
		return fmt.Errorf("create temp file: %w", createErr)
	}
	tmpName := f.Name()
	closed := false
	// err is the named return; every branch below assigns to it (not :=) so this
	// deferred cleanup — best-effort, errors intentionally discarded — actually
	// observes failures instead of a shadowed local staying nil.
	defer func() {
		if err != nil {
			if !closed {
				_ = f.Close()
			}
			_ = os.Remove(tmpName)
		}
	}()

	if _, err = f.Write(content); err != nil {
		return fmt.Errorf("write temp file: %w", err)
	}

	if err = f.Chmod(mode); err != nil {
		return fmt.Errorf("chmod temp file: %w", err)
	}

	if err = f.Sync(); err != nil {
		return fmt.Errorf("sync temp file: %w", err)
	}

	if err = f.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	closed = true

	if err = os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename temp file to %q: %w", path, err)
	}

	return nil
}

func splitLinesPreserveEndings(text string) []string {
	if text == "" {
		return nil
	}
	parts := strings.SplitAfter(text, "\n")
	if parts[len(parts)-1] == "" {
		return parts[:len(parts)-1]
	}
	return parts
}

func unifiedTextDiff(path, before, after string) string {
	beforeLines := strings.Split(strings.TrimSuffix(strings.ReplaceAll(before, "\r\n", "\n"), "\n"), "\n")
	afterLines := strings.Split(strings.TrimSuffix(strings.ReplaceAll(after, "\r\n", "\n"), "\n"), "\n")
	if before == "" {
		beforeLines = nil
	}
	if after == "" {
		afterLines = nil
	}
	if mutateDiffTooLarge(before, after, beforeLines, afterLines) {
		return fmt.Sprintf("--- %s\n+++ %s\n<diff omitted: change too large (%d -> %d bytes, %d -> %d lines)>",
			path, path, len(before), len(after), len(beforeLines), len(afterLines))
	}
	return formatUnifiedHunks(path, beforeLines, afterLines, 3)
}

func mutateDiffTooLarge(before, after string, beforeLines, afterLines []string) bool {
	return len(before)+len(after) > maxMutateDiffInputBytes ||
		len(beforeLines)+len(afterLines) > maxMutateDiffInputLines
}

func appendUnique(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func truncateMutateOutput(output string) string {
	if len(output) <= maxMutateOutputChars {
		return output
	}
	return output[:maxMutateOutputChars] + "\n<truncated>"
}

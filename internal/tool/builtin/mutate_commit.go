package builtin

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func (p *mutatePlanner) commit() error {
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
		var err error
		if state.exists {
			mode := state.originalMode
			if mode == 0 {
				mode = 0o644
			}
			err = os.WriteFile(state.path, state.content, mode)
		} else {
			err = os.Remove(state.path)
			if errors.Is(err, os.ErrNotExist) {
				err = nil
			}
		}
		if err != nil {
			rollbackErr := rollbackMutate(committed, snapshots)
			if rollbackErr != nil {
				return errors.Join(err, fmt.Errorf("rollback failed: %w", rollbackErr))
			}
			return err
		}
		committed = append(committed, state)
	}
	return nil
}

func rollbackMutate(committed []*mutateFileState, snapshots map[string]*mutateFileState) error {
	var errs []string
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
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// ensureParentDirExists is called by the commit phase to verify or create
// parent directories. For sandbox tmpDir paths, it creates missing parents;
// for other paths, it only verifies existence. This prevents silent directory
// tree creation outside the sandbox while allowing full access within it.
func ensureParentDirExists(path string, isSandboxTmpPath bool) error {
	parent := filepath.Dir(path)
	info, err := os.Stat(parent)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("parent directory %q: %w", parent, err)
		}
		if !isSandboxTmpPath {
			return fmt.Errorf("parent directory %q: %w", parent, err)
		}
		if mkErr := os.MkdirAll(parent, 0o755); mkErr != nil {
			return fmt.Errorf("create parent directory %q: %w", parent, mkErr)
		}
		return nil
	}
	if !info.IsDir() {
		return fmt.Errorf("parent %q is not a directory", parent)
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

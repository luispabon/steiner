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
	committed := make([]*mutateFileState, 0, len(p.states))
	for path, state := range p.states {
		if state.touched {
			copyState := *state
			copyState.content = append([]byte(nil), state.original...)
			snapshots[path] = &copyState
		}
	}

	states := make([]*mutateFileState, 0, len(p.states))
	for _, state := range p.states {
		if state.touched {
			states = append(states, state)
		}
	}
	sort.Slice(states, func(i, j int) bool { return states[i].path < states[j].path })

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
				return fmt.Errorf("%w; rollback failed: %v", err, rollbackErr)
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

func ensureParentDir(path string) error {
	parent := filepath.Dir(path)
	info, err := os.Stat(parent)
	if err != nil {
		return fmt.Errorf("parent directory %q: %w", parent, err)
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
	var b strings.Builder
	fmt.Fprintf(&b, "--- %s\n+++ %s\n", path, path)
	beforeLines := strings.Split(strings.TrimSuffix(strings.ReplaceAll(before, "\r\n", "\n"), "\n"), "\n")
	afterLines := strings.Split(strings.TrimSuffix(strings.ReplaceAll(after, "\r\n", "\n"), "\n"), "\n")
	if before == "" {
		beforeLines = nil
	}
	if after == "" {
		afterLines = nil
	}
	fmt.Fprintf(&b, "@@ -1,%d +1,%d @@\n", max(1, len(beforeLines)), max(1, len(afterLines)))
	for _, line := range beforeLines {
		b.WriteString("-")
		b.WriteString(line)
		b.WriteString("\n")
	}
	for _, line := range afterLines {
		b.WriteString("+")
		b.WriteString(line)
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
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

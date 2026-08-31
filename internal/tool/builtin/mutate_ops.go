package builtin

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const mutateContextRadius = 2
const mutateContextMaxLines = 7

func (p *mutatePlanner) planCreate(index int, op MutateOperation) error {
	state, err := p.stateFor(op.Path)
	if err != nil {
		return fmt.Errorf("mutate: operation %d create: %w", index, err)
	}
	if err := p.verifyFileHash(index, "create", state, op.FileHash); err != nil {
		return err
	}
	if state.exists {
		return fmt.Errorf("mutate: operation %d create: %s already exists", index, state.displayPath)
	}
	if err := verifyParentDirExists(state.path, p.isSandboxTmpPath(state.path), state); err != nil {
		return fmt.Errorf("mutate: operation %d create: %w", index, err)
	}
	state.exists = true
	state.isDir = false
	state.content = []byte(op.Content)
	state.touched = true
	p.result.Created = appendUnique(p.result.Created, state.displayPath)
	if err := p.recordTextOperation(index, op, state, 0, 1); err != nil {
		return err
	}
	return nil
}

func (p *mutatePlanner) planWrite(index int, op MutateOperation) error {
	state, err := p.stateFor(op.Path)
	if err != nil {
		return fmt.Errorf("mutate: operation %d write: %w", index, err)
	}
	if err := p.verifyFileHash(index, "write", state, op.FileHash); err != nil {
		return err
	}
	if state.exists && state.isDir {
		return fmt.Errorf("mutate: operation %d write: %s is a directory", index, state.displayPath)
	}
	if err := verifyParentDirExists(state.path, p.isSandboxTmpPath(state.path), state); err != nil {
		return fmt.Errorf("mutate: operation %d write: %w", index, err)
	}
	if op.Content == "" && state.exists && len(state.content) > 0 {
		return fmt.Errorf("mutate: operation %d write: content is empty but %s has %d bytes — use delete_file to remove the file", index, state.displayPath, len(state.content))
	}
	wasExisting := state.exists
	state.exists = true
	state.isDir = false
	state.content = []byte(op.Content)
	state.touched = true
	if wasExisting {
		p.result.Modified = appendUnique(p.result.Modified, state.displayPath)
	} else {
		p.result.Created = appendUnique(p.result.Created, state.displayPath)
	}
	if err := p.recordTextOperation(index, op, state, 0, 1); err != nil {
		return err
	}
	return nil
}

func (p *mutatePlanner) planReplace(index int, op MutateOperation) error {
	state, err := p.textState(index, "replace", op.Path)
	if err != nil {
		return err
	}
	if err := p.verifyFileHash(index, "replace", state, op.FileHash); err != nil {
		return err
	}
	if err := p.verifyObserved(index, state, op.FileHash); err != nil {
		return err
	}
	if op.OldString == "" {
		return fmt.Errorf("mutate: operation %d replace: old_string is empty", index)
	}
	oldBytes := []byte(op.OldString)
	matchCount := bytes.Count(state.content, oldBytes)
	switch {
	case matchCount == 0:
		return errors.New(buildNoMatchDiagnostics(fmt.Sprintf("mutate: operation %d replace", index), state.content, op.OldString, state.path))
	case matchCount > 1 && !op.ReplaceAll:
		return errors.New(buildAmbiguousDiagnostics(fmt.Sprintf("mutate: operation %d replace", index), state.content, op.OldString, matchCount, state.path))
	}
	firstMatch := bytes.Index(state.content, oldBytes)
	anchorLine := lineNumberAt(state.content, firstMatch)
	if op.ReplaceAll {
		state.content = bytes.ReplaceAll(state.content, oldBytes, []byte(op.NewString))
	} else {
		state.content = bytes.Replace(state.content, oldBytes, []byte(op.NewString), 1)
	}
	state.touched = true
	p.result.Modified = appendUnique(p.result.Modified, state.displayPath)
	if err := p.recordTextOperation(index, op, state, matchCount, anchorLine); err != nil {
		return err
	}
	return nil
}

func (p *mutatePlanner) planDelete(index int, op MutateOperation) error {
	state, err := p.stateFor(op.Path)
	if err != nil {
		return fmt.Errorf("mutate: operation %d delete_file: %w", index, err)
	}
	if err := p.verifyFileHash(index, "delete_file", state, op.FileHash); err != nil {
		return err
	}
	if !state.exists {
		return fmt.Errorf("mutate: operation %d delete_file: %s does not exist", index, state.displayPath)
	}
	if state.isDir {
		return fmt.Errorf("mutate: operation %d delete_file: %s is a directory", index, state.displayPath)
	}
	state.exists = false
	state.content = nil
	state.touched = true
	p.result.Deleted = appendUnique(p.result.Deleted, state.displayPath)
	return nil
}

func (p *mutatePlanner) planMove(index int, op MutateOperation) error {
	from, err := p.stateFor(op.From)
	if err != nil {
		return fmt.Errorf("mutate: operation %d move: from: %w", index, err)
	}
	if !from.exists {
		return fmt.Errorf("mutate: operation %d move: %s does not exist", index, from.displayPath)
	}
	if from.isDir {
		return fmt.Errorf("mutate: operation %d move: %s is a directory", index, from.displayPath)
	}
	if err := p.verifyFileHash(index, "move", from, op.FileHash); err != nil {
		return err
	}
	to, err := p.stateFor(op.To)
	if err != nil {
		return fmt.Errorf("mutate: operation %d move: to: %w", index, err)
	}
	if to.exists {
		return fmt.Errorf("mutate: operation %d move: %s already exists", index, to.displayPath)
	}
	if err := verifyParentDirExists(to.path, p.isSandboxTmpPath(to.path), to); err != nil {
		return fmt.Errorf("mutate: operation %d move: %w", index, err)
	}
	to.exists = true
	to.isDir = false
	to.content = append([]byte(nil), from.content...)
	to.touched = true
	from.exists = false
	from.content = nil
	from.touched = true
	p.result.Moved = append(p.result.Moved, MoveResult{From: from.displayPath, To: to.displayPath})
	if err := p.recordMovedOperation(index, op, to); err != nil {
		return err
	}
	return nil
}

// verifyParentDirExists verifies the parent directory exists (plan-phase only, side-effect-free).
// For non-sandbox paths, it returns an error if the parent doesn't exist.
// For sandbox tmp paths, it marks the state to create the parent during commit.
func verifyParentDirExists(path string, isSandboxTmpPath bool, state *mutateFileState) error {
	parent := filepath.Dir(path)
	info, err := os.Stat(parent)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("parent directory %q: %w", parent, err)
		}
		if !isSandboxTmpPath {
			return fmt.Errorf("parent directory %q does not exist — create it first (e.g. with bash mkdir -p), then retry: %w", parent, err)
		}
		// Parent doesn't exist, but it's a sandbox tmp path — mark for creation at commit time.
		state.needsParent = true
		return nil
	}
	if !info.IsDir() {
		return fmt.Errorf("parent %q is not a directory", parent)
	}
	return nil
}

func verifyAssertions(index int, op MutateOperation, state *mutateFileState) ([]MutateAssertionResult, error) {
	assertions := make([]MutateAssertionResult, 0, len(op.AssertPresent)+len(op.AssertAbsent))
	for _, text := range op.AssertPresent {
		matches := bytes.Count(state.content, []byte(text))
		if matches == 0 {
			return nil, fmt.Errorf("mutate: operation %d %s: assert_present failed on %s for %q", index, op.Type, state.displayPath, text)
		}
		assertions = append(assertions, MutateAssertionResult{Kind: "present", Text: text, Matches: matches})
	}
	for _, text := range op.AssertAbsent {
		matches := bytes.Count(state.content, []byte(text))
		if matches != 0 {
			return nil, fmt.Errorf("mutate: operation %d %s: assert_absent failed on %s for %q; found %d matches", index, op.Type, state.displayPath, text, matches)
		}
		assertions = append(assertions, MutateAssertionResult{Kind: "absent", Text: text, Matches: matches})
	}
	return assertions, nil
}

func buildMutateContext(content []byte, anchorLine int) *MutateContextResult {
	lines := splitLinesPreserveEndings(string(content))
	totalLines := len(lines)
	if totalLines == 0 {
		return nil
	}
	if anchorLine <= 0 {
		anchorLine = 1
	}
	if anchorLine > totalLines {
		anchorLine = totalLines
	}
	start := anchorLine - mutateContextRadius
	if start < 1 {
		start = 1
	}
	end := anchorLine + mutateContextRadius
	if end > totalLines {
		end = totalLines
	}
	if span := end - start + 1; span > mutateContextMaxLines {
		end = start + mutateContextMaxLines - 1
	}
	return &MutateContextResult{
		StartLine:  start,
		EndLine:    end,
		TotalLines: totalLines,
		Content:    strings.Join(lines[start-1:end], ""),
		Truncated:  start > 1 || end < totalLines,
	}
}

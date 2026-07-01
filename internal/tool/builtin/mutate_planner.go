package builtin

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
)

const maxMutateOutputChars = 30000
const maxMutateDiffInputBytes = maxMutateOutputChars * 2
const maxMutateDiffInputLines = 4000

type mutatePlanner struct {
	env     Env
	states  map[string]*mutateFileState
	result  MutateResult
	diffs   []string
	applied int
}

type mutateFileState struct {
	path           string
	displayPath    string
	originalExists bool
	originalIsDir  bool
	originalMode   os.FileMode
	original       []byte
	exists         bool
	isDir          bool
	content        []byte
	touched        bool
}

func (p *mutatePlanner) run(in MutateInput) *MutateResult {
	if len(in.Operations) == 0 {
		return p.fail("mutate: operations is required", 0)
	}
	for i, op := range in.Operations {
		if err := p.planOperation(i+1, op); err != nil {
			return p.fail(err.Error(), len(in.Operations))
		}
		p.applied++
	}

	p.finalizeResult()
	if in.DryRun {
		p.result.Output = p.successOutput("Dry run succeeded.")
		return &p.result
	}
	if err := p.commit(); err != nil {
		p.result.OperationsFailed = 1
		p.result.clearCommittedMetadata()
		p.result.Output = fmt.Sprintf("mutate: commit failed: %v", err)
		return &p.result
	}
	p.result.OperationsApplied = p.applied
	p.result.Output = p.successOutput("Success.")
	return &p.result
}

func (p *mutatePlanner) fail(message string, total int) *MutateResult {
	p.result.clearCommittedMetadata()
	p.result.OperationsFailed = 1
	skipped := total - p.applied - 1
	if skipped > 0 {
		p.result.OperationsSkipped = skipped
	}
	p.result.Output = message
	return &p.result
}

func (p *mutatePlanner) planOperation(index int, op MutateOperation) error {
	if err := validateFields(index, op); err != nil {
		return err
	}
	if err := validateRequired(index, op); err != nil {
		return err
	}
	switch strings.TrimSpace(op.Type) {
	case "create":
		return p.planCreate(index, op)
	case "write":
		return p.planWrite(index, op)
	case "replace":
		return p.planReplace(index, op)
	case "line_replace":
		return p.planLineReplace(index, op)
	case "delete_line":
		return p.planDeleteLine(index, op)
	case "delete":
		return p.planDelete(index, op)
	case "move":
		return p.planMove(index, op)
	case "insert_before":
		return p.planInsert(index, op, false)
	case "insert_after":
		return p.planInsert(index, op, true)
	default:
		return fmt.Errorf("mutate: operation %d: unsupported type %q", index, op.Type)
	}
}

func (p *mutatePlanner) verifyFileHash(index int, opType string, state *mutateFileState, fileHash string) error {
	if fileHash == "" {
		return nil // optional — backward compat
	}
	if !state.exists {
		return fmt.Errorf("mutate: operation %d %s: file_hash requires an existing file, but %s does not exist", index, opType, state.displayPath)
	}
	if state.isDir {
		return fmt.Errorf("mutate: operation %d %s: file_hash requires a file, but %s is a directory", index, opType, state.displayPath)
	}
	actual := fileContentHash(state.original)
	if actual != fileHash {
		return fmt.Errorf("mutate: operation %d %s: file_hash mismatch on %s — expected %s, got %s (file changed since last read; re-read to get fresh hash)", index, opType, state.displayPath, fileHash, actual)
	}
	return nil
}

func (p *mutatePlanner) textState(index int, opType, path string) (*mutateFileState, error) {
	state, err := p.stateFor(path)
	if err != nil {
		return nil, fmt.Errorf("mutate: operation %d %s: %w", index, opType, err)
	}
	if !state.exists {
		return nil, fmt.Errorf("mutate: operation %d %s: %s does not exist", index, opType, state.displayPath)
	}
	if state.isDir {
		return nil, fmt.Errorf("mutate: operation %d %s: %s is a directory", index, opType, state.displayPath)
	}
	if isBinary(state.content) {
		return nil, fmt.Errorf("mutate: operation %d %s: %s appears to be binary", index, opType, state.displayPath)
	}
	return state, nil
}

func (p *mutatePlanner) stateFor(rawPath string) (*mutateFileState, error) {
	absPath, err := p.resolvePath(rawPath)
	if err != nil {
		return nil, err
	}
	if state, ok := p.states[absPath]; ok {
		return state, nil
	}
	displayPath := relDisplayPath(p.env.WorkDir, absPath)
	if p.env.PathPolicy != nil {
		dp := p.env.PathPolicy.DisplayPath(absPath)
		if dp != absPath {
			displayPath = dp
		}
	}
	state := &mutateFileState{path: absPath, displayPath: displayPath}
	info, err := os.Stat(absPath)
	switch {
	case err == nil:
		state.originalExists = true
		state.exists = true
		state.originalIsDir = info.IsDir()
		state.isDir = info.IsDir()
		state.originalMode = info.Mode().Perm()
		if !info.IsDir() {
			content, readErr := os.ReadFile(absPath)
			if readErr != nil {
				return nil, fmt.Errorf("read %q: %w", state.displayPath, readErr)
			}
			state.original = append([]byte(nil), content...)
			state.content = append([]byte(nil), content...)
		}
	case errors.Is(err, os.ErrNotExist):
		state.originalMode = 0o644
	default:
		return nil, fmt.Errorf("stat %q: %w", state.displayPath, err)
	}
	p.states[absPath] = state
	return state, nil
}

func (p *mutatePlanner) resolvePath(rawPath string) (string, error) {
	if p.env.PathPolicy != nil {
		resolved, err := p.env.PathPolicy.ResolvePath(rawPath, true)
		if err != nil {
			return "", err
		}
		return resolved, nil
	}
	return absWorkspacePath(p.env.WorkDir, rawPath)
}

func (p *mutatePlanner) resolvedDisplayPath(absPath string) string {
	if p.env.PathPolicy != nil {
		return p.env.PathPolicy.DisplayPath(absPath)
	}
	return absPath
}

func (p *mutatePlanner) isSandboxTmpPath(path string) bool {
	if p.env.PathPolicy == nil {
		return false
	}
	return p.env.PathPolicy.IsSandboxTmpPath(path)
}

func (p *mutatePlanner) finalizeResult() {
	for _, state := range p.states {
		if state.touched {
			p.result.Paths = appendUnique(p.result.Paths, state.displayPath)
			if state.exists && !state.isDir {
				if p.result.FileHashes == nil {
					p.result.FileHashes = make(map[string]string)
				}
				p.result.FileHashes[state.displayPath] = fileContentHash(state.content)
			}
		}
	}
	sort.Strings(p.result.Paths)
	sort.Strings(p.result.Created)
	sort.Strings(p.result.Modified)
	sort.Strings(p.result.Deleted)
}

func (p *mutatePlanner) recordTextOperation(index int, op MutateOperation, state *mutateFileState, matchCount, anchorLine int) error {
	assertions, err := verifyAssertions(index, op, state)
	if err != nil {
		return err
	}
	p.result.OperationResults = append(p.result.OperationResults, MutateOperationResult{
		Index:        index,
		Type:         op.Type,
		Path:         state.displayPath,
		ResolvedPath: p.resolvedDisplayPath(state.path),
		MatchCount:   matchCount,
		FileHash:     fileContentHash(state.content),
		Assertions:   assertions,
		Context:      buildMutateContext(state.content, anchorLine),
	})
	return nil
}

func (p *mutatePlanner) recordMovedOperation(index int, op MutateOperation, state *mutateFileState) error {
	assertions, err := verifyAssertions(index, op, state)
	if err != nil {
		return err
	}
	p.result.OperationResults = append(p.result.OperationResults, MutateOperationResult{
		Index:        index,
		Type:         op.Type,
		From:         op.From,
		To:           op.To,
		Path:         state.displayPath,
		ResolvedPath: p.resolvedDisplayPath(state.path),
		FileHash:     fileContentHash(state.content),
		Assertions:   assertions,
		Context:      buildMutateContext(state.content, 1),
	})
	return nil
}

func (p *mutatePlanner) successOutput(prefix string) string {
	lines := []string{prefix, "Updated the following files:"}
	for _, path := range p.result.Created {
		lines = append(lines, "A "+path)
	}
	for _, path := range p.result.Modified {
		lines = append(lines, "M "+path)
	}
	for _, path := range p.result.Deleted {
		lines = append(lines, "D "+path)
	}
	for _, moved := range p.result.Moved {
		lines = append(lines, "R "+moved.From+" -> "+moved.To)
	}
	if len(p.diffs) > 0 {
		lines = append(lines, "", strings.Join(p.diffs, "\n"))
	}
	return truncateMutateOutput(strings.Join(lines, "\n"))
}

func (p *mutatePlanner) addDiff(path, before, after string) {
	if before == after {
		return
	}
	p.diffs = append(p.diffs, unifiedTextDiff(path, before, after))
}

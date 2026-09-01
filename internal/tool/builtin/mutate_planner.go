package builtin

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
)

type mutatePlanner struct {
	env     Env
	states  map[string]*mutateFileState
	result  MutateResult
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
	needsParent    bool
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
	dirtyPaths, err := p.commit()
	if err != nil {
		p.result.OperationsFailed = 1
		p.result.OperationsRolledBack = len(in.Operations)
		for i := range p.result.OperationResults {
			p.result.OperationResults[i].Applied = false
			p.result.OperationResults[i].FileHash = ""
		}
		if len(dirtyPaths) > 0 {
			p.result.Paths = dirtyPaths
			p.result.Modified = dirtyPaths
			p.result.Output = fmt.Sprintf("mutate: commit failed with rollback failure: %v — the following files are in an inconsistent state: %s", err, strings.Join(dirtyPaths, ", "))
		} else {
			p.result.clearCommittedMetadata()
			p.result.Output = fmt.Sprintf("mutate: commit failed: %v", err)
		}
		return &p.result
	}
	p.result.OperationsApplied = p.applied
	// Success path: trim envelope to only what model doesn't already have.
	p.result.Paths = nil
	kept := p.result.OperationResults[:0]
	for _, op := range p.result.OperationResults {
		if op.MatchCount > 1 {
			kept = append(kept, MutateOperationResult{
				Index:      op.Index,
				Type:       op.Type,
				Path:       op.Path,
				MatchCount: op.MatchCount,
			})
		}
	}
	if len(kept) == 0 {
		p.result.OperationResults = nil
	} else {
		p.result.OperationResults = kept
	}
	p.result.Output = ""
	return &p.result
}

func (p *mutatePlanner) fail(message string, total int) *MutateResult {
	p.result.clearCommittedMetadata()
	p.result.OperationsFailed = 1
	p.result.OperationsRolledBack = p.applied
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
	case "delete_file":
		return p.planDelete(index, op)
	case "move":
		return p.planMove(index, op)
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
	actual := FileContentHash(state.original)
	if actual != fileHash {
		return fmt.Errorf("mutate: operation %d %s: file_hash mismatch on %s — expected %s, got %s (file changed since last read; re-read to get fresh hash)", index, opType, state.displayPath, fileHash, actual)
	}
	return nil
}

// verifyObserved enforces the replace-operation observation guard: an
// existing-file replace must be backed by either an observed read this
// session or an explicit file_hash. fileHash is passed separately (rather
// than read off op) since verifyFileHash above already validated it when
// non-empty — a mismatch there returns before this runs.
func (p *mutatePlanner) verifyObserved(index int, state *mutateFileState, fileHash string) error {
	if fileHash != "" {
		return nil
	}
	if p.env.FileObserved != nil && p.env.FileObserved(state.path) {
		return nil
	}
	return fmt.Errorf("mutate: operation %d replace: %s not read this session and no file_hash supplied — read the file first, or pass the file_hash from a read/grep result", index, state.displayPath)
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
				p.result.FileHashes[state.displayPath] = FileContentHash(state.content)
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
		FileHash:     FileContentHash(state.content),
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
		FileHash:     FileContentHash(state.content),
		Assertions:   assertions,
		Context:      buildMutateContext(state.content, 1),
	})
	return nil
}

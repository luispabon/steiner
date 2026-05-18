package builtin

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/luispabon/steiner/internal/tool"
)

const maxMutateOutputChars = 30000

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

// NewMutateTool creates a ToolDef for the mutate tool.
func NewMutateTool(env Env) tool.ToolDef {
	return tool.ToolDef{
		Name:            "mutate",
		Description:     "Create, overwrite, replace, line-replace, delete, or move files. Use mutate for all file edits; do not use bash, sed, cat, write, edit, or apply_patch for file mutations.",
		ParameterSchema: MutateSchema(),
		Handler: func(_ context.Context, input map[string]any) (any, error) {
			in, err := decodeInput[MutateInput](input)
			if err != nil {
				return nil, fmt.Errorf("mutate: %w", err)
			}
			planner := &mutatePlanner{
				env:    env,
				states: make(map[string]*mutateFileState),
				result: MutateResult{DryRun: in.DryRun},
			}
			result := planner.run(in)
			return result, nil
		},
	}
}

func (p *mutatePlanner) run(in MutateInput) *MutateResult {
	if len(in.Operations) == 0 {
		return p.fail("mutate: operations is required")
	}
	for i, op := range in.Operations {
		if err := p.planOperation(i+1, op); err != nil {
			return p.fail(err.Error())
		}
		p.applied++
	}

	p.result.OperationsApplied = p.applied
	p.finalizeResult()
	if in.DryRun {
		p.result.Output = p.successOutput("Dry run succeeded.")
		return &p.result
	}
	if err := p.commit(); err != nil {
		p.result.OperationsFailed = 1
		p.result.Output = fmt.Sprintf("mutate: commit failed: %v", err)
		return &p.result
	}
	p.result.Output = p.successOutput("Success.")
	return &p.result
}

func (p *mutatePlanner) fail(message string) *MutateResult {
	p.result.OperationsApplied = p.applied
	p.result.OperationsFailed = 1
	p.result.Output = message
	return &p.result
}

func (p *mutatePlanner) planOperation(index int, op MutateOperation) error {
	switch strings.TrimSpace(op.Type) {
	case "create":
		return p.planCreate(index, op)
	case "write":
		return p.planWrite(index, op)
	case "replace":
		return p.planReplace(index, op)
	case "line_replace":
		return p.planLineReplace(index, op)
	case "delete":
		return p.planDelete(index, op)
	case "move":
		return p.planMove(index, op)
	default:
		return fmt.Errorf("mutate: operation %d: unsupported type %q", index, op.Type)
	}
}

func (p *mutatePlanner) planCreate(index int, op MutateOperation) error {
	state, err := p.stateFor(op.Path)
	if err != nil {
		return fmt.Errorf("mutate: operation %d create: %w", index, err)
	}
	if state.exists {
		return fmt.Errorf("mutate: operation %d create: %s already exists", index, state.displayPath)
	}
	if err := ensureParentDir(state.path); err != nil {
		return fmt.Errorf("mutate: operation %d create: %w", index, err)
	}
	before := string(state.content)
	state.exists = true
	state.isDir = false
	state.content = []byte(op.Content)
	state.touched = true
	p.result.Created = appendUnique(p.result.Created, state.displayPath)
	p.addDiff(state.displayPath, before, op.Content)
	return nil
}

func (p *mutatePlanner) planWrite(index int, op MutateOperation) error {
	state, err := p.stateFor(op.Path)
	if err != nil {
		return fmt.Errorf("mutate: operation %d write: %w", index, err)
	}
	if state.exists && state.isDir {
		return fmt.Errorf("mutate: operation %d write: %s is a directory", index, state.displayPath)
	}
	if err := ensureParentDir(state.path); err != nil {
		return fmt.Errorf("mutate: operation %d write: %w", index, err)
	}
	before := string(state.content)
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
	p.addDiff(state.displayPath, before, op.Content)
	return nil
}

func (p *mutatePlanner) planReplace(index int, op MutateOperation) error {
	state, err := p.textState(index, "replace", op.Path)
	if err != nil {
		return err
	}
	if op.OldString == "" {
		return fmt.Errorf("mutate: operation %d replace: old_string is empty", index)
	}
	oldBytes := []byte(op.OldString)
	matchCount := bytes.Count(state.content, oldBytes)
	switch {
	case matchCount == 0:
		return errors.New(buildNoMatchDiagnostics("mutate", "old_string", state.content, op.OldString))
	case matchCount > 1 && !op.ReplaceAll:
		return errors.New(buildAmbiguousDiagnostics("mutate", "old_string", state.content, op.OldString, matchCount))
	}
	before := string(state.content)
	if op.ReplaceAll {
		state.content = bytes.ReplaceAll(state.content, oldBytes, []byte(op.NewString))
	} else {
		state.content = bytes.Replace(state.content, oldBytes, []byte(op.NewString), 1)
	}
	state.touched = true
	p.result.Modified = appendUnique(p.result.Modified, state.displayPath)
	p.addDiff(state.displayPath, before, string(state.content))
	return nil
}

func (p *mutatePlanner) planLineReplace(index int, op MutateOperation) error {
	state, err := p.textState(index, "line_replace", op.Path)
	if err != nil {
		return err
	}
	if op.Line <= 0 {
		return fmt.Errorf("mutate: operation %d line_replace: line must be >= 1", index)
	}
	if op.OldString == "" {
		return fmt.Errorf("mutate: operation %d line_replace: old_string is empty", index)
	}
	lines := splitLinesPreserveEndings(string(state.content))
	if op.Line > len(lines) {
		return fmt.Errorf("mutate: operation %d line_replace: line %d is outside file with %d lines", index, op.Line, len(lines))
	}
	line := lines[op.Line-1]
	lineText := strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
	if count := strings.Count(lineText, op.OldString); count != 1 {
		return fmt.Errorf("mutate: operation %d line_replace: line %d contains old_string %d times", index, op.Line, count)
	}
	before := string(state.content)
	lines[op.Line-1] = strings.Replace(line, op.OldString, op.NewString, 1)
	state.content = []byte(strings.Join(lines, ""))
	state.touched = true
	p.result.Modified = appendUnique(p.result.Modified, state.displayPath)
	p.addDiff(state.displayPath, before, string(state.content))
	return nil
}

func (p *mutatePlanner) planDelete(index int, op MutateOperation) error {
	state, err := p.stateFor(op.Path)
	if err != nil {
		return fmt.Errorf("mutate: operation %d delete: %w", index, err)
	}
	if !state.exists {
		return fmt.Errorf("mutate: operation %d delete: %s does not exist", index, state.displayPath)
	}
	if state.isDir {
		return fmt.Errorf("mutate: operation %d delete: %s is a directory", index, state.displayPath)
	}
	before := string(state.content)
	state.exists = false
	state.content = nil
	state.touched = true
	p.result.Deleted = appendUnique(p.result.Deleted, state.displayPath)
	p.addDiff(state.displayPath, before, "")
	return nil
}

func (p *mutatePlanner) planMove(index int, op MutateOperation) error {
	from, err := p.stateFor(op.From)
	if err != nil {
		return fmt.Errorf("mutate: operation %d move: from: %w", index, err)
	}
	to, err := p.stateFor(op.To)
	if err != nil {
		return fmt.Errorf("mutate: operation %d move: to: %w", index, err)
	}
	if !from.exists {
		return fmt.Errorf("mutate: operation %d move: %s does not exist", index, from.displayPath)
	}
	if from.isDir {
		return fmt.Errorf("mutate: operation %d move: %s is a directory", index, from.displayPath)
	}
	if to.exists {
		return fmt.Errorf("mutate: operation %d move: %s already exists", index, to.displayPath)
	}
	if err := ensureParentDir(to.path); err != nil {
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
	p.addDiff(from.displayPath, string(to.content), "")
	p.addDiff(to.displayPath, "", string(to.content))
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
	state := &mutateFileState{path: absPath, displayPath: relDisplayPath(p.env.WorkDir, absPath)}
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

func (p *mutatePlanner) finalizeResult() {
	for _, state := range p.states {
		if state.touched {
			p.result.Paths = appendUnique(p.result.Paths, state.displayPath)
		}
	}
	sort.Strings(p.result.Paths)
	sort.Strings(p.result.Created)
	sort.Strings(p.result.Modified)
	sort.Strings(p.result.Deleted)
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

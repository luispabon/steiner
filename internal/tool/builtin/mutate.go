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
const maxMutateDiffInputBytes = maxMutateOutputChars * 2
const maxMutateDiffInputLines = 4000
const mutateContextRadius = 2
const mutateContextMaxLines = 7

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
		Description:     "Create, overwrite, replace, line-replace, delete_line, insert-before, insert-after, delete, or move files. Supports file_hash for staleness detection on existing targets — pass the hash from read/grep to fail fast if the file changed. Move rejects destination collisions instead of overwriting. Use mutate for all file edits; do not use bash, sed, cat, write, edit, or apply_patch for file mutations.",
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

var allowedFields = map[string]map[string]struct{}{
	"create": {
		"path":           {},
		"content":        {},
		"assert_present": {},
		"assert_absent":  {},
		"file_hash":      {},
		"allow_empty":    {},
	},
	"write": {
		"path":           {},
		"content":        {},
		"assert_present": {},
		"assert_absent":  {},
		"file_hash":      {},
		"allow_empty":    {},
	},
	"replace": {
		"path":           {},
		"old_string":     {},
		"new_string":     {},
		"assert_present": {},
		"assert_absent":  {},
		"replace_all":    {},
		"file_hash":      {},
	},
	"line_replace": {
		"path":           {},
		"line":           {},
		"line_count":     {},
		"old_string":     {},
		"new_string":     {},
		"assert_present": {},
		"assert_absent":  {},
		"file_hash":      {},
	},
	"delete_line": {
		"path":           {},
		"line":           {},
		"line_count":     {},
		"assert_present": {},
		"assert_absent":  {},
		"file_hash":      {},
	},
	"delete": {
		"path":      {},
		"file_hash": {},
	},
	"move": {
		"from":           {},
		"to":             {},
		"assert_present": {},
		"assert_absent":  {},
		"file_hash":      {},
	},
	"insert_before": {
		"path":           {},
		"line":           {},
		"content":        {},
		"new_string":     {},
		"assert_present": {},
		"assert_absent":  {},
		"file_hash":      {},
	},
	"insert_after": {
		"path":           {},
		"line":           {},
		"content":        {},
		"new_string":     {},
		"assert_present": {},
		"assert_absent":  {},
		"file_hash":      {},
	},
}

var validFieldsByOpType = func() map[string][]string {
	types := make(map[string][]string, len(allowedFields))
	for opType, fields := range allowedFields {
		valid := make([]string, 0, len(fields))
		for field := range fields {
			valid = append(valid, field)
		}
		sort.Strings(valid)
		types[opType] = valid
	}
	return types
}()

func validateFields(index int, op MutateOperation) error {
	opType := strings.TrimSpace(op.Type)
	allowed, ok := allowedFields[opType]
	if !ok {
		return nil
	}
	type fieldCheck struct {
		name  string
		isSet bool
	}
	checks := []fieldCheck{
		{"path", op.Path != ""},
		{"content", op.Content != ""},
		{"old_string", op.OldString != ""},
		{"new_string", op.NewString != ""},
		{"assert_present", len(op.AssertPresent) > 0},
		{"assert_absent", len(op.AssertAbsent) > 0},
		{"replace_all", op.ReplaceAll},
		{"line", op.Line != 0},
		{"line_count", op.LineCount != 0},
		{"file_hash", op.FileHash != ""},
		{"allow_empty", op.AllowEmpty},
		{"from", op.From != ""},
		{"to", op.To != ""},
	}
	for _, c := range checks {
		if c.isSet {
			if _, ok := allowed[c.name]; !ok {
				return fmt.Errorf("mutate: operation %d %s: field %q is not valid for this operation type; valid fields: %s", index, opType, c.name, strings.Join(validFieldsByOpType[opType], ", "))
			}
		}
	}
	return nil
}

func (p *mutatePlanner) planOperation(index int, op MutateOperation) error {
	if err := validateFields(index, op); err != nil {
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
	if err := ensureParentDir(state.path); err != nil {
		return fmt.Errorf("mutate: operation %d create: %w", index, err)
	}
	before := string(state.content)
	state.exists = true
	state.isDir = false
	state.content = []byte(op.Content)
	state.touched = true
	p.result.Created = appendUnique(p.result.Created, state.displayPath)
	if err := p.recordTextOperation(index, op, state, 0, 1); err != nil {
		return err
	}
	p.addDiff(state.displayPath, before, op.Content)
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
	if err := ensureParentDir(state.path); err != nil {
		return fmt.Errorf("mutate: operation %d write: %w", index, err)
	}
	if op.Content == "" && state.exists && len(state.content) > 0 && !op.AllowEmpty {
		return fmt.Errorf("mutate: operation %d write: content is empty but %s has %d bytes — use delete to remove the file, or set allow_empty to confirm", index, state.displayPath, len(state.content))
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
	if err := p.recordTextOperation(index, op, state, 0, 1); err != nil {
		return err
	}
	p.addDiff(state.displayPath, before, op.Content)
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
	if op.OldString == "" {
		return fmt.Errorf("mutate: operation %d replace: old_string is empty", index)
	}
	oldBytes := []byte(op.OldString)
	matchCount := bytes.Count(state.content, oldBytes)
	switch {
	case matchCount == 0:
		return errors.New(buildNoMatchDiagnostics("mutate", state.content, op.OldString))
	case matchCount > 1 && !op.ReplaceAll:
		return errors.New(buildAmbiguousDiagnostics("mutate", state.content, op.OldString, matchCount))
	}
	before := string(state.content)
	if op.ReplaceAll {
		state.content = bytes.ReplaceAll(state.content, oldBytes, []byte(op.NewString))
	} else {
		state.content = bytes.Replace(state.content, oldBytes, []byte(op.NewString), 1)
	}
	state.touched = true
	p.result.Modified = appendUnique(p.result.Modified, state.displayPath)
	anchorLine := 1
	if firstMatch := bytes.Index([]byte(before), oldBytes); firstMatch >= 0 {
		anchorLine = lineNumberAtOffset([]byte(before), firstMatch)
	}
	if err := p.recordTextOperation(index, op, state, matchCount, anchorLine); err != nil {
		return err
	}
	p.addDiff(state.displayPath, before, string(state.content))
	return nil
}

func (p *mutatePlanner) planLineReplace(index int, op MutateOperation) error {
	state, err := p.textState(index, "line_replace", op.Path)
	if err != nil {
		return err
	}
	if err := p.verifyFileHash(index, "line_replace", state, op.FileHash); err != nil {
		return err
	}
	if op.Line <= 0 {
		return fmt.Errorf("mutate: operation %d line_replace: line must be >= 1", index)
	}
	if op.OldString != "" && strings.Contains(op.OldString, "\n") {
		return fmt.Errorf("mutate: operation %d line_replace: old_string contains newline characters; line_replace matches a single line without its ending — use replace for multi-line matches, or remove newlines from old_string", index)
	}

	useRange := op.LineCount > 0
	lineCount := op.LineCount
	if lineCount <= 0 {
		lineCount = 1
	}
	lines, endLine, err := lineEditRange(index, "line_replace", state, op.Line, lineCount)
	if err != nil {
		return err
	}

	before := string(state.content)

	if !useRange {
		// Single-line: old_string required to prevent silent corruption when line numbers shift.
		line := lines[op.Line-1]
		lineText := strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		if op.OldString == "" {
			return fmt.Errorf("mutate: operation %d line_replace: line_replace on existing file requires old_string for safety — provide the expected line content", index)
		}
		if count := strings.Count(lineText, op.OldString); count != 1 {
			return errors.New(buildLineReplaceMismatchDiagnostics(
				fmt.Sprintf("mutate: operation %d line_replace", index),
				op.OldString, count, op.Line, op.Line, line, true,
			))
		}
		lines[op.Line-1] = strings.Replace(line, op.OldString, op.NewString, 1)
	} else {
		if op.OldString != "" {
			// old_string acts as a validation guard: must appear exactly once in the target range.
			rangeText := strings.Join(lines[op.Line-1:endLine], "")
			if count := strings.Count(rangeText, op.OldString); count != 1 {
				return errors.New(buildLineReplaceMismatchDiagnostics(
					fmt.Sprintf("mutate: operation %d line_replace", index),
					op.OldString, count, op.Line, endLine, rangeText, false,
				))
			}
		}
		lines = spliceLineRange(lines, op.Line-1, endLine, op.NewString)
	}

	state.content = []byte(strings.Join(lines, ""))
	state.touched = true
	p.result.Modified = appendUnique(p.result.Modified, state.displayPath)
	if err := p.recordTextOperation(index, op, state, 0, op.Line); err != nil {
		return err
	}
	p.addDiff(state.displayPath, before, string(state.content))
	return nil
}

func (p *mutatePlanner) planDeleteLine(index int, op MutateOperation) error {
	state, err := p.textState(index, "delete_line", op.Path)
	if err != nil {
		return err
	}
	if err := p.verifyFileHash(index, "delete_line", state, op.FileHash); err != nil {
		return err
	}
	if op.Line <= 0 {
		return fmt.Errorf("mutate: operation %d delete_line: line must be >= 1", index)
	}
	lineCount := op.LineCount
	if lineCount <= 0 {
		lineCount = 1
	}

	lines, endLine, err := lineEditRange(index, "delete_line", state, op.Line, lineCount)
	if err != nil {
		return err
	}

	before := string(state.content)
	lines = spliceLineRange(lines, op.Line-1, endLine, "")

	state.content = []byte(strings.Join(lines, ""))
	state.touched = true
	p.result.Modified = appendUnique(p.result.Modified, state.displayPath)
	if err := p.recordTextOperation(index, op, state, 0, op.Line); err != nil {
		return err
	}
	p.addDiff(state.displayPath, before, string(state.content))
	return nil
}

func lineEditRange(index int, opType string, state *mutateFileState, line, lineCount int) ([]string, int, error) {
	lines := splitLinesPreserveEndings(string(state.content))
	if line > len(lines) {
		return nil, 0, fmt.Errorf("mutate: operation %d %s: line %d is outside file with %d lines", index, opType, line, len(lines))
	}
	endLine := line - 1 + lineCount
	if endLine > len(lines) {
		return nil, 0, fmt.Errorf("mutate: operation %d %s: line_count %d starting at line %d exceeds file length (%d lines)", index, opType, lineCount, line, len(lines))
	}
	return lines, endLine, nil
}

func spliceLineRange(lines []string, start, end int, newString string) []string {
	lastLine := lines[end-1]
	lastLineText := strings.TrimSuffix(strings.TrimSuffix(lastLine, "\n"), "\r")
	trailingEnding := lastLine[len(lastLineText):]

	var replacement []string
	if newString != "" {
		content := newString
		if trailingEnding != "" && !strings.HasSuffix(content, "\n") {
			content += trailingEnding
		}
		replacement = splitLinesPreserveEndings(content)
	}

	result := make([]string, 0, len(lines)-(end-start)+len(replacement))
	result = append(result, lines[:start]...)
	result = append(result, replacement...)
	result = append(result, lines[end:]...)
	return result
}

func (p *mutatePlanner) planDelete(index int, op MutateOperation) error {
	state, err := p.stateFor(op.Path)
	if err != nil {
		return fmt.Errorf("mutate: operation %d delete: %w", index, err)
	}
	if err := p.verifyFileHash(index, "delete", state, op.FileHash); err != nil {
		return err
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
	if err := p.recordMovedOperation(index, op, to); err != nil {
		return err
	}
	p.addDiff(from.displayPath, string(to.content), "")
	p.addDiff(to.displayPath, "", string(to.content))
	return nil
}

func (p *mutatePlanner) planInsert(index int, op MutateOperation, insertAfter bool) error {
	opType := "insert_before"
	if insertAfter {
		opType = "insert_after"
	}

	state, lines, op, err := p.prepareInsert(index, opType, op, insertAfter)
	if err != nil {
		return err
	}
	before := string(state.content)
	state.content = []byte(strings.Join(buildInsertedLines(lines, op, insertAfter), ""))
	state.touched = true
	p.result.Modified = appendUnique(p.result.Modified, state.displayPath)
	anchorLine := op.Line
	if insertAfter {
		anchorLine++
	}
	if err := p.recordTextOperation(index, op, state, 0, anchorLine); err != nil {
		return err
	}
	p.addDiff(state.displayPath, before, string(state.content))
	return nil
}

func (p *mutatePlanner) prepareInsert(index int, opType string, op MutateOperation, insertAfter bool) (*mutateFileState, []string, MutateOperation, error) {
	state, err := p.textState(index, opType, op.Path)
	if err != nil {
		return nil, nil, op, err
	}
	if err := p.verifyFileHash(index, opType, state, op.FileHash); err != nil {
		return nil, nil, op, err
	}
	if err := validateInsertLine(index, op.Line, insertAfter); err != nil {
		return nil, nil, op, err
	}
	lines := splitLinesPreserveEndings(string(state.content))
	if err := validateInsertBounds(index, opType, op.Line, lines); err != nil {
		return nil, nil, op, err
	}
	if op.Content == "" && op.NewString != "" {
		op.Content = op.NewString
	}
	if op.Content == "" {
		return nil, nil, op, fmt.Errorf("mutate: operation %d %s: content is required", index, opType)
	}
	normalizeInsertAnchor(lines, op.Line, insertAfter)
	op.Content = normalizeInsertedContent(op.Content, detectLineEnding(lines))
	return state, lines, op, nil
}

func validateInsertLine(index, line int, insertAfter bool) error {
	if line > 0 {
		return nil
	}
	if insertAfter {
		return fmt.Errorf("mutate: operation %d insert_after: line must be >= 1 (use insert_before line 1 to prepend)", index)
	}
	return fmt.Errorf("mutate: operation %d insert_before: line must be >= 1", index)
}

func validateInsertBounds(index int, opType string, line int, lines []string) error {
	if len(lines) == 0 {
		return fmt.Errorf("mutate: operation %d %s: file is empty, no valid anchor line", index, opType)
	}
	if line > len(lines) {
		return fmt.Errorf("mutate: operation %d %s: line %d is outside file with %d lines", index, opType, line, len(lines))
	}
	return nil
}

func normalizeInsertAnchor(lines []string, line int, insertAfter bool) {
	if !insertAfter {
		return
	}
	ending := detectLineEnding(lines)
	anchorLine := lines[line-1]
	anchorText := strings.TrimSuffix(strings.TrimSuffix(anchorLine, "\n"), "\r")
	if anchorText == anchorLine {
		lines[line-1] = anchorLine + ending
	}
}

func normalizeInsertedContent(content, ending string) string {
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += ending
	}
	return content
}

func buildInsertedLines(lines []string, op MutateOperation, insertAfter bool) []string {
	insertLines := splitLinesPreserveEndings(op.Content)
	result := make([]string, 0, len(lines)+len(insertLines))
	if insertAfter {
		result = append(result, lines[:op.Line]...)
	} else {
		result = append(result, lines[:op.Line-1]...)
	}
	result = append(result, insertLines...)
	if insertAfter {
		return append(result, lines[op.Line:]...)
	}
	return append(result, lines[op.Line-1:]...)
}

func detectLineEnding(lines []string) string {
	for _, line := range lines {
		if strings.HasSuffix(line, "\r\n") {
			return "\r\n"
		}
		if strings.HasSuffix(line, "\n") {
			return "\n"
		}
	}
	return "\n"
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
		Index:      index,
		Type:       op.Type,
		Path:       state.displayPath,
		MatchCount: matchCount,
		FileHash:   fileContentHash(state.content),
		Assertions: assertions,
		Context:    buildMutateContext(state.content, anchorLine),
	})
	return nil
}

func (p *mutatePlanner) recordMovedOperation(index int, op MutateOperation, state *mutateFileState) error {
	assertions, err := verifyAssertions(index, op, state)
	if err != nil {
		return err
	}
	p.result.OperationResults = append(p.result.OperationResults, MutateOperationResult{
		Index:      index,
		Type:       op.Type,
		From:       op.From,
		To:         op.To,
		Path:       state.displayPath,
		FileHash:   fileContentHash(state.content),
		Assertions: assertions,
		Context:    buildMutateContext(state.content, 1),
	})
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

func lineNumberAtOffset(content []byte, offset int) int {
	if offset <= 0 {
		return 1
	}
	line := 1
	for i := 0; i < offset && i < len(content); i++ {
		if content[i] == '\n' {
			line++
		}
	}
	return line
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

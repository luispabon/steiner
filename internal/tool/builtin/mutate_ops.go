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
	if err := verifyParentDirExists(state.path, p.isSandboxTmpPath(state.path), state); err != nil {
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
		return errors.New(buildNoMatchDiagnostics(fmt.Sprintf("mutate: operation %d replace", index), state.content, op.OldString, state.path))
	case matchCount > 1 && !op.ReplaceAll:
		return errors.New(buildAmbiguousDiagnostics(fmt.Sprintf("mutate: operation %d replace", index), state.content, op.OldString, matchCount, state.path))
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
				op.OldString, count, op.Line, op.Line, line, true, state.path,
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
					op.OldString, count, op.Line, endLine, rangeText, false, state.path,
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

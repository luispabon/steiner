package patchdoc

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// PlannedChangeKind identifies the planned mutation type.
type PlannedChangeKind string

const (
	// ChangeAdd marks a planned file addition.
	ChangeAdd PlannedChangeKind = "add"
	// ChangeDelete marks a planned file deletion.
	ChangeDelete PlannedChangeKind = "delete"
	// ChangeUpdate marks a planned file content update.
	ChangeUpdate PlannedChangeKind = "update"
	// ChangeMove marks a planned file move.
	ChangeMove PlannedChangeKind = "move"
)

// PlannedChange captures a validated change planned from a patch hunk.
type PlannedChange struct {
	Kind        PlannedChangeKind
	Path        string
	RelPath     string
	MovePath    string
	MoveRelPath string
	OldContent  []byte
	NewContent  []byte
	Mode        fs.FileMode
}

// ApplyResult summarizes the effect of applying a patch.
type ApplyResult struct {
	Added    []string
	Modified []string
	Deleted  []string
	Moved    []MoveResult
	DryRun   bool
}

// MoveResult describes a file move in the apply result.
type MoveResult struct {
	From string
	To   string
}

// FS abstracts filesystem operations needed to plan and apply patches.
type FS interface {
	ReadFile(name string) ([]byte, error)
	WriteFile(name string, data []byte, perm fs.FileMode) error
	Remove(name string) error
	MkdirAll(path string, perm fs.FileMode) error
	Stat(name string) (fs.FileInfo, error)
}

// OSFS implements FS using the local operating system filesystem.
type OSFS struct{}

// ReadFile reads a file from disk.
func (OSFS) ReadFile(name string) ([]byte, error) {
	return os.ReadFile(name)
}

// WriteFile writes a file to disk with the given permissions.
func (OSFS) WriteFile(name string, data []byte, perm fs.FileMode) error {
	return os.WriteFile(name, data, perm)
}

// Remove deletes a file from disk.
func (OSFS) Remove(name string) error {
	return os.Remove(name)
}

// MkdirAll creates a directory tree on disk.
func (OSFS) MkdirAll(path string, perm fs.FileMode) error {
	return os.MkdirAll(path, perm)
}

// Stat returns file metadata from disk.
func (OSFS) Stat(name string) (fs.FileInfo, error) {
	return os.Stat(name)
}

// ApplyPatch plans a patch and optionally commits the planned changes.
func ApplyPatch(root string, patch Patch, dryRun bool, fsys FS) (ApplyResult, error) {
	if fsys == nil {
		fsys = OSFS{}
	}
	if len(patch.Hunks) == 0 {
		return ApplyResult{}, fmt.Errorf("patch contains no file operations")
	}

	if err := validateDuplicateTargets(patch.Hunks); err != nil {
		return ApplyResult{}, err
	}

	planned := make([]PlannedChange, 0, len(patch.Hunks))
	for _, hunk := range patch.Hunks {
		change, err := planChange(root, hunk, fsys)
		if err != nil {
			return ApplyResult{}, err
		}
		planned = append(planned, change)
	}

	result := buildApplyResult(planned)
	if dryRun {
		result.DryRun = true
		return result, nil
	}

	if err := commitPlannedChanges(root, planned, fsys); err != nil {
		return ApplyResult{}, err
	}

	return result, nil
}

func planChange(root string, hunk Hunk, fsys FS) (PlannedChange, error) {
	switch h := hunk.(type) {
	case AddFile:
		return planAddFile(root, h, fsys)
	case DeleteFile:
		return planDeleteFile(root, h, fsys)
	case UpdateFile:
		return planUpdateFile(root, h, fsys)
	default:
		return PlannedChange{}, fmt.Errorf("planning not implemented for %T", hunk)
	}
}

func planAddFile(root string, hunk AddFile, fsys FS) (PlannedChange, error) {
	abs, rel, err := ValidatePatchPath(root, hunk.PathValue)
	if err != nil {
		return PlannedChange{}, err
	}
	if _, err := fsys.Stat(abs); err == nil {
		return PlannedChange{}, fmt.Errorf("add file failed: %s already exists", rel)
	} else if !errors.Is(err, os.ErrNotExist) {
		return PlannedChange{}, err
	}

	return PlannedChange{
		Kind:       ChangeAdd,
		Path:       abs,
		RelPath:    rel,
		NewContent: []byte(hunk.Contents),
		Mode:       0o644,
	}, nil
}

func planDeleteFile(root string, hunk DeleteFile, fsys FS) (PlannedChange, error) {
	abs, rel, err := ValidatePatchPath(root, hunk.PathValue)
	if err != nil {
		return PlannedChange{}, err
	}
	info, err := fsys.Stat(abs)
	if err != nil {
		return PlannedChange{}, fmt.Errorf("delete file failed: %s: %w", rel, err)
	}
	if info.IsDir() {
		return PlannedChange{}, fmt.Errorf("delete file failed: %s is a directory", rel)
	}

	oldContent, err := fsys.ReadFile(abs)
	if err != nil {
		return PlannedChange{}, err
	}

	return PlannedChange{
		Kind:       ChangeDelete,
		Path:       abs,
		RelPath:    rel,
		OldContent: oldContent,
		Mode:       info.Mode(),
	}, nil
}

func planUpdateFile(root string, hunk UpdateFile, fsys FS) (PlannedChange, error) {
	abs, rel, err := ValidatePatchPath(root, hunk.PathValue)
	if err != nil {
		return PlannedChange{}, err
	}
	info, err := fsys.Stat(abs)
	if err != nil {
		return PlannedChange{}, fmt.Errorf("update file failed: %s: %w", rel, err)
	}
	if info.IsDir() {
		return PlannedChange{}, fmt.Errorf("update file failed: %s is a directory", rel)
	}

	oldBytes, err := fsys.ReadFile(abs)
	if err != nil {
		return PlannedChange{}, err
	}
	if isBinary(oldBytes) {
		return PlannedChange{}, fmt.Errorf("update file failed: %s appears to be binary", rel)
	}

	newText, err := DeriveNewContents(string(oldBytes), rel, hunk.Chunks)
	if err != nil {
		return PlannedChange{}, err
	}

	change := PlannedChange{
		Kind:       ChangeUpdate,
		Path:       abs,
		RelPath:    rel,
		OldContent: oldBytes,
		NewContent: []byte(newText),
		Mode:       info.Mode(),
	}

	if hunk.MovePath != "" {
		destAbs, destRel, err := ValidatePatchPath(root, hunk.MovePath)
		if err != nil {
			return PlannedChange{}, err
		}
		if destAbs == abs {
			return PlannedChange{}, fmt.Errorf("move failed: source and destination are the same: %s", rel)
		}
		if _, err := fsys.Stat(destAbs); err == nil {
			return PlannedChange{}, fmt.Errorf("move failed: destination already exists: %s", destRel)
		} else if !errors.Is(err, os.ErrNotExist) {
			return PlannedChange{}, err
		}
		change.Kind = ChangeMove
		change.MovePath = destAbs
		change.MoveRelPath = destRel
	}

	return change, nil
}

func buildApplyResult(planned []PlannedChange) ApplyResult {
	result := ApplyResult{}
	for _, change := range planned {
		switch change.Kind {
		case ChangeAdd:
			result.Added = append(result.Added, change.RelPath)
		case ChangeDelete:
			result.Deleted = append(result.Deleted, change.RelPath)
		case ChangeUpdate:
			result.Modified = append(result.Modified, change.RelPath)
		case ChangeMove:
			result.Moved = append(result.Moved, MoveResult{
				From: change.RelPath,
				To:   change.MoveRelPath,
			})
		}
	}
	return result
}

func commitPlannedChanges(root string, planned []PlannedChange, fsys FS) error {
	_ = root
	return commitChanges(planned, fsys)
}

func commitChanges(changes []PlannedChange, fsys FS) error {
	ordered := append([]PlannedChange(nil), changes...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return changeKindOrder(ordered[i].Kind) < changeKindOrder(ordered[j].Kind)
	})

	committed := make([]PlannedChange, 0, len(ordered))
	for _, ch := range ordered {
		if err := commitOne(ch, fsys); err != nil {
			return errors.Join(err, rollbackChanges(committed, fsys))
		}
		committed = append(committed, ch)
	}

	return nil
}

func commitOne(ch PlannedChange, fsys FS) error {
	switch ch.Kind {
	case ChangeAdd:
		if err := fsys.MkdirAll(filepath.Dir(ch.Path), 0o755); err != nil {
			return err
		}
		return fsys.WriteFile(ch.Path, ch.NewContent, ch.Mode)
	case ChangeUpdate:
		return fsys.WriteFile(ch.Path, ch.NewContent, ch.Mode)
	case ChangeMove:
		if err := fsys.MkdirAll(filepath.Dir(ch.MovePath), 0o755); err != nil {
			return err
		}
		if err := fsys.WriteFile(ch.MovePath, ch.NewContent, ch.Mode); err != nil {
			return err
		}
		if err := fsys.Remove(ch.Path); err != nil {
			if cleanupErr := fsys.Remove(ch.MovePath); cleanupErr != nil && !errors.Is(cleanupErr, os.ErrNotExist) {
				return errors.Join(err, cleanupErr)
			}
			return err
		}
		return nil
	case ChangeDelete:
		return fsys.Remove(ch.Path)
	default:
		return fmt.Errorf("unknown change kind %q", ch.Kind)
	}
}

func rollbackChanges(committed []PlannedChange, fsys FS) error {
	errs := make([]error, 0)
	for i := len(committed) - 1; i >= 0; i-- {
		ch := committed[i]
		switch ch.Kind {
		case ChangeAdd:
			if err := fsys.Remove(ch.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
				errs = append(errs, err)
			}
		case ChangeUpdate:
			if err := fsys.WriteFile(ch.Path, ch.OldContent, ch.Mode); err != nil {
				errs = append(errs, err)
			}
		case ChangeMove:
			if err := fsys.Remove(ch.MovePath); err != nil && !errors.Is(err, os.ErrNotExist) {
				errs = append(errs, err)
			}
			if err := fsys.WriteFile(ch.Path, ch.OldContent, ch.Mode); err != nil {
				errs = append(errs, err)
			}
		case ChangeDelete:
			if err := fsys.MkdirAll(filepath.Dir(ch.Path), 0o755); err != nil {
				errs = append(errs, err)
				continue
			}
			if err := fsys.WriteFile(ch.Path, ch.OldContent, ch.Mode); err != nil {
				errs = append(errs, err)
			}
		default:
			errs = append(errs, fmt.Errorf("unknown change kind %q", ch.Kind))
		}
	}
	return errors.Join(errs...)
}

func changeKindOrder(kind PlannedChangeKind) int {
	switch kind {
	case ChangeAdd:
		return 0
	case ChangeUpdate:
		return 1
	case ChangeMove:
		return 2
	case ChangeDelete:
		return 3
	default:
		return 4
	}
}

func isBinary(data []byte) bool {
	return bytes.Contains(data, []byte{0})
}

// DeriveNewContents applies update chunks to original and returns the resulting text.
func DeriveNewContents(original string, path string, chunks []UpdateFileChunk) (string, error) {
	originalLines := splitCodexLines(original)
	replacements, err := computeReplacements(originalLines, path, chunks)
	if err != nil {
		return "", err
	}

	newLines := applyReplacements(originalLines, replacements)
	return joinCodexLines(newLines), nil
}

func splitCodexLines(contents string) []string {
	lines := strings.Split(contents, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func joinCodexLines(lines []string) string {
	if len(lines) == 0 || lines[len(lines)-1] != "" {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

type replacement struct {
	Start    int
	OldLen   int
	NewLines []string
}

func computeReplacements(originalLines []string, path string, chunks []UpdateFileChunk) ([]replacement, error) {
	replacements := make([]replacement, 0, len(chunks))
	lineIndex := 0

	for chunkIndex, chunk := range chunks {
		nextIndex, err := advanceReplacementContext(originalLines, path, chunk, lineIndex, chunkIndex, len(chunks))
		if err != nil {
			return nil, err
		}
		lineIndex = nextIndex

		if len(chunk.OldLines) == 0 {
			replacements = append(replacements, buildInsertionReplacement(originalLines, chunk))
			continue
		}

		repl, err := buildChunkReplacement(originalLines, path, chunk, lineIndex, chunkIndex, len(chunks))
		if err != nil {
			return nil, err
		}
		replacements = append(replacements, repl)
		lineIndex = repl.Start + repl.OldLen
	}

	sort.SliceStable(replacements, func(i, j int) bool {
		return replacements[i].Start < replacements[j].Start
	})
	if err := validateNoOverlap(replacements, path); err != nil {
		return nil, err
	}
	return replacements, nil
}

func advanceReplacementContext(originalLines []string, path string, chunk UpdateFileChunk, lineIndex int, chunkIndex int, totalChunks int) (int, error) {
	if !chunk.HasContext {
		return lineIndex, nil
	}

	idx, ok := SeekSequence(originalLines, []string{chunk.ChangeContext}, lineIndex, false)
	if ok {
		return idx + 1, nil
	}

	return 0, buildChunkFailureError(
		path,
		len(originalLines),
		chunkIndex,
		totalChunks,
		fmt.Sprintf(
			"failed to find context %q; @@ anchors must match a literal source line. Use bare @@ plus normal context lines when the anchor is awkward",
			chunk.ChangeContext,
		),
		originalLines,
		chunk.ChangeContext,
		lineIndex,
	)
}

func buildInsertionReplacement(originalLines []string, chunk UpdateFileChunk) replacement {
	insertionIdx := len(originalLines)
	if len(originalLines) > 0 && originalLines[len(originalLines)-1] == "" {
		insertionIdx--
	}
	return replacement{
		Start:    insertionIdx,
		OldLen:   0,
		NewLines: append([]string(nil), chunk.NewLines...),
	}
}

func buildChunkReplacement(originalLines []string, path string, chunk UpdateFileChunk, lineIndex int, chunkIndex int, totalChunks int) (replacement, error) {
	pattern, newLines, startIdx, ok := findChunkSequence(originalLines, chunk, lineIndex)
	if !ok {
		return replacement{}, buildChunkFailureError(
			path,
			len(originalLines),
			chunkIndex,
			totalChunks,
			fmt.Sprintf("failed to find expected lines:\n%s", strings.Join(chunk.OldLines, "\n")),
			originalLines,
			chunk.OldLines[0],
			lineIndex,
		)
	}

	return replacement{
		Start:    startIdx,
		OldLen:   len(pattern),
		NewLines: append([]string(nil), newLines...),
	}, nil
}

func findChunkSequence(originalLines []string, chunk UpdateFileChunk, lineIndex int) ([]string, []string, int, bool) {
	pattern := append([]string(nil), chunk.OldLines...)
	newLines := append([]string(nil), chunk.NewLines...)
	startIdx, ok := SeekSequence(originalLines, pattern, lineIndex, chunk.EndOfFile)
	if ok || len(pattern) == 0 || pattern[len(pattern)-1] != "" {
		return pattern, newLines, startIdx, ok
	}

	pattern = pattern[:len(pattern)-1]
	if len(newLines) > 0 && newLines[len(newLines)-1] == "" {
		newLines = newLines[:len(newLines)-1]
	}
	startIdx, ok = SeekSequence(originalLines, pattern, lineIndex, chunk.EndOfFile)
	return pattern, newLines, startIdx, ok
}

func buildChunkFailureError(path string, lineCount int, chunkIndex int, totalChunks int, message string, originalLines []string, target string, searchStart int) error {
	var b strings.Builder
	fmt.Fprintf(&b, "chunk %d of %d failed in %s (%d lines): %s", chunkIndex+1, totalChunks, path, lineCount, message)
	if target == "" {
		return errors.New(b.String())
	}

	closest, found := FindClosestLine(originalLines, target, searchStart, 20)
	if !found {
		return errors.New(b.String())
	}
	fmt.Fprintf(&b, "\nclosest match at line %d (edit distance %d): %q", closest.LineIndex+1, closest.Distance, closest.Content)
	b.WriteString("\ncontext:")
	for _, preview := range buildClosestMatchPreview(originalLines, closest.LineIndex) {
		b.WriteByte('\n')
		b.WriteString(preview)
	}
	return errors.New(b.String())
}

func buildClosestMatchPreview(lines []string, matchIndex int) []string {
	start := matchIndex - 1
	if start < 0 {
		start = 0
	}
	end := matchIndex + 1
	if end >= len(lines) {
		end = len(lines) - 1
	}

	preview := make([]string, 0, end-start+1)
	for i := start; i <= end; i++ {
		marker := " "
		if i == matchIndex {
			marker = ">"
		}
		preview = append(preview, fmt.Sprintf("%s %d | %s", marker, i+1, lines[i]))
	}
	return preview
}

// validateNoOverlap returns an error if any two consecutive sorted replacements overlap.
func validateNoOverlap(replacements []replacement, path string) error {
	for i := 1; i < len(replacements); i++ {
		prev := replacements[i-1]
		curr := replacements[i]
		if prev.Start+prev.OldLen > curr.Start {
			return fmt.Errorf(
				"overlapping chunks in %s: lines %d-%d overlap with lines %d-%d",
				path, prev.Start+1, prev.Start+prev.OldLen, curr.Start+1, curr.Start+curr.OldLen,
			)
		}
	}
	return nil
}

func applyReplacements(lines []string, replacements []replacement) []string {
	for i := len(replacements) - 1; i >= 0; i-- {
		r := replacements[i]
		if r.OldLen > 0 {
			lines = append(lines[:r.Start], lines[r.Start+r.OldLen:]...)
		}
		if len(r.NewLines) > 0 {
			inserted := append([]string(nil), r.NewLines...)
			lines = append(lines[:r.Start], append(inserted, lines[r.Start:]...)...)
		}
	}
	return lines
}

package patchdoc

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
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

type FS interface {
	ReadFile(name string) ([]byte, error)
	WriteFile(name string, data []byte, perm fs.FileMode) error
	Remove(name string) error
	MkdirAll(path string, perm fs.FileMode) error
	Stat(name string) (fs.FileInfo, error)
}

type OSFS struct{}

func (OSFS) ReadFile(name string) ([]byte, error) {
	return os.ReadFile(name)
}

func (OSFS) WriteFile(name string, data []byte, perm fs.FileMode) error {
	return os.WriteFile(name, data, perm)
}

func (OSFS) Remove(name string) error {
	return os.Remove(name)
}

func (OSFS) MkdirAll(path string, perm fs.FileMode) error {
	return os.MkdirAll(path, perm)
}

func (OSFS) Stat(name string) (fs.FileInfo, error) {
	return os.Stat(name)
}

// ApplyPatch plans a patch and optionally commits the planned changes.
func ApplyPatch(root string, patch Patch, dryRun bool, fsys FS) (ApplyResult, error) {
	if fsys == nil {
		fsys = OSFS{}
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
	_ = planned
	_ = fsys
	return fmt.Errorf("commit not implemented")
}

func isBinary(data []byte) bool {
	return bytes.Contains(data, []byte{0})
}

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

	for _, chunk := range chunks {
		if chunk.HasContext {
			idx, ok := SeekSequence(originalLines, []string{chunk.ChangeContext}, lineIndex, false)
			if !ok {
				return nil, fmt.Errorf("failed to find context %q in %s", chunk.ChangeContext, path)
			}
			lineIndex = idx + 1
		}

		if len(chunk.OldLines) == 0 {
			insertionIdx := len(originalLines)
			if len(originalLines) > 0 && originalLines[len(originalLines)-1] == "" {
				insertionIdx--
			}
			replacements = append(replacements, replacement{
				Start:    insertionIdx,
				OldLen:   0,
				NewLines: append([]string(nil), chunk.NewLines...),
			})
			continue
		}

		pattern := chunk.OldLines
		newLines := chunk.NewLines
		startIdx, ok := SeekSequence(originalLines, pattern, lineIndex, chunk.EndOfFile)
		if !ok && len(pattern) > 0 && pattern[len(pattern)-1] == "" {
			pattern = pattern[:len(pattern)-1]
			newLines = chunk.NewLines
			if len(newLines) > 0 && newLines[len(newLines)-1] == "" {
				newLines = newLines[:len(newLines)-1]
			}
			startIdx, ok = SeekSequence(originalLines, pattern, lineIndex, chunk.EndOfFile)
		}
		if !ok {
			return nil, fmt.Errorf("failed to find expected lines in %s:\n%s", path, strings.Join(chunk.OldLines, "\n"))
		}

		replacements = append(replacements, replacement{
			Start:    startIdx,
			OldLen:   len(pattern),
			NewLines: append([]string(nil), newLines...),
		})
		lineIndex = startIdx + len(pattern)
	}

	sort.SliceStable(replacements, func(i, j int) bool {
		return replacements[i].Start < replacements[j].Start
	})
	return replacements, nil
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

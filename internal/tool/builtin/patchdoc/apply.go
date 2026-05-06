package patchdoc

import (
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"
)

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

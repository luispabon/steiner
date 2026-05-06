package builtin

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/luispabon/steiner/internal/tool"
)

// NewEditTool creates a ToolDef for the edit tool.
func NewEditTool(env Env) tool.ToolDef {
	return tool.ToolDef{
		Name:            "edit",
		Description:     "Replace exact text in one file. Fails if old_string is absent or ambiguous unless replace_all is true.",
		ParameterSchema: EditSchema(),
		Handler: func(ctx context.Context, input map[string]any) (any, error) {
			in, err := decodeInput[EditInput](input)
			if err != nil {
				return nil, fmt.Errorf("edit: %w", err)
			}

			_, err = env.PathPolicy.ResolvePath(in.Path, true)
			if err != nil {
				return nil, fmt.Errorf("edit: %w", err)
			}

			absPath, err := absWorkspacePath(env.WorkDir, in.Path)
			if err != nil {
				return nil, fmt.Errorf("edit: %w", err)
			}

			content, err := os.ReadFile(absPath)
			if err != nil {
				return nil, fmt.Errorf("edit: %w", err)
			}

			if isBinary(content) {
				return &MutationResult{
					Path:   relDisplayPath(env.WorkDir, absPath),
					Output: "edit: file appears to be binary",
				}, nil
			}

			if in.OldString == "" {
				return &MutationResult{
					Path:   relDisplayPath(env.WorkDir, absPath),
					Output: "edit: old_string is empty",
				}, nil
			}

			oldBytes := []byte(in.OldString)
			newBytes := []byte(in.NewString)
			matchCount := bytes.Count(content, oldBytes)

			switch {
			case matchCount == 0:
				return &MutationResult{
					Path:   relDisplayPath(env.WorkDir, absPath),
					Output: buildNoMatchDiagnostics("edit", "old_string", content, in.OldString),
				}, nil
			case matchCount > 1 && !in.ReplaceAll:
				return &MutationResult{
					Path:   relDisplayPath(env.WorkDir, absPath),
					Output: buildAmbiguousDiagnostics("edit", "old_string", content, in.OldString, matchCount),
				}, nil
			}

			replaced := content
			if in.ReplaceAll {
				replaced = bytes.ReplaceAll(content, oldBytes, newBytes)
			} else {
				replaced = bytes.Replace(content, oldBytes, newBytes, 1)
			}

			if err := os.WriteFile(absPath, replaced, 0o644); err != nil {
				return nil, fmt.Errorf("edit: write %q: %w", in.Path, err)
			}

			output := "edit: replaced 1 occurrence"
			if in.ReplaceAll {
				output = fmt.Sprintf("edit: replaced %d occurrence", matchCount)
				if matchCount != 1 {
					output = fmt.Sprintf("edit: replaced %d occurrences", matchCount)
				}
			}

			return &MutationResult{
				Path:    relDisplayPath(env.WorkDir, absPath),
				Output:  output,
				Mutated: true,
			}, nil
		},
	}
}

func buildNoMatchDiagnostics(prefix, subject string, content []byte, oldText string) string {
	var lines []string
	lines = append(lines, fmt.Sprintf("%s: no match for %s", prefix, subject))

	if normalizedWhitespaceMatchExists(content, oldText) {
		lines = append(lines, fmt.Sprintf("%s: exact match failed; normalized whitespace match exists", prefix))
	}

	if anchorStart, anchorEnd, lineNum, preview, ok := findDiagnosticAnchor(content, oldText); ok {
		lines = append(lines, fmt.Sprintf("%s: nearest anchor at line %d, bytes %d-%d", prefix, lineNum, anchorStart+1, anchorEnd))
		lines = append(lines, fmt.Sprintf("%s: context:", prefix))
		lines = append(lines, previewContext(content, lineNum, preview)...)
	} else {
		lines = append(lines, fmt.Sprintf("%s: no nearby exact anchor found", prefix))
	}

	lines = append(lines, fmt.Sprintf("%s: suggestion: reread a slightly wider region around the target text", prefix))
	return strings.Join(lines, "\n")
}

func buildAmbiguousDiagnostics(prefix, subject string, content []byte, oldText string, matchCount int) string {
	var lines []string
	lines = append(lines, fmt.Sprintf("%s: ambiguous match for %s (found %d occurrences)", prefix, subject, matchCount))

	if matchStart, matchEnd, lineNum, preview, ok := findExactMatchPreview(content, oldText); ok {
		lines = append(lines, fmt.Sprintf("%s: closest occurrence at line %d, bytes %d-%d", prefix, lineNum, matchStart+1, matchEnd))
		lines = append(lines, fmt.Sprintf("%s: context:", prefix))
		lines = append(lines, previewContext(content, lineNum, preview)...)
	}

	lines = append(lines, fmt.Sprintf("%s: suggestion: reread a slightly wider region around the target text", prefix))
	return strings.Join(lines, "\n")
}

func normalizedWhitespaceMatchExists(content []byte, oldText string) bool {
	oldNorm := strings.Join(strings.Fields(oldText), " ")
	if oldNorm == "" {
		return false
	}
	contentNorm := strings.Join(strings.Fields(string(content)), " ")
	return strings.Contains(contentNorm, oldNorm)
}

func findDiagnosticAnchor(content []byte, oldText string) (int, int, int, string, bool) {
	candidates := diagnosticAnchorCandidates(oldText)
	bestStart := -1
	bestEnd := -1
	bestLine := 0
	bestPreview := ""
	bestLen := 0

	for _, candidate := range candidates {
		idx := bytes.Index(content, []byte(candidate))
		if idx < 0 {
			continue
		}
		if len(candidate) > bestLen || (len(candidate) == bestLen && (bestStart < 0 || idx < bestStart)) {
			bestStart = idx
			bestEnd = idx + len(candidate)
			bestLine = lineNumberAt(content, idx)
			bestPreview = candidate
			bestLen = len(candidate)
		}
	}

	if bestStart < 0 {
		return 0, 0, 0, "", false
	}

	return bestStart, bestEnd, bestLine, bestPreview, true
}

func findExactMatchPreview(content []byte, oldText string) (int, int, int, string, bool) {
	idx := bytes.Index(content, []byte(oldText))
	if idx < 0 {
		return 0, 0, 0, "", false
	}
	return idx, idx + len(oldText), lineNumberAt(content, idx), oldText, true
}

func diagnosticAnchorCandidates(oldText string) []string {
	seen := make(map[string]struct{})
	var candidates []string

	add := func(candidate string) {
		candidate = strings.TrimSpace(candidate)
		if len(candidate) < 4 {
			return
		}
		if _, ok := seen[candidate]; ok {
			return
		}
		seen[candidate] = struct{}{}
		candidates = append(candidates, candidate)
	}

	for _, line := range strings.Split(oldText, "\n") {
		add(line)

		fields := strings.Fields(line)
		for span := 2; span <= 4 && span <= len(fields); span++ {
			for start := 0; start+span <= len(fields); start++ {
				add(strings.Join(fields[start:start+span], " "))
			}
		}
	}

	return candidates
}

func lineNumberAt(content []byte, offset int) int {
	if offset < 0 {
		offset = 0
	}
	if offset > len(content) {
		offset = len(content)
	}
	line := 1
	for i := 0; i < offset; i++ {
		if content[i] == '\n' {
			line++
		}
	}
	return line
}

func previewContext(content []byte, lineNum int, anchor string) []string {
	lines := strings.Split(strings.ReplaceAll(string(content), "\r\n", "\n"), "\n")
	if len(lines) == 0 {
		return []string{fmt.Sprintf("  1 | %s", truncatePreviewLine(anchor, 120))}
	}

	start := lineNum - 1
	if start < 1 {
		start = 1
	}
	end := lineNum + 1
	if end > len(lines) {
		end = len(lines)
	}

	preview := make([]string, 0, end-start+1)
	for i := start; i <= end; i++ {
		lineText := strings.TrimRight(lines[i-1], "\r")
		if i == lineNum && lineText == "" && anchor != "" {
			lineText = anchor
		}
		prefix := "  "
		if i == lineNum {
			prefix = "> "
		}
		preview = append(preview, fmt.Sprintf("%s%4d | %s", prefix, i, truncatePreviewLine(lineText, 120)))
	}
	return preview
}

func truncatePreviewLine(s string, limit int) string {
	if limit <= 0 || len(s) <= limit {
		return s
	}
	if limit <= 3 {
		return s[:limit]
	}
	return s[:limit-3] + "..."
}

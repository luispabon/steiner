package output

import (
	"fmt"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
)

// FormatFilePreview formats file contents using the default preview line limit.
func FormatFilePreview(path, contents string) PreviewDocument {
	return formatFilePreviewWithLimit(path, contents, defaultPreviewLineLimit)
}

// FormatFilePreviewWithLimit formats file contents using the provided preview line limit.
func FormatFilePreviewWithLimit(path, contents string, lineLimit int) PreviewDocument {
	return formatFilePreviewWithLimit(path, contents, lineLimit)
}

// FormatFilePreviewWithLanguage formats file contents with an explicit language hint.
func FormatFilePreviewWithLanguage(path, language, contents string) PreviewDocument {
	return formatFilePreviewWithLimitAndLanguage(path, language, contents, defaultPreviewLineLimit)
}

func formatFilePreviewWithLimitAndLanguage(path, language, contents string, lineLimit int) PreviewDocument {
	var lexer chroma.Lexer
	if language != "" && language != "plain" {
		if l := lexers.Get(language); l != nil {
			lexer = chroma.Coalesce(l)
		}
	}
	if lexer == nil {
		syntax := DetectPreviewSyntax(path, contents)
		lexer = syntax.Lexer
		language = syntax.Language
	}
	lines, truncated := formatHighlightedText(contents, lexer, PreviewLineKindText, lineLimit)
	if truncated {
		lines = append(lines, truncationLine(lineLimit))
	}
	return PreviewDocument{
		Kind:      PreviewFormatKindFile,
		Path:      path,
		Language:  language,
		Lines:     lines,
		Truncated: truncated,
		LineLimit: lineLimit,
	}
}

// FormatEditDiffPreview formats an edit diff using the default preview line limit.
func FormatEditDiffPreview(path, before, after string) PreviewDocument {
	return formatEditDiffPreviewWithLimit(path, before, after, defaultPreviewLineLimit)
}

// FormatEditDiffPreviewWithLimit formats an edit diff using the provided preview line limit.
func FormatEditDiffPreviewWithLimit(path, before, after string, lineLimit int) PreviewDocument {
	return formatEditDiffPreviewWithLimit(path, before, after, lineLimit)
}

func formatFilePreviewWithLimit(path, contents string, lineLimit int) PreviewDocument {
	syntax := DetectPreviewSyntax(path, contents)
	lines, truncated := formatHighlightedText(contents, syntax.Lexer, PreviewLineKindText, lineLimit)
	if truncated {
		lines = append(lines, truncationLine(lineLimit))
	}
	return PreviewDocument{
		Kind:      PreviewFormatKindFile,
		Path:      path,
		Language:  syntax.Language,
		Lines:     lines,
		Truncated: truncated,
		LineLimit: lineLimit,
	}
}

func formatEditDiffPreviewWithLimit(path, before, after string, lineLimit int) PreviewDocument {
	syntax := DetectPreviewSyntax(path, firstNonEmpty(after, before))
	lines := make([]PreviewLine, 0, 8)
	appendLine := func(line PreviewLine) bool { return appendPreviewLine(&lines, line, lineLimit) }

	if !appendLine(newHeaderLine("---", path)) {
		return truncatedDiffPreview(path, syntax.Language, lines, lineLimit)
	}
	if !appendLine(newHeaderLine("+++", path)) {
		return truncatedDiffPreview(path, syntax.Language, lines, lineLimit)
	}

	beforeLines := splitPreviewLines(normalizePreviewText(before))
	afterLines := splitPreviewLines(normalizePreviewText(after))
	if syntax.Language == "markdown" {
		beforeLines, afterLines = trimSharedMarkdownHeadingPrefix(beforeLines, afterLines)
	}
	oldRange := previewRangeSpec(len(beforeLines))
	newRange := previewRangeSpec(len(afterLines))
	hunkHeader := PreviewLine{
		Kind:   PreviewLineKindHeader,
		Prefix: "@@",
		Spans: []PreviewSpan{
			{Type: chroma.Text, Text: fmt.Sprintf(" -%s +%s ", oldRange, newRange)},
		},
	}
	if !appendLine(hunkHeader) {
		return truncatedDiffPreview(path, syntax.Language, lines, lineLimit)
	}

	for _, op := range diffLineOps(beforeLines, afterLines) {
		line := formatDiffPreviewLine(op, syntax.Lexer)
		if !appendLine(line) {
			return truncatedDiffPreview(path, syntax.Language, lines, lineLimit)
		}
	}

	return PreviewDocument{
		Kind:      PreviewFormatKindEditDiff,
		Path:      path,
		Language:  syntax.Language,
		Lines:     lines,
		Truncated: false,
		LineLimit: lineLimit,
	}
}

func appendPreviewLine(lines *[]PreviewLine, line PreviewLine, lineLimit int) bool {
	if lineLimit >= 0 && len(*lines) >= lineLimit {
		return false
	}
	*lines = append(*lines, line)
	return true
}

func truncatedDiffPreview(path, language string, lines []PreviewLine, lineLimit int) PreviewDocument {
	lines = append(lines, truncationLine(lineLimit))
	return PreviewDocument{Kind: PreviewFormatKindEditDiff, Path: path, Language: language, Lines: lines, Truncated: true, LineLimit: lineLimit}
}

func formatDiffPreviewLine(op diffOp, lexer chroma.Lexer) PreviewLine {
	switch op.kind {
	case diffOpEqual:
		return highlightedLine(PreviewLineKindContext, " ", lexer, op.before)
	case diffOpDelete:
		return highlightedLine(PreviewLineKindRemoved, "-", lexer, op.before)
	case diffOpInsert:
		return highlightedLine(PreviewLineKindAdded, "+", lexer, op.after)
	default:
		return PreviewLine{}
	}
}

func trimSharedMarkdownHeadingPrefix(before, after []string) ([]string, []string) {
	if len(before) == 0 || len(after) == 0 {
		return before, after
	}
	if before[0] != after[0] {
		return before, after
	}
	if !strings.HasPrefix(strings.TrimSpace(before[0]), "#") {
		return before, after
	}
	return before[1:], after[1:]
}

type diffOpKind int

const (
	diffOpEqual diffOpKind = iota
	diffOpDelete
	diffOpInsert
)

type diffOp struct {
	kind   diffOpKind
	before string
	after  string
}

type diffCell struct {
	length int
	prevI  int
	prevJ  int
	move   diffOpKind
}

func diffLineOps(before, after []string) []diffOp {
	table := buildDiffTable(before, after)
	return walkDiffOps(before, after, table)
}

func buildDiffTable(before, after []string) [][]diffCell {
	table := make([][]diffCell, len(before)+1)
	for i := range table {
		table[i] = make([]diffCell, len(after)+1)
	}

	for i := len(before) - 1; i >= 0; i-- {
		for j := len(after) - 1; j >= 0; j-- {
			switch {
			case before[i] == after[j]:
				table[i][j] = diffCell{length: table[i+1][j+1].length + 1, prevI: i + 1, prevJ: j + 1, move: diffOpEqual}
			case table[i+1][j].length >= table[i][j+1].length:
				table[i][j] = diffCell{length: table[i+1][j].length, prevI: i + 1, prevJ: j, move: diffOpDelete}
			default:
				table[i][j] = diffCell{length: table[i][j+1].length, prevI: i, prevJ: j + 1, move: diffOpInsert}
			}
		}
	}
	return table
}

func walkDiffOps(before, after []string, table [][]diffCell) []diffOp {
	ops := make([]diffOp, 0, len(before)+len(after))
	for i, j := 0, 0; i < len(before) || j < len(after); {
		if i < len(before) && j < len(after) && before[i] == after[j] {
			ops = append(ops, diffOp{kind: diffOpEqual, before: before[i], after: after[j]})
			i++
			j++
			continue
		}
		if i < len(before) && (j == len(after) || table[i+1][j].length >= table[i][j+1].length) {
			ops = append(ops, diffOp{kind: diffOpDelete, before: before[i]})
			i++
			continue
		}
		if j < len(after) {
			ops = append(ops, diffOp{kind: diffOpInsert, after: after[j]})
			j++
		}
	}
	return ops
}

func splitPreviewLines(text string) []string {
	normalized := normalizePreviewText(text)
	if normalized == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(normalized, "\n"), "\n")
}

func normalizePreviewText(text string) string {
	if text == "" {
		return ""
	}
	return strings.ReplaceAll(text, "\r\n", "\n")
}

func isBinaryLike(text string) bool {
	return strings.IndexByte(text, 0) >= 0
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func previewRangeSpec(lines int) string {
	if lines < 1 {
		lines = 1
	}
	return fmt.Sprintf("1,%d", lines)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func previewCapacity(lineLimit int) int {
	if lineLimit < 0 {
		return 0
	}
	return min(lineLimit, defaultPreviewLineLimit)
}

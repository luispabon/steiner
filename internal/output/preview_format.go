package output

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
)

const defaultPreviewLineLimit = 120

type PreviewFormatKind string

const (
	PreviewFormatKindFile     PreviewFormatKind = "file"
	PreviewFormatKindEditDiff PreviewFormatKind = "edit_diff"
)

type PreviewLineKind string

const (
	PreviewLineKindText      PreviewLineKind = "text"
	PreviewLineKindHeader    PreviewLineKind = "header"
	PreviewLineKindContext   PreviewLineKind = "context"
	PreviewLineKindAdded     PreviewLineKind = "added"
	PreviewLineKindRemoved   PreviewLineKind = "removed"
	PreviewLineKindTruncated PreviewLineKind = "truncated"
)

type PreviewSpan struct {
	Type chroma.TokenType
	Text string
}

type PreviewLine struct {
	Kind   PreviewLineKind
	Prefix string
	Spans  []PreviewSpan
}

type PreviewDocument struct {
	Kind      PreviewFormatKind
	Path      string
	Language  string
	Lines     []PreviewLine
	Truncated bool
	LineLimit int
}

type PreviewSyntax struct {
	Lexer    chroma.Lexer
	Language string
}

func DetectPreviewSyntax(path, contents string) PreviewSyntax {
	lexer := detectPreviewLexer(path, contents)
	return PreviewSyntax{
		Lexer:    lexer,
		Language: previewLanguageForLexer(lexer),
	}
}

func FormatFilePreview(path, contents string) PreviewDocument {
	return formatFilePreviewWithLimit(path, contents, defaultPreviewLineLimit)
}

func FormatFilePreviewWithLimit(path, contents string, lineLimit int) PreviewDocument {
	return formatFilePreviewWithLimit(path, contents, lineLimit)
}

func FormatEditDiffPreview(path, before, after string) PreviewDocument {
	return formatEditDiffPreviewWithLimit(path, before, after, defaultPreviewLineLimit)
}

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
	appendLine := func(line PreviewLine) bool {
		if lineLimit >= 0 && len(lines) >= lineLimit {
			return false
		}
		lines = append(lines, line)
		return true
	}

	if !appendLine(newHeaderLine("---", path)) {
		lines = append(lines, truncationLine(lineLimit))
		return PreviewDocument{Kind: PreviewFormatKindEditDiff, Path: path, Language: syntax.Language, Lines: lines, Truncated: true, LineLimit: lineLimit}
	}
	if !appendLine(newHeaderLine("+++", path)) {
		lines = append(lines, truncationLine(lineLimit))
		return PreviewDocument{Kind: PreviewFormatKindEditDiff, Path: path, Language: syntax.Language, Lines: lines, Truncated: true, LineLimit: lineLimit}
	}

	beforeLines := splitPreviewLines(normalizePreviewText(before))
	afterLines := splitPreviewLines(normalizePreviewText(after))
	if syntax.Language == "markdown" {
		beforeLines, afterLines = trimSharedMarkdownHeadingPrefix(beforeLines, afterLines)
	}
	beforeHighlighted, beforeHighlightedOK := highlightedPreviewLines(joinPreviewLines(beforeLines), syntax.Lexer)
	afterHighlighted, afterHighlightedOK := highlightedPreviewLines(joinPreviewLines(afterLines), syntax.Lexer)
	beforeIndex := 0
	afterIndex := 0
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
		lines = append(lines, truncationLine(lineLimit))
		return PreviewDocument{Kind: PreviewFormatKindEditDiff, Path: path, Language: syntax.Language, Lines: lines, Truncated: true, LineLimit: lineLimit}
	}

	for _, op := range diffLineOps(beforeLines, afterLines) {
		var line PreviewLine
		switch op.kind {
		case diffOpEqual:
			if beforeHighlightedOK {
				line = beforeHighlighted[beforeIndex]
				beforeIndex++
				if afterHighlightedOK {
					afterIndex++
				}
				line.Kind = PreviewLineKindContext
				line.Prefix = " "
			} else {
				line = highlightedLine(PreviewLineKindContext, " ", syntax.Lexer, op.before)
			}
		case diffOpDelete:
			if beforeHighlightedOK {
				line = beforeHighlighted[beforeIndex]
				beforeIndex++
				line.Kind = PreviewLineKindRemoved
				line.Prefix = "-"
			} else {
				line = highlightedLine(PreviewLineKindRemoved, "-", syntax.Lexer, op.before)
			}
		case diffOpInsert:
			if afterHighlightedOK {
				line = afterHighlighted[afterIndex]
				afterIndex++
				line.Kind = PreviewLineKindAdded
				line.Prefix = "+"
			} else {
				line = highlightedLine(PreviewLineKindAdded, "+", syntax.Lexer, op.after)
			}
		}
		if !appendLine(line) {
			lines = append(lines, truncationLine(lineLimit))
			return PreviewDocument{Kind: PreviewFormatKindEditDiff, Path: path, Language: syntax.Language, Lines: lines, Truncated: true, LineLimit: lineLimit}
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

func joinPreviewLines(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

func detectPreviewLexer(path, contents string) chroma.Lexer {
	if isBinaryLike(contents) {
		return chroma.Coalesce(lexers.Fallback)
	}
	if base := strings.TrimSpace(filepath.Base(path)); base != "" {
		if lexer := lexers.Match(base); lexer != nil {
			return chroma.Coalesce(lexer)
		}
	}
	if lexer := lexers.Analyse(contents); lexer != nil {
		return chroma.Coalesce(lexer)
	}
	return chroma.Coalesce(lexers.Fallback)
}

func previewLanguageForLexer(lexer chroma.Lexer) string {
	if lexer == nil || lexer.Config() == nil {
		return "plain"
	}
	name := strings.TrimSpace(lexer.Config().Name)
	if name == "" || strings.EqualFold(name, "fallback") {
		return "plain"
	}
	return strings.ToLower(name)
}

func formatHighlightedText(text string, lexer chroma.Lexer, kind PreviewLineKind, lineLimit int) ([]PreviewLine, bool) {
	normalized := normalizePreviewText(text)
	if normalized == "" {
		return nil, false
	}

	tokens, err := chroma.Tokenise(lexer, &chroma.TokeniseOptions{State: "root", EnsureLF: false}, normalized)
	if err != nil {
		return plainTextLines(normalized, kind, lineLimit)
	}

	lines := make([]PreviewLine, 0, previewCapacity(lineLimit))
	current := PreviewLine{Kind: kind}
	appendLine := func() bool {
		if lineLimit >= 0 && len(lines) >= lineLimit {
			return false
		}
		lines = append(lines, current)
		current = PreviewLine{Kind: kind}
		return true
	}
	appendSpan := func(tokenType chroma.TokenType, text string) {
		if text == "" {
			return
		}
		if len(current.Spans) > 0 {
			last := &current.Spans[len(current.Spans)-1]
			if last.Type == tokenType {
				last.Text += text
				return
			}
		}
		current.Spans = append(current.Spans, PreviewSpan{Type: tokenType, Text: text})
	}

	for _, token := range tokens {
		if token.Type == chroma.EOFType {
			continue
		}
		remaining := token.Value
		for {
			newline := strings.IndexByte(remaining, '\n')
			if newline < 0 {
				appendSpan(token.Type, remaining)
				break
			}
			appendSpan(token.Type, remaining[:newline])
			if !appendLine() {
				return lines, true
			}
			remaining = remaining[newline+1:]
		}
	}

	if len(current.Spans) > 0 {
		if lineLimit >= 0 && len(lines) >= lineLimit {
			return lines, true
		}
		lines = append(lines, current)
	}

	return lines, false
}

func plainTextLines(text string, kind PreviewLineKind, lineLimit int) ([]PreviewLine, bool) {
	lines := splitPreviewLines(text)
	if len(lines) == 0 {
		return nil, false
	}

	out := make([]PreviewLine, 0, previewCapacity(lineLimit))
	for _, line := range lines {
		if lineLimit >= 0 && len(out) >= lineLimit {
			return out, true
		}
		out = append(out, PreviewLine{
			Kind: kind,
			Spans: []PreviewSpan{
				{Type: chroma.Text, Text: line},
			},
		})
	}
	return out, false
}

func highlightedLine(kind PreviewLineKind, prefix string, lexer chroma.Lexer, text string) PreviewLine {
	line := PreviewLine{
		Kind:   kind,
		Prefix: prefix,
	}
	line.Spans = highlightTextSpans(text, lexer)
	if len(line.Spans) == 0 {
		line.Spans = []PreviewSpan{{Type: chroma.Text, Text: text}}
	}
	return line
}

func highlightedPreviewLines(text string, lexer chroma.Lexer) ([]PreviewLine, bool) {
	lines, _ := formatHighlightedText(text, lexer, PreviewLineKindText, -1)
	if len(lines) == 0 {
		return nil, true
	}
	if len(lines) != len(splitPreviewLines(normalizePreviewText(text))) {
		return nil, false
	}
	return lines, true
}

func highlightTextSpans(text string, lexer chroma.Lexer) []PreviewSpan {
	normalized := normalizePreviewText(text)
	if normalized == "" {
		return nil
	}

	tokens, err := chroma.Tokenise(lexer, &chroma.TokeniseOptions{State: "root", EnsureLF: false}, normalized)
	if err != nil {
		return []PreviewSpan{{Type: chroma.Text, Text: normalized}}
	}

	spans := make([]PreviewSpan, 0, 4)
	for _, token := range tokens {
		if token.Type == chroma.EOFType || token.Value == "" {
			continue
		}
		if len(spans) > 0 {
			last := &spans[len(spans)-1]
			if last.Type == token.Type {
				last.Text += token.Value
				continue
			}
		}
		spans = append(spans, PreviewSpan{Type: token.Type, Text: token.Value})
	}
	return spans
}

func newHeaderLine(prefix, text string) PreviewLine {
	return PreviewLine{
		Kind:   PreviewLineKindHeader,
		Prefix: prefix,
		Spans: []PreviewSpan{
			{Type: chroma.Text, Text: text},
		},
	}
}

func truncationLine(lineLimit int) PreviewLine {
	label := "output truncated"
	if lineLimit >= 0 {
		label = fmt.Sprintf("output truncated after %d lines", lineLimit)
	}
	return PreviewLine{
		Kind:   PreviewLineKindTruncated,
		Prefix: "…",
		Spans: []PreviewSpan{
			{Type: chroma.GenericError, Text: label},
		},
	}
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

func diffLineOps(before, after []string) []diffOp {
	if len(before) == 0 && len(after) == 0 {
		return nil
	}

	dp := make([][]int, len(before)+1)
	for i := range dp {
		dp[i] = make([]int, len(after)+1)
	}
	for i := len(before) - 1; i >= 0; i-- {
		for j := len(after) - 1; j >= 0; j-- {
			if before[i] == after[j] {
				dp[i][j] = dp[i+1][j+1] + 1
				continue
			}
			if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
				continue
			}
			dp[i][j] = dp[i][j+1]
		}
	}

	ops := make([]diffOp, 0, len(before)+len(after))
	for i, j := 0, 0; i < len(before) || j < len(after); {
		switch {
		case i < len(before) && j < len(after) && before[i] == after[j]:
			ops = append(ops, diffOp{kind: diffOpEqual, before: before[i], after: after[j]})
			i++
			j++
		case j < len(after) && (i == len(before) || dp[i][j+1] > dp[i+1][j]):
			ops = append(ops, diffOp{kind: diffOpInsert, after: after[j]})
			j++
		case i < len(before):
			ops = append(ops, diffOp{kind: diffOpDelete, before: before[i]})
			i++
		}
	}
	return ops
}

func splitPreviewLines(text string) []string {
	text = normalizePreviewText(text)
	if text == "" {
		return nil
	}
	lines := strings.Split(text, "\n")
	if strings.HasSuffix(text, "\n") {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func normalizePreviewText(text string) string {
	if text == "" {
		return ""
	}
	if strings.IndexByte(text, '\r') == -1 {
		return text
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	return text
}

func isBinaryLike(text string) bool {
	if text == "" {
		return false
	}
	if strings.IndexByte(text, 0) >= 0 {
		return true
	}
	return !utf8.ValidString(text)
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
	if lines <= 0 {
		return "0,0"
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
		return 8
	}
	return min(lineLimit, 8)
}

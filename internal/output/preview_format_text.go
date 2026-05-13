package output

import (
	"fmt"
	"strings"

	"github.com/alecthomas/chroma/v2"
)

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
		parts := strings.SplitAfter(text, "\n")
		for i, part := range parts {
			if part == "" {
				continue
			}
			if strings.HasSuffix(part, "\n") {
				current.Spans = append(current.Spans, PreviewSpan{Type: tokenType, Text: strings.TrimSuffix(part, "\n")})
				if !appendLine() {
					return
				}
				continue
			}
			if i == len(parts)-1 {
				current.Spans = append(current.Spans, PreviewSpan{Type: tokenType, Text: part})
			}
		}
	}

	for _, token := range tokens {
		appendSpan(token.Type, token.Value)
	}

	if len(current.Spans) > 0 || len(lines) == 0 {
		if !appendLine() {
			return lines, true
		}
	}

	return lines, false
}

func plainTextLines(text string, kind PreviewLineKind, lineLimit int) ([]PreviewLine, bool) {
	normalized := normalizePreviewText(text)
	if normalized == "" {
		return nil, false
	}
	parts := strings.Split(normalized, "\n")
	lines := make([]PreviewLine, 0, previewCapacity(lineLimit))
	for _, part := range parts {
		if lineLimit >= 0 && len(lines) >= lineLimit {
			return lines, true
		}
		lines = append(lines, PreviewLine{Kind: kind, Spans: []PreviewSpan{{Type: chroma.Text, Text: part}}})
	}
	return lines, false
}

func highlightedLine(kind PreviewLineKind, prefix string, lexer chroma.Lexer, text string) PreviewLine {
	lines, _ := highlightedPreviewLines(text, lexer)
	if len(lines) == 0 {
		return PreviewLine{
			Kind:   kind,
			Prefix: prefix,
			Spans:  []PreviewSpan{{Type: chroma.Text, Text: text}},
		}
	}
	lines[0].Kind = kind
	lines[0].Prefix = prefix
	return lines[0]
}

func highlightedPreviewLines(text string, lexer chroma.Lexer) ([]PreviewLine, bool) {
	lines, truncated := formatHighlightedText(text, lexer, PreviewLineKindText, -1)
	if len(lines) == 0 {
		return nil, truncated
	}
	return lines, truncated
}

func highlightTextSpans(text string, lexer chroma.Lexer) []PreviewSpan {
	lines, _ := highlightedPreviewLines(text, lexer)
	if len(lines) == 0 {
		return []PreviewSpan{{Type: chroma.Text, Text: text}}
	}
	spans := make([]PreviewSpan, 0, len(lines[0].Spans))
	spans = append(spans, lines[0].Spans...)
	return spans
}

func newHeaderLine(prefix, text string) PreviewLine {
	return PreviewLine{
		Kind:   PreviewLineKindHeader,
		Prefix: prefix,
		Spans:  []PreviewSpan{{Type: chroma.Text, Text: text}},
	}
}

func truncationLine(lineLimit int) PreviewLine {
	text := "output truncated"
	if lineLimit >= 0 {
		text = fmt.Sprintf("output truncated after %d lines", lineLimit)
	}
	return PreviewLine{
		Kind:   PreviewLineKindTruncated,
		Prefix: "…",
		Spans:  []PreviewSpan{{Type: chroma.Comment, Text: text}},
	}
}

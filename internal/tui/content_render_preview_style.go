package tui

import (
	"strings"

	"github.com/alecthomas/chroma/v2"
	chromastyles "github.com/alecthomas/chroma/v2/styles"
	"github.com/charmbracelet/lipgloss"

	"github.com/luispabon/steiner/internal/output"
)

var previewSyntaxStyle = chromastyles.Get("github-dark")

func (b *contentBuffer) previewTokenStyle(token chroma.TokenType) lipgloss.Style {
	if b.previewStyleCache == nil {
		b.previewStyleCache = make(map[chroma.TokenType]lipgloss.Style)
	}
	if style, ok := b.previewStyleCache[token]; ok {
		return style
	}

	style, meaningful := chromaStyleToLipgloss(previewSyntaxStyle.Get(token))
	if !meaningful {
		style = b.baseTextStyle()
	}
	b.previewStyleCache[token] = style
	return style
}

func (b *contentBuffer) renderPreviewLine(line output.PreviewLine) string {
	text := b.renderPreviewSpans(line.Spans)
	if text == "" {
		return b.styles.FgDim.Render("")
	}
	switch line.Kind {
	case output.PreviewLineKindHeader:
		return b.styles.FgMute.Render(text)
	case output.PreviewLineKindContext:
		return b.baseTextStyle().Render(text)
	case output.PreviewLineKindAdded:
		return b.styles.Added.Render(text)
	case output.PreviewLineKindRemoved:
		return b.styles.Removed.Render(text)
	case output.PreviewLineKindTruncated:
		return b.styles.Warn.Render(text)
	default:
		return text
	}
}

func (b *contentBuffer) renderPreviewSpans(spans []output.PreviewSpan) string {
	var sb strings.Builder
	for _, span := range spans {
		sb.WriteString(b.renderPreviewSpan(span))
	}
	return sb.String()
}

func (b *contentBuffer) renderPreviewSpan(span output.PreviewSpan) string {
	style := b.previewTokenStyle(span.Type)
	return style.Render(span.Text)
}

func chromaStyleToLipgloss(entry chroma.StyleEntry) (lipgloss.Style, bool) {
	style := lipgloss.NewStyle()
	meaningful := false

	if entry.Colour.IsSet() {
		style = style.Foreground(lipgloss.Color(entry.Colour.String()))
		meaningful = true
	}
	switch entry.Bold {
	case chroma.Yes:
		style = style.Bold(true)
		meaningful = true
	case chroma.No:
		style = style.Bold(false)
		meaningful = true
	}
	switch entry.Italic {
	case chroma.Yes:
		style = style.Italic(true)
		meaningful = true
	case chroma.No:
		style = style.Italic(false)
		meaningful = true
	}
	switch entry.Underline {
	case chroma.Yes:
		style = style.Underline(true)
		meaningful = true
	case chroma.No:
		style = style.Underline(false)
		meaningful = true
	}

	return style, meaningful
}

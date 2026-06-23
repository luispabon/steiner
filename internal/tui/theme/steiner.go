package theme

import (
	"image/color"

	"charm.land/glamour/v2"
	glamourstyles "charm.land/glamour/v2/styles"
	"charm.land/lipgloss/v2"
)

func init() {
	Register("steiner", steinerTheme{})
}

type steinerTheme struct{}

func (t steinerTheme) Background() color.Color {
	return lipgloss.Color(Bg)
}

func (t steinerTheme) Foreground() color.Color {
	return lipgloss.Color(Fg)
}

func (t steinerTheme) Accent() color.Color {
	return lipgloss.Color(AccentAmber)
}

func (t steinerTheme) Muted() color.Color {
	return lipgloss.Color(FgMute)
}

func (t steinerTheme) Border() color.Color {
	return lipgloss.Color(Border)
}

func (t steinerTheme) Error() color.Color {
	return lipgloss.Color(Removed)
}

func (t steinerTheme) Warning() color.Color {
	return lipgloss.Color(Warn)
}

func (t steinerTheme) Success() color.Color {
	return lipgloss.Color(Added)
}

func (t steinerTheme) SyntaxKeyword() color.Color {
	return lipgloss.Color(AccentAmber)
}

func (t steinerTheme) SyntaxString() color.Color {
	return lipgloss.Color(Added)
}

func (t steinerTheme) SyntaxComment() color.Color {
	return lipgloss.Color(FgMute)
}

func (t steinerTheme) SyntaxFunction() color.Color {
	return lipgloss.Color(User)
}

func (t steinerTheme) SyntaxNumber() color.Color {
	return lipgloss.Color(Warn)
}

func (t steinerTheme) SyntaxOperator() color.Color {
	return lipgloss.Color(Tool)
}

func (t steinerTheme) LipGlossStyles() Styles {
	return BuildStyles(AccentAmber)
}

func (t steinerTheme) GlamourStyleSheet() glamour.TermRendererOption {
	return BuildGlamourStyleSheet(AccentAmber)
}

// BuildGlamourStyleSheet creates a glamour stylesheet using the given accent hex.
// This allows the accent colour to be changed at runtime without a full theme rebuild.
func BuildGlamourStyleSheet(accentHex string) glamour.TermRendererOption {
	// Start from dark style and customize
	cfg := glamourstyles.DarkStyleConfig
	cfg.Document.Color = ptrStr(Fg)
	cfg.CodeBlock.BackgroundColor = ptrStr(BgElev) // code fences: bg-elev background
	cfg.Code.BackgroundColor = ptrStr(BgElev2)     // inline code: dim bg
	cfg.Code.Color = ptrStr(accentHex)             // inline code: accent text
	cfg.Heading.Color = ptrStr(accentHex)
	cfg.Heading.Bold = ptrBool(true)
	cfg.Link.Color = ptrStr(accentHex)
	cfg.Emph.Color = ptrStr(FgDim)
	cfg.Emph.Italic = ptrBool(true)
	cfg.Strong.Color = ptrStr(accentHex)
	cfg.Strong.Bold = ptrBool(true)

	return glamour.WithStyles(cfg)
}

// Helper functions for pointers to primitives
func ptrStr(s string) *string {
	return &s
}

func ptrBool(b bool) *bool {
	return &b
}

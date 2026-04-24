package theme

import (
	"github.com/charmbracelet/glamour"
	glamourstyles "github.com/charmbracelet/glamour/styles"
	"github.com/charmbracelet/lipgloss"
)

func init() {
	Register("steiner", steinerTheme{})
}

type steinerTheme struct{}

func (t steinerTheme) Background() lipgloss.Color {
	return lipgloss.Color(Bg)
}

func (t steinerTheme) Foreground() lipgloss.Color {
	return lipgloss.Color(Fg)
}

func (t steinerTheme) Accent() lipgloss.Color {
	return lipgloss.Color(AccentAmber)
}

func (t steinerTheme) Muted() lipgloss.Color {
	return lipgloss.Color(FgMute)
}

func (t steinerTheme) Border() lipgloss.Color {
	return lipgloss.Color(Border)
}

func (t steinerTheme) Error() lipgloss.Color {
	return lipgloss.Color(Removed)
}

func (t steinerTheme) Warning() lipgloss.Color {
	return lipgloss.Color(Warn)
}

func (t steinerTheme) Success() lipgloss.Color {
	return lipgloss.Color(Added)
}

func (t steinerTheme) SyntaxKeyword() lipgloss.Color {
	return lipgloss.Color(AccentAmber)
}

func (t steinerTheme) SyntaxString() lipgloss.Color {
	return lipgloss.Color(Added)
}

func (t steinerTheme) SyntaxComment() lipgloss.Color {
	return lipgloss.Color(FgMute)
}

func (t steinerTheme) SyntaxFunction() lipgloss.Color {
	return lipgloss.Color(User)
}

func (t steinerTheme) SyntaxNumber() lipgloss.Color {
	return lipgloss.Color(Warn)
}

func (t steinerTheme) SyntaxOperator() lipgloss.Color {
	return lipgloss.Color(Tool)
}

func (t steinerTheme) LipGlossStyles() Styles {
	return BuildStyles(AccentAmber)
}

func (t steinerTheme) GlamourStyleSheet() glamour.TermRendererOption {
	// Start from dark style and customize
	cfg := glamourstyles.DarkStyleConfig
	cfg.Document.Color = ptrStr(Fg)
	cfg.CodeBlock.BackgroundColor = ptrStr(Bg)
	cfg.Code.BackgroundColor = ptrStr(BgElev)
	cfg.Code.Color = ptrStr(AccentAmber)
	cfg.Heading.Color = ptrStr(AccentAmber)
	cfg.Heading.Bold = ptrBool(true)
	cfg.Link.Color = ptrStr(User)
	cfg.Emph.Color = ptrStr(FgDim)
	cfg.Emph.Italic = ptrBool(true)
	cfg.Strong.Color = ptrStr(Fg)
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

func ptrUint(u uint) *uint {
	return &u
}

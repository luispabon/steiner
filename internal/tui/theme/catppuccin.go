package theme

import (
	catppuccingo "github.com/catppuccin/go"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/ansi"
	glamourstyles "github.com/charmbracelet/glamour/styles"
	"github.com/charmbracelet/lipgloss"
)

func init() {
	Register("catppuccin-mocha", mochaTheme{})
}

type mochaTheme struct{}

func (m mochaTheme) Background() lipgloss.Color {
	return lipgloss.Color(catppuccingo.Mocha.Base().Hex)
}

func (m mochaTheme) Foreground() lipgloss.Color {
	return lipgloss.Color(catppuccingo.Mocha.Text().Hex)
}

func (m mochaTheme) Accent() lipgloss.Color {
	return lipgloss.Color(catppuccingo.Mocha.Lavender().Hex)
}

func (m mochaTheme) Muted() lipgloss.Color {
	return lipgloss.Color(catppuccingo.Mocha.Overlay0().Hex)
}

func (m mochaTheme) Border() lipgloss.Color {
	return lipgloss.Color(catppuccingo.Mocha.Surface1().Hex)
}

func (m mochaTheme) Error() lipgloss.Color {
	return lipgloss.Color(catppuccingo.Mocha.Red().Hex)
}

func (m mochaTheme) Warning() lipgloss.Color {
	return lipgloss.Color(catppuccingo.Mocha.Yellow().Hex)
}

func (m mochaTheme) Success() lipgloss.Color {
	return lipgloss.Color(catppuccingo.Mocha.Green().Hex)
}

func (m mochaTheme) SyntaxKeyword() lipgloss.Color {
	return lipgloss.Color(catppuccingo.Mocha.Mauve().Hex)
}

func (m mochaTheme) SyntaxString() lipgloss.Color {
	return lipgloss.Color(catppuccingo.Mocha.Green().Hex)
}

func (m mochaTheme) SyntaxComment() lipgloss.Color {
	return lipgloss.Color(catppuccingo.Mocha.Overlay0().Hex)
}

func (m mochaTheme) SyntaxFunction() lipgloss.Color {
	return lipgloss.Color(catppuccingo.Mocha.Blue().Hex)
}

func (m mochaTheme) SyntaxNumber() lipgloss.Color {
	return lipgloss.Color(catppuccingo.Mocha.Peach().Hex)
}

func (m mochaTheme) SyntaxOperator() lipgloss.Color {
	return lipgloss.Color(catppuccingo.Mocha.Sky().Hex)
}

func (m mochaTheme) LipGlossStyles() Styles {
	mantleHex := catppuccingo.Mocha.Mantle().Hex
	textHex := catppuccingo.Mocha.Text().Hex
	lavenderHex := catppuccingo.Mocha.Lavender().Hex
	peachHex := catppuccingo.Mocha.Peach().Hex
	surface0Hex := catppuccingo.Mocha.Surface0().Hex
	overlay2Hex := catppuccingo.Mocha.Overlay2().Hex
	overlay1Hex := catppuccingo.Mocha.Overlay1().Hex
	surface1Hex := catppuccingo.Mocha.Surface1().Hex
	subtextOHex := catppuccingo.Mocha.Subtext0().Hex
	yellowHex := catppuccingo.Mocha.Yellow().Hex
	redHex := catppuccingo.Mocha.Red().Hex
	greenHex := catppuccingo.Mocha.Green().Hex

	return Styles{
		ContentPane: lipgloss.NewStyle().
			Foreground(lipgloss.Color(textHex)).
			Padding(1, 1),
		Sidebar: lipgloss.NewStyle().
			Background(lipgloss.Color(mantleHex)).
			Foreground(lipgloss.Color(textHex)),
		SidebarSection: lipgloss.NewStyle().
			Background(lipgloss.Color(mantleHex)).
			Foreground(lipgloss.Color(peachHex)).
			Bold(true),
		SidebarLabel: lipgloss.NewStyle().
			Background(lipgloss.Color(mantleHex)).
			Foreground(lipgloss.Color(lavenderHex)),
		SidebarValue: lipgloss.NewStyle().
			Background(lipgloss.Color(mantleHex)).
			Foreground(lipgloss.Color(textHex)),
		ToolBlock: lipgloss.NewStyle().
			Background(lipgloss.Color(surface0Hex)).
			Foreground(lipgloss.Color(overlay2Hex)),
		ThinkingBlock: lipgloss.NewStyle().
			Background(lipgloss.Color(surface0Hex)).
			Foreground(lipgloss.Color(overlay1Hex)).
			Italic(true),
		AssistantProse: lipgloss.NewStyle().
			Foreground(lipgloss.Color(textHex)),
		ApprovalHighlight: lipgloss.NewStyle().
			Foreground(lipgloss.Color(yellowHex)).
			Bold(true),
		InputArea: lipgloss.NewStyle().
			Foreground(lipgloss.Color(textHex)).
			Background(lipgloss.Color(surface0Hex)).
			BorderForeground(lipgloss.Color(surface1Hex)),
		StatusBar: lipgloss.NewStyle().
			Background(lipgloss.Color(mantleHex)).
			Foreground(lipgloss.Color(subtextOHex)),
		Border: lipgloss.NewStyle().
			Foreground(lipgloss.Color(surface1Hex)),
		ErrorStyle: lipgloss.NewStyle().
			Background(lipgloss.Color(mantleHex)).
			Foreground(lipgloss.Color(redHex)),
		WarningStyle: lipgloss.NewStyle().
			Foreground(lipgloss.Color(yellowHex)),
		SuccessStyle: lipgloss.NewStyle().
			Background(lipgloss.Color(mantleHex)).
			Foreground(lipgloss.Color(greenHex)),
	}
}

func (m mochaTheme) GlamourStyleSheet() glamour.TermRendererOption {
	textHex := catppuccingo.Mocha.Text().Hex
	mantleHex := catppuccingo.Mocha.Mantle().Hex
	lavenderHex := catppuccingo.Mocha.Lavender().Hex
	peachHex := catppuccingo.Mocha.Peach().Hex
	blueHex := catppuccingo.Mocha.Blue().Hex
	mauveHex := catppuccingo.Mocha.Mauve().Hex
	surface0Hex := catppuccingo.Mocha.Surface0().Hex

	ptrStr := func(s string) *string { return &s }
	ptrBool := func(b bool) *bool { return &b }
	ptrUint := func(u uint) *uint { return &u }

	// Start from glamour's built-in dark style so list bullets, numbering
	// separators, paragraph spacing and other primitives keep working. Only
	// recolor the fields that should pick up the catppuccin palette.
	cfg := glamourstyles.DarkStyleConfig

	cfg.Document.Color = ptrStr(textHex)
	cfg.Document.Margin = ptrUint(0)

	cfg.CodeBlock.StyleBlock.StylePrimitive.BackgroundColor = ptrStr(mantleHex)
	cfg.CodeBlock.Theme = "catppuccin-mocha"

	cfg.Code.BackgroundColor = ptrStr(surface0Hex)
	cfg.Code.Color = ptrStr(peachHex)

	headingColor := func(b *ansi.StyleBlock) {
		b.Color = ptrStr(lavenderHex)
		b.Bold = ptrBool(true)
	}
	headingColor(&cfg.Heading)
	headingColor(&cfg.H1)
	headingColor(&cfg.H2)
	headingColor(&cfg.H3)
	headingColor(&cfg.H4)
	headingColor(&cfg.H5)
	headingColor(&cfg.H6)

	cfg.Link.Color = ptrStr(blueHex)
	cfg.Emph.Color = ptrStr(mauveHex)
	cfg.Emph.Italic = ptrBool(true)
	cfg.Strong.Color = ptrStr(lavenderHex)
	cfg.Strong.Bold = ptrBool(true)

	return glamour.WithStyles(cfg)
}

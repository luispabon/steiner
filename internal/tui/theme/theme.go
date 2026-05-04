package theme

import (
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
)

type Theme interface {
	Background() lipgloss.Color
	Foreground() lipgloss.Color
	Accent() lipgloss.Color
	Muted() lipgloss.Color
	Border() lipgloss.Color
	Error() lipgloss.Color
	Warning() lipgloss.Color
	Success() lipgloss.Color
	SyntaxKeyword() lipgloss.Color
	SyntaxString() lipgloss.Color
	SyntaxComment() lipgloss.Color
	SyntaxFunction() lipgloss.Color
	SyntaxNumber() lipgloss.Color
	SyntaxOperator() lipgloss.Color

	LipGlossStyles() Styles
	GlamourStyleSheet() glamour.TermRendererOption
}

type Styles struct {
	ContentPane       lipgloss.Style
	Sidebar           lipgloss.Style
	SidebarSection    lipgloss.Style
	SidebarLabel      lipgloss.Style
	SidebarValue      lipgloss.Style
	CardLabel         lipgloss.Style
	ToolBlock         lipgloss.Style
	ThinkingBlock     lipgloss.Style
	AssistantProse    lipgloss.Style
	ApprovalHighlight lipgloss.Style
	InputArea         lipgloss.Style
	StatusBar         lipgloss.Style
	Border            lipgloss.Style
	ErrorStyle        lipgloss.Style
	WarningStyle      lipgloss.Style
	SuccessStyle      lipgloss.Style

	// New elevation surfaces
	BgElev  lipgloss.Style // background for elevated panels
	BgElev2 lipgloss.Style // doubly-elevated surfaces
	BgInput lipgloss.Style // input bg

	// User message chrome
	UserBar lipgloss.Style // left-bar color for user messages
	UserBg  lipgloss.Style // user message background

	// Thinking block bar
	ThinkingBar lipgloss.Style // left-bar color for thinking blocks

	// Tool tag pills (per kind)
	ToolTagBash  lipgloss.Style
	ToolTagRead  lipgloss.Style
	ToolTagWrite lipgloss.Style

	ToolTagTodo    lipgloss.Style
	ToolTagDefault lipgloss.Style
	ToolTagSearch  lipgloss.Style // search (blue)
	ToolTagGlob    lipgloss.Style // glob (blue)
	ToolTagGrep    lipgloss.Style // grep (magenta)

	// Diff colors
	Added   lipgloss.Style // added lines (green)
	Removed lipgloss.Style // removed lines (red)
	Warn    lipgloss.Style // warning (amber)

	// Text tiers
	FgDim   lipgloss.Style
	FgFaint lipgloss.Style
	FgMute  lipgloss.Style

	// Computed from accent
	AccentSoft  lipgloss.Style // soft accent fill
	AccentLine  lipgloss.Style // accent border color
	AccentColor lipgloss.Color // accent color as raw color value

	// Status bar key chip
	KeyChip lipgloss.Style

	// Accent text (fg only, no background)
	Accent lipgloss.Style
	// Accent background (accent bg + black fg)
	AccentBg lipgloss.Style

	// Input focus border ring
	InputFocusBorder lipgloss.Style

	// Command palette
	PaletteOverlay    lipgloss.Style
	PaletteInput      lipgloss.Style
	PaletteItem       lipgloss.Style
	PaletteItemActive lipgloss.Style

	// Scrollbar
	Scrollbar lipgloss.Style
}

// BuildStyles builds a full Styles from an accent hex color string.
// Used when the accent preset changes at runtime.
func BuildStyles(accentHex string) Styles {
	// recompute AccentSoft and AccentLine from the given accentHex
	accentSoft := blendHex(accentHex, Bg, 0.09)
	accentLine := blendHex(accentHex, Bg, 0.35)
	return buildStylesInternal(accentHex, accentSoft, accentLine)
}

// buildStylesInternal builds all Styles fields from an accent hex and pre-computed soft variants.
// Used by both steiner theme and BuildStyles.
func buildStylesInternal(accentHex, accentSoft, accentLine string) Styles {
	return Styles{
		ContentPane:       lipgloss.NewStyle().Background(lipgloss.Color(BgElev)).PaddingTop(1).PaddingLeft(3).PaddingRight(3),
		Sidebar:           lipgloss.NewStyle().Background(lipgloss.Color(Black)),
		SidebarSection:    lipgloss.NewStyle().Foreground(lipgloss.Color(FgDim)),
		SidebarLabel:      lipgloss.NewStyle().Foreground(lipgloss.Color(FgFaint)),
		SidebarValue:      lipgloss.NewStyle().Foreground(lipgloss.Color(Fg)),
		CardLabel:         lipgloss.NewStyle().Foreground(lipgloss.Color(FgLabel)).Bold(true),
		ToolBlock:         lipgloss.NewStyle().BorderLeft(true).BorderStyle(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color(Tool)).Padding(1),
		ThinkingBlock:     lipgloss.NewStyle().BorderLeft(true).BorderStyle(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color(Thinking)),
		AssistantProse:    lipgloss.NewStyle().Foreground(lipgloss.Color(Fg)),
		ApprovalHighlight: lipgloss.NewStyle().Background(lipgloss.Color(accentSoft)).Foreground(lipgloss.Color(accentHex)),
		InputArea:         lipgloss.NewStyle().Background(lipgloss.Color(BgInput)).Foreground(lipgloss.Color(Fg)),
		StatusBar:         lipgloss.NewStyle().Background(lipgloss.Color(BgElev)).Foreground(lipgloss.Color(FgDim)).Padding(0, 2),
		Border:            lipgloss.NewStyle().Foreground(lipgloss.Color(Border)),
		ErrorStyle:        lipgloss.NewStyle().Foreground(lipgloss.Color(Removed)),
		WarningStyle:      lipgloss.NewStyle().Foreground(lipgloss.Color(Warn)),
		SuccessStyle:      lipgloss.NewStyle().Foreground(lipgloss.Color(Added)),

		BgElev:  lipgloss.NewStyle().Background(lipgloss.Color(BgElev)),
		BgElev2: lipgloss.NewStyle().Background(lipgloss.Color(BgElev2)),
		BgInput: lipgloss.NewStyle().Background(lipgloss.Color(BgInput)),

		UserBar: lipgloss.NewStyle().Foreground(lipgloss.Color(accentHex)),
		UserBg:  lipgloss.NewStyle().Background(lipgloss.Color(UserSoft)).Foreground(lipgloss.Color(Fg)),

		ThinkingBar: lipgloss.NewStyle().Foreground(lipgloss.Color(Thinking)),

		ToolTagBash:  lipgloss.NewStyle().Background(lipgloss.Color(AccentAmber)).Foreground(lipgloss.Color(Black)).Padding(0, 1).Bold(true),
		ToolTagRead:  lipgloss.NewStyle().Background(lipgloss.Color(ToolCyan)).Foreground(lipgloss.Color(Black)).Padding(0, 1).Bold(true),
		ToolTagWrite: lipgloss.NewStyle().Background(lipgloss.Color(ToolGrn)).Foreground(lipgloss.Color(Black)).Padding(0, 1).Bold(true),

		ToolTagTodo:    lipgloss.NewStyle().Background(lipgloss.Color(Warn)).Foreground(lipgloss.Color(Black)).Padding(0, 1).Bold(true),
		ToolTagDefault: lipgloss.NewStyle().Background(lipgloss.Color(ToolBlue)).Foreground(lipgloss.Color(Black)).Padding(0, 1).Bold(true),
		ToolTagSearch:  lipgloss.NewStyle().Background(lipgloss.Color(ToolBlue)).Foreground(lipgloss.Color(Black)).Padding(0, 1).Bold(true),
		ToolTagGlob:    lipgloss.NewStyle().Background(lipgloss.Color(ToolBlue)).Foreground(lipgloss.Color(Black)).Padding(0, 1).Bold(true),
		ToolTagGrep:    lipgloss.NewStyle().Background(lipgloss.Color(ToolMag)).Foreground(lipgloss.Color(Black)).Padding(0, 1).Bold(true),

		Added:   lipgloss.NewStyle().Foreground(lipgloss.Color(Added)),
		Removed: lipgloss.NewStyle().Foreground(lipgloss.Color(Removed)),
		Warn:    lipgloss.NewStyle().Foreground(lipgloss.Color(Warn)),

		FgDim:   lipgloss.NewStyle().Foreground(lipgloss.Color(FgDim)),
		FgFaint: lipgloss.NewStyle().Foreground(lipgloss.Color(FgFaint)),
		FgMute:  lipgloss.NewStyle().Foreground(lipgloss.Color(FgMute)),

		AccentSoft:  lipgloss.NewStyle().Foreground(lipgloss.Color(accentSoft)),
		AccentLine:  lipgloss.NewStyle().Foreground(lipgloss.Color(accentLine)),
		AccentColor: lipgloss.Color(accentHex),

		Accent:   lipgloss.NewStyle().Foreground(lipgloss.Color(accentHex)),
		AccentBg: lipgloss.NewStyle().Background(lipgloss.Color(accentHex)).Foreground(lipgloss.Color(Black)),

		KeyChip: lipgloss.NewStyle().Background(lipgloss.Color(FgFaint)).Foreground(lipgloss.Color(Black)).Padding(0, 1),

		Scrollbar: lipgloss.NewStyle().
			Foreground(lipgloss.Color(BorderSoft)),

		InputFocusBorder: lipgloss.NewStyle().
			BorderStyle(lipgloss.NormalBorder()).
			BorderForeground(lipgloss.Color(accentLine)),

		PaletteOverlay: lipgloss.NewStyle().
			Background(lipgloss.Color(BgElev)).
			Border(lipgloss.NormalBorder()).
			BorderForeground(lipgloss.Color(Border)),
		PaletteInput:      lipgloss.NewStyle().Foreground(lipgloss.Color(Fg)),
		PaletteItem:       lipgloss.NewStyle().Foreground(lipgloss.Color(Fg)),
		PaletteItemActive: lipgloss.NewStyle().Background(lipgloss.Color(accentSoft)),
	}
}

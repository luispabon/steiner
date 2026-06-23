package theme

import (
	"image/color"

	"charm.land/glamour/v2"
	"charm.land/lipgloss/v2"
)

// Theme describes the colors and styles needed to render the TUI.
type Theme interface {
	Background() color.Color
	Foreground() color.Color
	Accent() color.Color
	Muted() color.Color
	Border() color.Color
	Error() color.Color
	Warning() color.Color
	Success() color.Color
	SyntaxKeyword() color.Color
	SyntaxString() color.Color
	SyntaxComment() color.Color
	SyntaxFunction() color.Color
	SyntaxNumber() color.Color
	SyntaxOperator() color.Color

	LipGlossStyles() Styles
	GlamourStyleSheet() glamour.TermRendererOption
}

// Styles groups all rendered styles for the TUI.
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

	// ToolTagStyles maps normalized tool names to pill styles.
	ToolTagStyles map[string]lipgloss.Style
	// ToolTagDefault is the fallback pill style for unknown tools.
	ToolTagDefault lipgloss.Style

	// ToolBorderStyles maps normalized tool names to border styles.
	ToolBorderStyles map[string]lipgloss.Style
	// ToolBorderDefault is the fallback border style for unknown tools.
	ToolBorderDefault lipgloss.Style

	// DelegateTagStyles maps normalized delegate labels to pill styles.
	DelegateTagStyles map[string]lipgloss.Style
	// DelegateTagDefault is the fallback pill style for unknown delegates.
	DelegateTagDefault lipgloss.Style

	// DelegateBorderStyles maps normalized delegate labels to border styles.
	DelegateBorderStyles map[string]lipgloss.Style
	// DelegateBorderDefault is the fallback border style for unknown delegates.
	DelegateBorderDefault lipgloss.Style

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
	AccentColor color.Color    // accent color as raw color value

	// Status bar key chip
	KeyChip lipgloss.Style

	// Accent text (fg only, no background)
	Accent lipgloss.Style
	// Accent background (accent bg + black fg)
	AccentBg lipgloss.Style

	// StatusTag is the bold accent-colored label used to mark system status
	// lines in the conversation view (e.g. "status" prefix).
	StatusTag lipgloss.Style

	// Input focus border ring
	InputFocusBorder lipgloss.Style

	// Command palette
	PaletteOverlay    lipgloss.Style
	PaletteInput      lipgloss.Style
	PaletteItem       lipgloss.Style
	PaletteItemActive lipgloss.Style

	// Scrollbar
	Scrollbar      lipgloss.Style
	ScrollbarTrack lipgloss.Style

	// SelectionStyle is used to highlight mouse-selected text in the viewport.
	SelectionStyle lipgloss.Style
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
		ToolBlock:         lipgloss.NewStyle().Background(lipgloss.Color(BgElev)).BorderLeft(true).BorderStyle(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color(Tool)).Padding(1),
		ThinkingBlock:     lipgloss.NewStyle().Background(lipgloss.Color(BgElev)).BorderLeft(true).BorderStyle(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color(Thinking)).Padding(1),
		AssistantProse:    lipgloss.NewStyle().Background(lipgloss.Color(BgElev)).Foreground(lipgloss.Color(Fg)),
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

		ToolTagStyles: map[string]lipgloss.Style{
			"bash":      lipgloss.NewStyle().Background(lipgloss.Color(AccentAmber)).Foreground(lipgloss.Color(Black)).Padding(0, 1).Bold(true),
			"read":      lipgloss.NewStyle().Background(lipgloss.Color(ToolCyan)).Foreground(lipgloss.Color(Black)).Padding(0, 1).Bold(true),
			"read_file": lipgloss.NewStyle().Background(lipgloss.Color(ToolCyan)).Foreground(lipgloss.Color(Black)).Padding(0, 1).Bold(true),
			"mutate":    lipgloss.NewStyle().Background(lipgloss.Color(ToolGrn)).Foreground(lipgloss.Color(Black)).Padding(0, 1).Bold(true),
			"todo":      lipgloss.NewStyle().Background(lipgloss.Color(Warn)).Foreground(lipgloss.Color(Black)).Padding(0, 1).Bold(true),
			"search":    lipgloss.NewStyle().Background(lipgloss.Color(ToolBlue)).Foreground(lipgloss.Color(Black)).Padding(0, 1).Bold(true),
			"glob":      lipgloss.NewStyle().Background(lipgloss.Color(ToolBlue)).Foreground(lipgloss.Color(Black)).Padding(0, 1).Bold(true),
			"grep":      lipgloss.NewStyle().Background(lipgloss.Color(ToolMag)).Foreground(lipgloss.Color(Black)).Padding(0, 1).Bold(true),
		},
		ToolTagDefault: lipgloss.NewStyle().Background(lipgloss.Color(ToolBlue)).Foreground(lipgloss.Color(Black)).Padding(0, 1).Bold(true),

		ToolBorderStyles: map[string]lipgloss.Style{
			"bash":      lipgloss.NewStyle().Foreground(lipgloss.Color(ToolAmberLine)),
			"read":      lipgloss.NewStyle().Foreground(lipgloss.Color(ToolCyanLine)),
			"read_file": lipgloss.NewStyle().Foreground(lipgloss.Color(ToolCyanLine)),
			"mutate":    lipgloss.NewStyle().Foreground(lipgloss.Color(ToolGrnLine)),
			"todo":      lipgloss.NewStyle().Foreground(lipgloss.Color(WarnLine)),
			"search":    lipgloss.NewStyle().Foreground(lipgloss.Color(ToolBlueLine)),
			"glob":      lipgloss.NewStyle().Foreground(lipgloss.Color(ToolBlueLine)),
			"grep":      lipgloss.NewStyle().Foreground(lipgloss.Color(ToolMagLine)),
		},
		ToolBorderDefault: lipgloss.NewStyle().Foreground(lipgloss.Color(ToolBlueLine)),

		DelegateTagStyles: map[string]lipgloss.Style{
			"explore":  lipgloss.NewStyle().Foreground(lipgloss.Color(ToolCyan)).Bold(true),
			"research": lipgloss.NewStyle().Foreground(lipgloss.Color(DelegateViolet)).Bold(true),
			"code":     lipgloss.NewStyle().Foreground(lipgloss.Color(AccentAmber)).Bold(true),
			"plan":     lipgloss.NewStyle().Foreground(lipgloss.Color(Thinking)).Bold(true),
			"verify":   lipgloss.NewStyle().Foreground(lipgloss.Color(ToolMag)).Bold(true),
		},
		DelegateTagDefault: lipgloss.NewStyle().Foreground(lipgloss.Color(ToolGrn)).Bold(true),

		DelegateBorderStyles: map[string]lipgloss.Style{
			"explore":  lipgloss.NewStyle().Foreground(lipgloss.Color(ToolCyanLine)),
			"research": lipgloss.NewStyle().Foreground(lipgloss.Color(DelegateVioletLine)),
			"code":     lipgloss.NewStyle().Foreground(lipgloss.Color(ToolAmberLine)),
			"plan":     lipgloss.NewStyle().Foreground(lipgloss.Color(DelegateThinkingLine)),
			"verify":   lipgloss.NewStyle().Foreground(lipgloss.Color(ToolMagLine)),
		},
		DelegateBorderDefault: lipgloss.NewStyle().Foreground(lipgloss.Color(ToolGrnLine)),

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

		StatusTag: lipgloss.NewStyle().Foreground(lipgloss.Color(accentHex)).Bold(true),

		KeyChip: lipgloss.NewStyle().Background(lipgloss.Color(FgFaint)).Foreground(lipgloss.Color(Black)).Padding(0, 1),

		Scrollbar: lipgloss.NewStyle().
			Background(lipgloss.Color(BgElev)).
			Foreground(lipgloss.Color(BorderSoft)),
		ScrollbarTrack: lipgloss.NewStyle().Background(lipgloss.Color(BgElev)),

		SelectionStyle: lipgloss.NewStyle().Background(lipgloss.Color("#3a4a5a")),

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

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
	ToolTagBash     lipgloss.Style
	ToolTagRead     lipgloss.Style
	ToolTagWrite    lipgloss.Style
	ToolTagGlobGrep lipgloss.Style
	ToolTagTodo     lipgloss.Style
	ToolTagDefault  lipgloss.Style

	// Diff colors
	Added   lipgloss.Style // added lines (green)
	Removed lipgloss.Style // removed lines (red)
	Warn    lipgloss.Style // warning (amber)

	// Text tiers
	FgDim   lipgloss.Style
	FgFaint lipgloss.Style
	FgMute  lipgloss.Style

	// Computed from accent
	AccentSoft lipgloss.Style // soft accent fill
	AccentLine lipgloss.Style // accent border color

	// Status bar key chip
	KeyChip lipgloss.Style

	// Command palette
	PaletteOverlay    lipgloss.Style
	PaletteInput      lipgloss.Style
	PaletteItem       lipgloss.Style
	PaletteItemActive lipgloss.Style
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
	// Compute additional tool tag colors
	readColor := blendHex("#80A8E8", Bg, 0.10)
	globGrepColor := blendHex("#D080C8", Bg, 0.10)

	return Styles{
		ContentPane:       lipgloss.NewStyle().Background(lipgloss.Color(Bg)),
		Sidebar:           lipgloss.NewStyle().Background(lipgloss.Color(Bg)),
		SidebarSection:    lipgloss.NewStyle().Foreground(lipgloss.Color(FgDim)),
		SidebarLabel:      lipgloss.NewStyle().Foreground(lipgloss.Color(FgFaint)),
		SidebarValue:      lipgloss.NewStyle().Foreground(lipgloss.Color(Fg)),
		ToolBlock:         lipgloss.NewStyle().BorderLeft(true).BorderStyle(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color(Tool)),
		ThinkingBlock:     lipgloss.NewStyle().BorderLeft(true).BorderStyle(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color(Thinking)),
		AssistantProse:    lipgloss.NewStyle().Foreground(lipgloss.Color(Fg)),
		ApprovalHighlight: lipgloss.NewStyle().Background(lipgloss.Color(accentSoft)).Foreground(lipgloss.Color(accentHex)),
		InputArea:         lipgloss.NewStyle().Background(lipgloss.Color(BgInput)).Foreground(lipgloss.Color(Fg)),
		StatusBar:         lipgloss.NewStyle().Background(lipgloss.Color(BgElev)).Foreground(lipgloss.Color(FgDim)).Padding(0, 1),
		Border:            lipgloss.NewStyle().Foreground(lipgloss.Color(Border)),
		ErrorStyle:        lipgloss.NewStyle().Foreground(lipgloss.Color(Removed)),
		WarningStyle:      lipgloss.NewStyle().Foreground(lipgloss.Color(Warn)),
		SuccessStyle:      lipgloss.NewStyle().Foreground(lipgloss.Color(Added)),

		BgElev:  lipgloss.NewStyle().Background(lipgloss.Color(BgElev)),
		BgElev2: lipgloss.NewStyle().Background(lipgloss.Color(BgElev2)),
		BgInput: lipgloss.NewStyle().Background(lipgloss.Color(BgInput)),

		UserBar: lipgloss.NewStyle().Foreground(lipgloss.Color(User)),
		UserBg:  lipgloss.NewStyle().Background(lipgloss.Color(UserSoft)),

		ThinkingBar: lipgloss.NewStyle().Foreground(lipgloss.Color(Thinking)),

		ToolTagBash:     lipgloss.NewStyle().Background(lipgloss.Color(accentSoft)).Foreground(lipgloss.Color(accentHex)),
		ToolTagRead:     lipgloss.NewStyle().Background(lipgloss.Color(readColor)).Foreground(lipgloss.Color("#80A8E8")),
		ToolTagWrite:    lipgloss.NewStyle().Background(lipgloss.Color(AddedSoft)).Foreground(lipgloss.Color(Added)),
		ToolTagGlobGrep: lipgloss.NewStyle().Background(lipgloss.Color(globGrepColor)).Foreground(lipgloss.Color("#D080C8")),
		ToolTagTodo:     lipgloss.NewStyle().Background(lipgloss.Color(WarnSoft)).Foreground(lipgloss.Color(Warn)),
		ToolTagDefault:  lipgloss.NewStyle().Background(lipgloss.Color(ToolSoft)).Foreground(lipgloss.Color(Tool)),

		Added:   lipgloss.NewStyle().Foreground(lipgloss.Color(Added)),
		Removed: lipgloss.NewStyle().Foreground(lipgloss.Color(Removed)),
		Warn:    lipgloss.NewStyle().Foreground(lipgloss.Color(Warn)),

		FgDim:   lipgloss.NewStyle().Foreground(lipgloss.Color(FgDim)),
		FgFaint: lipgloss.NewStyle().Foreground(lipgloss.Color(FgFaint)),
		FgMute:  lipgloss.NewStyle().Foreground(lipgloss.Color(FgMute)),

		AccentSoft: lipgloss.NewStyle().Foreground(lipgloss.Color(accentSoft)),
		AccentLine: lipgloss.NewStyle().Foreground(lipgloss.Color(accentLine)),

		KeyChip: lipgloss.NewStyle().Background(lipgloss.Color(BgElev2)).Foreground(lipgloss.Color(FgFaint)).Padding(0, 1),

		PaletteOverlay:    lipgloss.NewStyle().Background(lipgloss.Color(FgMute)),
		PaletteInput:      lipgloss.NewStyle().BorderBottom(true).BorderStyle(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color(BorderSoft)),
		PaletteItem:       lipgloss.NewStyle().Foreground(lipgloss.Color(Fg)),
		PaletteItemActive: lipgloss.NewStyle().Background(lipgloss.Color(accentSoft)).Foreground(lipgloss.Color(Fg)),
	}
}

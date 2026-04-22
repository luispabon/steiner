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
}

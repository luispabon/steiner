package tui

import "github.com/charmbracelet/lipgloss"

const (
	catppuccinMochaBase      = "#1e1e2e"
	catppuccinMochaMantle    = "#181825"
	catppuccinMochaSurface0  = "#313244"
	catppuccinMochaSurface1  = "#45475a"
	catppuccinMochaText      = "#cdd6f4"
	catppuccinMochaSubtext0  = "#a6adc8"
	catppuccinMochaOverlay0  = "#6c7086"
	catppuccinMochaYellow    = "#f9e2af"
	catppuccinMochaPeach     = "#fab387"
	catppuccinMochaLavender  = "#b4befe"
	catppuccinMochaRosewater = "#f5e0dc"
	catppuccinMochaMaroon    = "#eba0ac"
	catppuccinMochaMauve     = "#cba6f7"
)

var (
	contentPaneStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color(catppuccinMochaText)).
				Background(lipgloss.Color(catppuccinMochaBase)).
				Padding(0, 1)

	sidebarStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color(catppuccinMochaText)).
			Background(lipgloss.Color(catppuccinMochaMantle)).
			BorderStyle(lipgloss.NormalBorder()).
			BorderLeft(true).
			BorderForeground(lipgloss.Color(catppuccinMochaSurface1)).
			Padding(0, 1)

	toolBlockStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color(catppuccinMochaSubtext0)).
			Background(lipgloss.Color(catppuccinMochaSurface0)).
			BorderStyle(lipgloss.NormalBorder()).
			BorderForeground(lipgloss.Color(catppuccinMochaOverlay0)).
			Padding(0, 1)

	thinkingBlockStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color(catppuccinMochaOverlay0)).
				Background(lipgloss.Color(catppuccinMochaSurface0)).
				Italic(true).
				Padding(0, 1)

	assistantProseStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color(catppuccinMochaText)).
				Background(lipgloss.Color(catppuccinMochaBase))

	approvalHighlightStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color(catppuccinMochaBase)).
				Background(lipgloss.Color(catppuccinMochaYellow)).
				Bold(true).
				Padding(0, 1)

	inputAreaStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color(catppuccinMochaText)).
			Background(lipgloss.Color(catppuccinMochaMantle)).
			BorderStyle(lipgloss.NormalBorder()).
			BorderTop(true).
			BorderForeground(lipgloss.Color(catppuccinMochaSurface1)).
			Padding(0, 1)

	borderStyle = lipgloss.NewStyle().
			BorderStyle(lipgloss.NormalBorder()).
			BorderForeground(lipgloss.Color(catppuccinMochaSurface1))

	sidebarLabelStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color(catppuccinMochaLavender)).
				Bold(true)

	sidebarValueStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color(catppuccinMochaRosewater))

	statusBarStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color(catppuccinMochaText)).
			Background(lipgloss.Color(catppuccinMochaSurface0)).
			BorderStyle(lipgloss.NormalBorder()).
			BorderTop(true).
			BorderForeground(lipgloss.Color(catppuccinMochaSurface1)).
			Padding(0, 1)

	approvalAccentStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color(catppuccinMochaMaroon)).
				Bold(true)

	toolAccentStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color(catppuccinMochaPeach)).
			Bold(true)
)

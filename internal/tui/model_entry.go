package tui

// ModelEntry is the TUI-local description of a selectable model.
type ModelEntry struct {
	Ref              string
	Display          string
	SupportedEfforts []string
	Current          bool
}

package tui

// OverlayShell defines the planned API surface for reusable framed overlays
// that can be placed over the base TUI model. Concrete overlays (e.g.
// filePickerOverlay, paletteOverlay) will embed or implement this contract in a
// later stage.
//
// Planned API surface:
//
//   - Open/close state: IsOpen() bool, open/close methods returning a new value.
//   - Dimensions: width and height passed in from the parent model on resize so
//     the overlay can compute its own inner dimensions.
//   - Title: a short string rendered in the header region of the framed box.
//   - Body region: a rendered string produced by the concrete overlay and placed
//     inside the padded body area.
//   - Footer chips: a slice of key-hint strings rendered in the footer bar
//     (e.g. "↵ select", "esc close").
//   - Placement: the overlay is layered over the base model view using
//     lipgloss overlay helpers; the shell is responsible for centering and
//     z-order, not the caller.
//
// TODO(stage-1): Extract the framed box, width/height handling, footer chip
// rendering, and scroll-window helpers from filePickerOverlay into a concrete
// OverlayShell implementation. Keep file-search-specific logic (walk, filter,
// selection) in file_picker.go.
type OverlayShell struct {
	// open tracks whether the overlay is currently visible.
	open bool
	// width and height are the terminal dimensions passed in from the parent.
	width  int
	height int
	// title is displayed in the overlay header.
	title string
}

// IsOpen reports whether the overlay is currently visible.
func (o OverlayShell) IsOpen() bool {
	return o.open
}

// WithDimensions returns a copy of the overlay shell with updated terminal dimensions.
func (o OverlayShell) WithDimensions(width, height int) OverlayShell {
	o.width = width
	o.height = height
	return o
}

// WithTitle returns a copy of the overlay shell with the given title.
func (o OverlayShell) WithTitle(title string) OverlayShell {
	o.title = title
	return o
}

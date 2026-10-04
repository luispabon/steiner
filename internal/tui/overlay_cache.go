package tui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// overlayComposeCache memoises composeCenteredOverlay and the bottom-anchored
// placement steps on the Model. All comparisons are plain string equality.
//
// Two levels for the centred compose:
//   - whole frame: same base, overlay and size returns the previous result;
//   - overlay plan: the cut overlay rows depend only on the overlay string and
//     size, so a base-only change (wheel scroll behind the overlay) reuses them.
//
// When the caller vouches that every base row is already exactly width cells
// wide (renderBaseView guarantees it; see TestBaseViewRowsAreFullWidth), rows
// outside the overlay are copied through and only covered rows are cut.
type overlayComposeCache struct {
	valid     bool
	fullWidth bool
	base      string
	overlay   string
	width     int
	height    int
	result    string

	plan overlayPlan

	bottom [bottomOverlayMax]bottomPlaceEntry

	composes int
}

// bottomOverlayMax bounds the number of bottom-anchored overlays bottomOverlays can return.
const bottomOverlayMax = 12

type overlayPlan struct {
	valid          bool
	overlay        string
	width, height  int
	empty          bool
	startX, startY int
	endY           int
	visibleStart   int
	visibleWidth   int
	lines          []string
	mids           []string
}

type bottomPlaceEntry struct {
	valid             bool
	base, overlay     string
	inputHeight, xOff int
	shellHeight       int
	result            string
}

// compose centres overlay over base within width x height. fullWidth asserts
// that every row of base is exactly width cells wide and that base has height
// rows; pass false for any base that has not come straight from renderBaseView.
func (c *overlayComposeCache) compose(base, overlay string, width, height int, fullWidth bool) string {
	if width < 1 || height < 1 {
		return base
	}
	if c.valid && c.width == width && c.height == height && c.fullWidth == fullWidth && c.overlay == overlay && c.base == base {
		return c.result
	}
	c.composes++
	result := c.build(base, overlay, width, height, fullWidth)
	c.valid, c.fullWidth, c.base, c.overlay, c.width, c.height, c.result = true, fullWidth, base, overlay, width, height, result
	return result
}

func (c *overlayComposeCache) overlayPlanFor(overlay string, width, height int) *overlayPlan {
	p := &c.plan
	if p.valid && p.width == width && p.height == height && p.overlay == overlay {
		return p
	}
	lines := strings.Split(overlay, "\n")
	*p = overlayPlan{valid: true, overlay: overlay, width: width, height: height, lines: lines}
	maxW := 0
	for _, line := range lines {
		maxW = max(maxW, ansi.StringWidth(line))
	}
	if maxW == 0 {
		p.empty = true
		return p
	}
	p.startX = (width - maxW) / 2
	p.startY = (height - len(lines)) / 2
	p.endY = min(height, p.startY+len(lines))
	p.visibleStart = max(0, p.startX)
	overlayOffset := max(0, -p.startX)
	p.visibleWidth = min(maxW-overlayOffset, width-p.visibleStart)
	if p.visibleWidth > 0 {
		p.mids = make([]string, len(lines))
		for i, line := range lines {
			p.mids[i] = padCells(ansi.Cut(line, overlayOffset, overlayOffset+p.visibleWidth), p.visibleWidth)
		}
	}
	return p
}

// padCells is padOverlayLine for single-line strings, measured with ansi
// directly to skip lipgloss.Width's line splitting.
func padCells(line string, width int) string {
	if width <= 0 {
		return ""
	}
	if w := ansi.StringWidth(line); w < width {
		return line + strings.Repeat(" ", width-w)
	}
	return line
}

func (c *overlayComposeCache) build(base, overlay string, width, height int, fullWidth bool) string {
	p := c.overlayPlanFor(overlay, width, height)
	covered := !p.empty && p.visibleWidth > 0
	if fullWidth {
		// Cheap guards against a stale layout; the per-row guarantee itself is
		// pinned by TestBaseViewRowsAreFullWidth.
		first, _, _ := strings.Cut(base, "\n")
		fullWidth = strings.Count(base, "\n") == height-1 && ansi.StringWidth(first) == width
	}

	var sb strings.Builder
	sb.Grow(len(base) + len(base)/8)
	rest := base
	for y := 0; y < height; y++ {
		var line string
		if rest != "" || y == 0 {
			if i := strings.IndexByte(rest, '\n'); i >= 0 {
				line, rest = rest[:i], rest[i+1:]
			} else {
				line, rest = rest, ""
			}
		}
		if y > 0 {
			sb.WriteByte('\n')
		}
		inRange := covered && y >= max(0, p.startY) && y < p.endY
		if !inRange {
			if fullWidth {
				sb.WriteString(line)
			} else {
				sb.WriteString(padOverlayLine(ansi.Cut(line, 0, width), width))
			}
			continue
		}
		if !fullWidth {
			line = padOverlayLine(ansi.Cut(line, 0, width), width)
		}
		p.writeCovered(&sb, line, y, width, fullWidth)
	}
	return sb.String()
}

func (p *overlayPlan) writeCovered(sb *strings.Builder, line string, y, width int, fullWidth bool) {
	rightStart := p.visibleStart + p.visibleWidth
	var right string
	switch {
	case rightStart >= width:
	case fullWidth:
		// Cut(line, rightStart, width) truncates to width first, which is a
		// no-op for a row that is already exactly width cells wide.
		right = ansi.TruncateLeft(line, rightStart, "")
	default:
		right = ansi.Cut(line, rightStart, width)
	}
	sb.WriteString(padCells(ansi.Cut(line, 0, p.visibleStart), p.visibleStart))
	sb.WriteString(p.mids[y-p.startY])
	sb.WriteString(padCells(right, width-rightStart))
}

// placeBottom memoises one OverlayShell.PlaceBottomAnchoredAt step for the
// i-th bottom-anchored overlay.
func (c *overlayComposeCache) placeBottom(i int, o bottomAnchoredOverlay, base, overlay string, inputHeight, xOffset int) string {
	shellHeight := o.shellHeight()
	if i >= len(c.bottom) {
		return o.PlaceBottomAnchoredAt(base, overlay, inputHeight, xOffset)
	}
	e := &c.bottom[i]
	if e.valid && e.shellHeight == shellHeight && e.inputHeight == inputHeight && e.xOff == xOffset && e.overlay == overlay && e.base == base {
		return e.result
	}
	result := o.PlaceBottomAnchoredAt(base, overlay, inputHeight, xOffset)
	*e = bottomPlaceEntry{valid: true, base: base, overlay: overlay, inputHeight: inputHeight, xOff: xOffset, shellHeight: shellHeight, result: result}
	return result
}

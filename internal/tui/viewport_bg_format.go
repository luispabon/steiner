package tui

import "github.com/luispabon/steiner/internal/tui/theme"

// bgFormatCache holds the last transcript formatted by
// theme.FormatContent (background restoration plus padding) and the width and
// background it was formatted for. Validity is keyed on the source bytes
// themselves, so no content-buffer invalidation (gen bump, showThinking,
// segment rewrite) can leave it stale: a changed transcript reuses only the
// whole lines it still shares with source. The zero value is a correct entry:
// FormatContent("", 0, "") is "".
type bgFormatCache struct {
	source string
	output string
	width  int
	bg     string
}

// format returns theme.FormatContent(rendered, width, bg), reusing the cached
// output (all of it when rendered is unchanged) while width and bg match.
func (c *bgFormatCache) format(rendered string, width int, bg string) string {
	if c.width == width && c.bg == bg {
		c.output = theme.ReformatContent(c.source, c.output, rendered, width, bg)
	} else {
		c.output = theme.FormatContent(rendered, width, bg)
	}
	c.source, c.width, c.bg = rendered, width, bg
	return c.output
}

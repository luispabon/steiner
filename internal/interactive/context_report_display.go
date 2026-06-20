package interactive

import (
	"strings"

	"github.com/luispabon/steiner/internal/output"
)

// ClassifyContextReportDisplay returns the display mode for a context report
// based on its content. Short single-line content (<=100 chars, no newline)
// returns "inline"; long or multi-line content returns "overlay".
func ClassifyContextReportDisplay(content string) string {
	if strings.Contains(content, "\n") || len(content) > 100 {
		return output.ContextReportDisplayOverlay
	}
	return output.ContextReportDisplayInline
}

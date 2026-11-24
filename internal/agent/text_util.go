package agent

import (
	"github.com/luispabon/steiner/internal/output"
)

func summarizeTextPreview(text string, limit int) string {
	return output.TruncateWithEllipsis(text, limit)
}

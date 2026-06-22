package builtin

const defaultMaxLineRunes = 400

// lineBoundingConfig controls how rendered lines are truncated.
type lineBoundingConfig struct {
	maxLineRunes   int // per-line rune cap; 0 means use default (400)
	maxOutputRunes int // total output rune cap; 0 means no cap
}

// boundLines truncates individual lines by rune count and optionally caps
// total output. It returns the bounded lines and truncation reasons.
func boundLines(lines []string, cfg lineBoundingConfig) ([]string, []string) {
	if cfg.maxLineRunes <= 0 {
		cfg.maxLineRunes = defaultMaxLineRunes
	}

	hasLineTruncation := false
	result := make([]string, 0, len(lines))

	for _, line := range lines {
		runes := []rune(line)
		if len(runes) > cfg.maxLineRunes {
			hasLineTruncation = true
			line = string(runes[:cfg.maxLineRunes]) + "…<truncated>"
		}
		result = append(result, line)
	}

	var reasons []string
	if hasLineTruncation {
		reasons = append(reasons, "line_length_capped")
	}

	if cfg.maxOutputRunes > 0 {
		total := 0
		for i, line := range result {
			lineLen := len([]rune(line))
			sep := 0
			if i > 0 {
				sep = 1 // newline between lines when rejoined
			}
			if total+sep+lineLen > cfg.maxOutputRunes {
				result = result[:i]
				reasons = append(reasons, "output_length_capped")
				break
			}
			total += sep + lineLen
		}
	}

	return result, reasons
}

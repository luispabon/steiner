package builtin

const defaultMaxLineRunes = 400

// readMaxLineRunes is the per-line cap for read. Prose-heavy files (e.g.
// markdown plan files) legitimately contain long wrapped lines, so read uses
// a higher cap than grep's 400.
const readMaxLineRunes = 2000

// readMaxOutputRunes caps total read output per page (runes, not bytes).
// When hit, read returns a contiguous prefix of complete lines and the
// caller continues via NextOffset. Kept larger than readMaxLineRunes so a
// page always returns at least one full line and NextOffset advances.
const readMaxOutputRunes = 65536

// lineBoundingConfig controls how rendered lines are truncated.
type lineBoundingConfig struct {
	maxLineRunes   int // per-line rune cap; 0 means use default (400)
	maxOutputRunes int // total output rune cap; 0 means no cap
}

// boundLines truncates individual lines by rune count and optionally caps
// total output.
func boundLines(lines []string, cfg lineBoundingConfig) []string {
	if cfg.maxLineRunes <= 0 {
		cfg.maxLineRunes = defaultMaxLineRunes
	}

	result := make([]string, 0, len(lines))

	for _, line := range lines {
		runes := []rune(line)
		if len(runes) > cfg.maxLineRunes {
			line = string(runes[:cfg.maxLineRunes]) + "…<truncated>"
		}
		result = append(result, line)
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
				break
			}
			total += sep + lineLen
		}
	}

	return result
}

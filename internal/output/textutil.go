package output

import (
	"strings"
	"unicode/utf8"
)

// TruncateWithEllipsis trims whitespace, normalizes internal spaces,
// and truncates text to maxLen characters, appending "..." if truncated.
// If maxLen is <= 3, returns the first maxLen characters without ellipsis.
func TruncateWithEllipsis(s string, maxLen int) string {
	s = strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
	runeCount := utf8.RuneCountInString(s)
	if maxLen <= 0 {
		return ""
	}
	if runeCount <= maxLen {
		return s
	}
	runes := []rune(s)
	if maxLen <= 3 {
		return string(runes[:maxLen])
	}
	return string(runes[:maxLen-3]) + "..."
}

// PluralSuffix returns singular if count == 1, otherwise returns plural.
func PluralSuffix(count int, singular, plural string) string {
	if count == 1 {
		return singular
	}
	return plural
}

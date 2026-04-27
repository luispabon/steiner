package output

import "strings"

// TruncateWithEllipsis trims whitespace, normalizes internal spaces,
// and truncates text to maxLen characters, appending "..." if truncated.
// If maxLen is <= 3, returns the first maxLen characters without ellipsis.
func TruncateWithEllipsis(s string, maxLen int) string {
	s = strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
	if maxLen <= 0 || len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}

// PluralSuffix returns singular if count == 1, otherwise returns plural.
func PluralSuffix(count int, singular, plural string) string {
	if count == 1 {
		return singular
	}
	return plural
}

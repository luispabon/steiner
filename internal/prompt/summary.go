package prompt

import (
	"strings"
	"unicode/utf8"
)

func truncateText(content string, limit int) string {
	if limit <= 0 || len(content) <= limit {
		return content
	}
	if limit >= len(content) {
		return content
	}

	end := limit
	for end > 0 && !utf8.RuneStart(content[end]) {
		end--
	}
	if end <= 0 {
		return ""
	}
	return strings.TrimRight(content[:end], "\n\r\t ")
}

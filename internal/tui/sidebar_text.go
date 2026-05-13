package tui

import "strings"

func wrapWords(text string, width, maxLines int) []string {
	if width <= 0 || maxLines <= 0 || text == "" {
		return nil
	}
	words := strings.Fields(text)
	var lines []string
	current := ""
	for _, word := range words {
		if len(lines) >= maxLines {
			break
		}
		switch {
		case current == "":
			current = word
		case len(current)+1+len(word) <= width:
			current += " " + word
		default:
			lines = append(lines, current)
			current = word
		}
	}
	if current != "" && len(lines) < maxLines {
		lines = append(lines, current)
	}
	if n := len(lines); n > 0 {
		last := []rune(lines[n-1])
		if len(last) > width {
			if width > 1 {
				lines[n-1] = string(last[:width-1]) + "…"
			} else {
				lines[n-1] = string(last[:width])
			}
		}
	}
	return lines
}

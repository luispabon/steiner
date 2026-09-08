package lsp

import (
	"fmt"
	"strings"
)

// resolveSymbolPosition finds a symbol within a file and returns its absolute path and 1-based position.
// - If line >= 1, searches only that line; line beyond the file's line count is an error.
// - If line < 1, scans the entire file; multiple matches are an error (unless all on one line, then leftmost wins).
// - Identifier-boundary matching ensures "Foo" won't match inside "FooBar" but will match after ".".
// - Returns the resolved absolute file path, the 1-based line and column, or an error if resolution fails.
func resolveSymbolPosition(workspace, file, symbol string, line int) (resolvedFile string, resolvedLine, resolvedCol int, err error) {
	if symbol == "" {
		return "", 0, 0, fmt.Errorf("symbol is required")
	}

	absPath, err := absWorkspacePath(workspace, file)
	if err != nil {
		return "", 0, 0, err
	}

	content, err := readFileContent(absPath)
	if err != nil {
		return "", 0, 0, fmt.Errorf("read file: %w", err)
	}

	// Split on \n, trimming trailing \r per line to handle CRLF line endings.
	lines := strings.Split(string(content), "\n")
	for i := range lines {
		lines[i] = strings.TrimSuffix(lines[i], "\r")
	}

	if line >= 1 {
		// Search only the specified line.
		if line > len(lines) {
			return "", 0, 0, fmt.Errorf("line %d is out of range (file has %d lines)", line, len(lines))
		}

		lineIdx := line - 1 // Convert to 0-based
		col := findLeftmostMatch(lines[lineIdx], symbol)
		if col < 0 {
			return "", 0, 0, fmt.Errorf("symbol %q not found on line %d; line reads: %s", symbol, line, lines[lineIdx])
		}
		return absPath, line, col + 1, nil // +1 for 1-based column
	}

	// Scan the entire file for matches.
	var matchLines []int
	for lineIdx, lineText := range lines {
		col := findLeftmostMatch(lineText, symbol)
		if col >= 0 {
			matchLines = append(matchLines, lineIdx)
		}
	}

	if len(matchLines) == 0 {
		return "", 0, 0, fmt.Errorf("symbol %q not found in %s", symbol, makeRelative(workspace, absPath))
	}

	if len(matchLines) == 1 {
		lineIdx := matchLines[0]
		col := findLeftmostMatch(lines[lineIdx], symbol)
		return absPath, lineIdx + 1, col + 1, nil // +1 for 1-based
	}

	// Multiple matches on different lines: error with line list.
	const maxListedLines = 20
	lineNums := make([]string, 0, len(matchLines))
	for i, lineIdx := range matchLines {
		if i >= maxListedLines {
			remaining := len(matchLines) - maxListedLines
			lineNums = append(lineNums, fmt.Sprintf("... and %d more", remaining))
			break
		}
		lineNums = append(lineNums, fmt.Sprintf("%d", lineIdx+1))
	}
	return "", 0, 0, fmt.Errorf("symbol %q found on lines %s — narrow with the line parameter", symbol, strings.Join(lineNums, ", "))
}

// findLeftmostMatch searches line for symbol at identifier boundaries, returning
// the 0-based column of the leftmost match, or -1 if not found or no match is at a boundary.
// A match at column i is valid if the character before index i (if any) and the character
// after the match (if any) are not identifier characters.
func findLeftmostMatch(line, symbol string) int {
	if symbol == "" {
		return -1
	}

	runes := []rune(line)
	symbolRunes := []rune(symbol)
	symbolLen := len(symbolRunes)

	for i := 0; i <= len(runes)-symbolLen; i++ {
		// Check if the substring starting at i matches the symbol.
		match := true
		for j := 0; j < symbolLen; j++ {
			if runes[i+j] != symbolRunes[j] {
				match = false
				break
			}
		}

		if !match {
			continue
		}

		// Check boundary before.
		if i > 0 && isIdentifierChar(runes[i-1]) {
			continue
		}

		// Check boundary after.
		afterIdx := i + symbolLen
		if afterIdx < len(runes) && isIdentifierChar(runes[afterIdx]) {
			continue
		}

		return i
	}

	return -1
}

// isIdentifierChar reports whether r is a valid identifier character: [A-Za-z0-9_].
func isIdentifierChar(r rune) bool {
	return (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_'
}

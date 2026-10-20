package lsp

import "fmt"

// parseLineParameter extracts the line parameter from input, returning (value, was_present).
// Presence is determined by the key existing in the map; returns (0, false) if not present.
func parseLineParameter(input map[string]any) (int, bool) {
	v, ok := input["line"]
	if !ok {
		return 0, false
	}
	f, ok := v.(float64)
	if !ok {
		return 0, false
	}
	return int(f), true
}

// parseColumnParameter extracts the column parameter from input, returning (value, was_present).
func parseColumnParameter(input map[string]any) (int, bool) {
	v, ok := input["column"]
	if !ok {
		return 0, false
	}
	f, ok := v.(float64)
	if !ok {
		return 0, false
	}
	return int(f), true
}

// parseSymbolParameter extracts the symbol parameter from input, returning (value, was_present).
func parseSymbolParameter(input map[string]any) (string, bool) {
	v, ok := input["symbol"]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	if !ok {
		return "", false
	}
	return s, true
}

// positionInput is the optional position a tool call addresses: a column (which
// requires a line) or a symbol (optionally narrowed by line). Each ok flag
// records whether its parameter was present and of the right type.
type positionInput struct {
	line     int
	lineOk   bool
	col      int
	colOk    bool
	symbol   string
	symbolOk bool
}

// parsePositionInput reads the optional position parameters from input.
func parsePositionInput(input map[string]any) positionInput {
	var pos positionInput
	pos.line, pos.lineOk = parseLineParameter(input)
	pos.col, pos.colOk = parseColumnParameter(input)
	pos.symbol, pos.symbolOk = parseSymbolParameter(input)
	return pos
}

// resolvePosition resolves a position from either column or symbol.
// - If column is provided, requires line; returns (line, column, "", "") if valid.
// - If symbol is provided, calls resolveSymbolPosition; returns the resolved position and echo line.
// - If neither, returns ("", "", "", error message).
// - If line is provided but < 1 on symbol path, returns error message.
// The returned echo line (3rd return) is empty string if no resolution via symbol, or "resolved FILE:LINE:COL (SYMBOL)" if symbol was used.
func resolvePosition(m *Manager, toolName string, pos positionInput, file string) (resolvedLine, resolvedCol int, echoLine string, errMsg string) {
	if pos.colOk {
		// Column path: requires line.
		if !pos.lineOk {
			return 0, 0, "", fmt.Sprintf("%s: column requires line", toolName)
		}
		return pos.line, pos.col, "", ""
	}

	if pos.symbolOk {
		// Symbol path.
		if pos.lineOk && pos.line < 1 {
			return 0, 0, "", fmt.Sprintf("%s: invalid line %d", toolName, pos.line)
		}

		resolvedFile, resolvedLine, resolvedCol, err := resolveSymbolPosition(m.workspace, file, pos.symbol, pos.line)
		if err != nil {
			return 0, 0, "", err.Error()
		}

		// Build echo line with relative path.
		relPath := makeRelative(m.workspace, resolvedFile)
		echo := fmt.Sprintf("resolved %s:%d:%d (%s)", relPath, resolvedLine, resolvedCol, pos.symbol)
		return resolvedLine, resolvedCol, echo, ""
	}

	// Neither column nor symbol provided.
	return 0, 0, "", fmt.Sprintf("%s: requires either column (with line) or symbol", toolName)
}

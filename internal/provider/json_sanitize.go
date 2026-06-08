package provider

// sanitizeToolCallJSON removes trailing commas from JSON arrays and objects.
// Handles whitespace between trailing comma and closing bracket.
// Does not corrupt valid JSON or mangle commas inside string literals.
func sanitizeToolCallJSON(raw string) string {
	if raw == "" {
		return raw
	}

	buf := make([]byte, 0, len(raw))
	inString := false
	escaped := false

	for i := 0; i < len(raw); i++ {
		ch := raw[i]

		if inString {
			escaped, inString = advanceStringState(ch, escaped)
			buf = append(buf, ch)
			continue
		}

		if ch == '"' {
			inString = true
			buf = append(buf, ch)
			continue
		}

		if ch == ',' {
			if j := trailingCommaEnd(raw, i+1); j >= 0 {
				i = j - 1
				continue
			}
		}

		buf = append(buf, ch)
	}

	return string(buf)
}

// advanceStringState returns updated escaped and inString flags for a character
// read while already inside a JSON string literal.
func advanceStringState(ch byte, escaped bool) (newEscaped, stillInString bool) {
	if escaped {
		return false, true
	}
	if ch == '\\' {
		return true, true
	}
	if ch == '"' {
		return false, false
	}
	return false, true
}

// trailingCommaEnd scans raw[start:] over whitespace and returns the index of
// the first non-whitespace character if it is a closing bracket (] or }),
// indicating a trailing comma. Returns -1 if no trailing comma is found.
func trailingCommaEnd(raw string, start int) int {
	j := start
	for j < len(raw) && isJSONWhitespace(raw[j]) {
		j++
	}
	if j < len(raw) && (raw[j] == ']' || raw[j] == '}') {
		return j
	}
	return -1
}

func isJSONWhitespace(ch byte) bool {
	return ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r'
}

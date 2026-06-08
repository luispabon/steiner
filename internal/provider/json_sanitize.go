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

		// Track escape sequences inside strings.
		if inString {
			if escaped {
				escaped = false
				buf = append(buf, ch)
				continue
			}
			if ch == '\\' {
				escaped = true
				buf = append(buf, ch)
				continue
			}
			if ch == '"' {
				inString = false
				buf = append(buf, ch)
				continue
			}
			buf = append(buf, ch)
			continue
		}

		// Track string entry/exit (outside strings only).
		if ch == '"' {
			inString = true
			buf = append(buf, ch)
			continue
		}

		// Look for trailing comma: comma followed by whitespace and ] or }.
		if ch == ',' {
			// Scan ahead for closing bracket, skipping whitespace.
			j := i + 1
			for j < len(raw) && (raw[j] == ' ' || raw[j] == '\t' || raw[j] == '\n' || raw[j] == '\r') {
				j++
			}
			if j < len(raw) && (raw[j] == ']' || raw[j] == '}') {
				// Skip the comma and any whitespace.
				i = j - 1
				continue
			}
		}

		buf = append(buf, ch)
	}

	return string(buf)
}

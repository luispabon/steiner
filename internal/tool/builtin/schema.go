package builtin

// ReadSchema returns the JSON schema for the read tool.
func ReadSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path":   map[string]any{"type": "string", "description": "File path to read"},
			"offset": map[string]any{"type": "integer", "description": "Starting line number (1-based)", "default": 1},
			"limit":  map[string]any{"type": "integer", "description": "Max lines to read", "default": defaultReadLimit, "maximum": maxReadLimit},
		},
		"required":             []string{"path"},
		"additionalProperties": false,
	}
}

// WriteSchema returns the JSON schema for the write tool.
func WriteSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path":    map[string]any{"type": "string", "description": "File path to write"},
			"content": map[string]any{"type": "string", "description": "File content"},
		},
		"required":             []string{"path", "content"},
		"additionalProperties": false,
	}
}

// EditSchema returns the JSON schema for the edit tool.
func EditSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path":       map[string]any{"type": "string", "description": "File path to edit"},
			"oldString":  map[string]any{"type": "string", "description": "Text to replace"},
			"newString":  map[string]any{"type": "string", "description": "Replacement text"},
			"replaceAll": map[string]any{"type": "boolean", "description": "Replace all occurrences", "default": false},
		},
		"required":             []string{"path", "oldString", "newString"},
		"additionalProperties": false,
	}
}

// GlobSchema returns the JSON schema for the glob tool.
func GlobSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pattern": map[string]any{"type": "string", "description": "Glob pattern to match"},
			"path":    map[string]any{"type": "string", "description": "Directory to search in"},
			"limit":   map[string]any{"type": "integer", "description": "Max results", "default": defaultGlobLimit, "maximum": maxGlobLimit},
			"offset":  map[string]any{"type": "integer", "description": "Result offset", "default": 0},
		},
		"required":             []string{"pattern"},
		"additionalProperties": false,
	}
}

// GrepSchema returns the JSON schema for the grep tool.
func GrepSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pattern":         map[string]any{"type": "string", "description": "Regex pattern to search for"},
			"path":            map[string]any{"type": "string", "description": "Directory or file to search in"},
			"glob":            map[string]any{"type": "string", "description": "File pattern filter"},
			"type":            map[string]any{"type": "string", "description": "Search type (content, files, etc)"},
			"outputMode":      map[string]any{"type": "string", "description": "Output format"},
			"caseInsensitive": map[string]any{"type": "boolean", "description": "Case insensitive search"},
			"lineNumbers":     map[string]any{"type": "boolean", "description": "Include line numbers"},
			"afterContext":    map[string]any{"type": "integer", "description": "Lines after match"},
			"beforeContext":   map[string]any{"type": "integer", "description": "Lines before match"},
			"context":         map[string]any{"type": "integer", "description": "Context lines around match"},
			"multiline":       map[string]any{"type": "boolean", "description": "Enable multiline mode"},
			"headLimit":       map[string]any{"type": "integer", "description": "Max matches to return", "default": defaultGrepHeadLimit, "maximum": maxGrepHeadLimit},
			"offset":          map[string]any{"type": "integer", "description": "Match offset", "default": 0},
		},
		"required":             []string{"pattern"},
		"additionalProperties": false,
	}
}

// LSSchema returns the JSON schema for the ls tool.
func LSSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path":      map[string]any{"type": "string", "description": "Directory to list"},
			"recursive": map[string]any{"type": "boolean", "description": "List recursively"},
			"limit":     map[string]any{"type": "integer", "description": "Max results", "default": defaultLSLimit, "maximum": maxLSLimit},
			"offset":    map[string]any{"type": "integer", "description": "Result offset", "default": 0},
		},
		"additionalProperties": false,
	}
}

// BashSchema returns the JSON schema for the bash tool.
func BashSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command":        map[string]any{"type": "string", "description": "Shell command to execute"},
			"cwd":            map[string]any{"type": "string", "description": "Working directory"},
			"timeoutSeconds": map[string]any{"type": "integer", "description": "Max execution time", "default": defaultBashTimeoutSeconds, "maximum": maxBashTimeoutSeconds},
			"maxOutputChars": map[string]any{"type": "integer", "description": "Max output characters", "default": defaultBashMaxOutputChars, "maximum": maxBashMaxOutputChars},
		},
		"required":             []string{"command"},
		"additionalProperties": false,
	}
}

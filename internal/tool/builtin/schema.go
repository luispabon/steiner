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
			"path":        map[string]any{"type": "string", "description": "File path to edit"},
			"old_string":  map[string]any{"type": "string", "description": "Text to replace"},
			"new_string":  map[string]any{"type": "string", "description": "Replacement text"},
			"replace_all": map[string]any{"type": "boolean", "description": "Replace all occurrences", "default": false},
		},
		"required":             []string{"path", "old_string", "new_string"},
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
			"limit":   map[string]any{"type": "integer", "description": "Max results (default: 200)", "default": defaultGlobLimit, "maximum": maxGlobLimit},
			"offset":  map[string]any{"type": "integer", "description": "Result offset (default: 0)", "default": 0},
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
			"pattern":          map[string]any{"type": "string", "description": "Regex pattern to search for"},
			"path":             map[string]any{"type": "string", "description": "Directory or file to search in"},
			"glob":             map[string]any{"type": "string", "description": "File pattern filter"},
			"type":             map[string]any{"type": "string", "description": "Search type (content, files, etc)"},
			"output_mode":      map[string]any{"type": "string", "description": "Output mode: content, files_with_matches, or count"},
			"case_insensitive": map[string]any{"type": "boolean", "description": "Case insensitive search"},
			"line_numbers":     map[string]any{"type": "boolean", "description": "Include line numbers in content output"},
			"after_context":    map[string]any{"type": "integer", "description": "Lines after each match in content output"},
			"before_context":   map[string]any{"type": "integer", "description": "Lines before each match in content output"},
			"context":          map[string]any{"type": "integer", "description": "Symmetric context lines for content output"},
			"multiline":        map[string]any{"type": "boolean", "description": "Enable multiline mode"},
			"head_limit":       map[string]any{"type": "integer", "description": "Max logical results to return per page", "default": defaultGrepHeadLimit, "maximum": maxGrepHeadLimit},
			"offset":           map[string]any{"type": "integer", "description": "Logical result offset", "default": 0},
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

// DisplayFileSchema returns the JSON schema for the display_file tool.
func DisplayFileSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path":   map[string]any{"type": "string", "description": "Absolute or workspace-relative path to the file to display"},
			"offset": map[string]any{"type": "integer", "description": "Starting line number (1-based)", "default": 1},
			"limit":  map[string]any{"type": "integer", "description": "Max lines to preview", "default": defaultDisplayFileLimit, "maximum": maxDisplayFileLimit},
		},
		"required":             []string{"path"},
		"additionalProperties": false,
	}
}

// BashSchema returns the JSON schema for the bash tool.
func BashSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command":          map[string]any{"type": "string", "description": "Shell command to execute"},
			"cwd":              map[string]any{"type": "string", "description": "Working directory"},
			"timeout_seconds":  map[string]any{"type": "integer", "description": "Max execution time", "default": defaultBashTimeoutSeconds, "maximum": maxBashTimeoutSeconds},
			"max_output_chars": map[string]any{"type": "integer", "description": "Max output characters", "default": defaultBashMaxOutputChars, "maximum": maxBashMaxOutputChars},
		},
		"required":             []string{"command"},
		"additionalProperties": false,
	}
}

// ApplyPatchSchema returns the JSON schema for the apply_patch tool.
func ApplyPatchSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{"type": "string", "description": "Absolute or workspace-relative path to the file to patch"},
			"hunks": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"old": map[string]any{"type": "string", "description": "Exact text to find (must match uniquely)"},
						"new": map[string]any{"type": "string", "description": "Replacement text"},
					},
					"required":             []string{"old", "new"},
					"additionalProperties": false,
				},
				"description": "List of hunks (old/new pairs) to apply. Hunks are sorted by position automatically.",
			},
			"dry_run":         map[string]any{"type": "boolean", "description": "If true, preview changes without writing", "default": false},
			"fuzzy_threshold": map[string]any{"type": "number", "description": "Future: fuzzy match threshold for slightly mismatched old text", "default": 1.0},
		},
		"required":             []string{"path", "hunks"},
		"additionalProperties": false,
	}
}

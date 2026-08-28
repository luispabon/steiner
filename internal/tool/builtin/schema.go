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

// MutateSchema returns the JSON schema for the mutate tool.
func MutateSchema() map[string]any {
	operationSchema := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"type": map[string]any{"type": "string", "enum": []string{"create", "write", "replace", "delete_file", "move"}, "description": "Operation type. Required fields per type ([optional] in brackets): " +
				"create: path, content. " +
				"write: path, content. " +
				"replace: path, old_string, new_string [replace_all]. " +
				"delete_file: path. " +
				"move: from, to. " +
				"Most types also accept [assert_present, assert_absent, file_hash]; exceptions: create accepts asserts but not file_hash; delete_file accepts file_hash but not asserts."},
			"path":       map[string]any{"type": "string", "description": "Target path for create, write, replace, and delete_file"},
			"content":    map[string]any{"type": "string", "description": "File content for create and write"},
			"old_string": map[string]any{"type": "string", "description": "Exact text to replace. Whitespace must match the file exactly, including leading indentation depth (most common mismatch: tab vs space nesting level). Copy directly from read output rather than reconstructing from memory."},
			"new_string": map[string]any{"type": "string", "description": "Replacement text"},
			"assert_present": map[string]any{
				"type":        "array",
				"description": "Strings that must appear in the post-operation file content. Use assertions to confirm edits landed where you intended (especially for replace operations whose old_string might be ambiguous); assertion failures abort the full batch before commit.",
				"items":       map[string]any{"type": "string"},
			},
			"assert_absent": map[string]any{
				"type":        "array",
				"description": "Strings that must be absent from the post-operation file content. Use assertions to confirm edits landed where you intended; assertion failures abort the full batch before commit.",
				"items":       map[string]any{"type": "string"},
			},
			"replace_all": map[string]any{"type": "boolean", "description": "Replace all occurrences for replace", "default": false},
			"file_hash":   map[string]any{"type": "string", "description": "8-char hex hash from read/grep result. When provided, it is validated against the initial disk snapshot captured when the batch starts, not after earlier in-memory operations. It only applies to existing files; missing targets fail explicitly instead of being silently accepted."},
			"from":        map[string]any{"type": "string", "description": "Source path for move"},
			"to":          map[string]any{"type": "string", "description": "Destination path for move. The destination must not already exist; move never overwrites."},
		},
		"required": []string{"type"},
	}
	return map[string]any{
		"type":                 "object",
		"required":             []string{"operations"},
		"additionalProperties": false,
		"properties": map[string]any{
			"operations": map[string]any{
				"type":        "array",
				"description": "Ordered list of file mutations. Operations are evaluated sequentially against an in-memory snapshot, so later operations see earlier edits in the same batch, but no filesystem writes are committed until the full batch has been planned. On partial failure, operations_skipped reports how many were never attempted.",
				"minItems":    1,
				"items":       operationSchema,
			},
		},
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

// FetchURLSchema returns the JSON schema for the fetch_url tool.
func FetchURLSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"url":      map[string]any{"type": "string", "description": "URL to fetch"},
			"max_size": map[string]any{"type": "integer", "description": "Max content length in runes", "default": defaultFetchURLMaxSize, "maximum": maxFetchURLMaxSize},
		},
		"required":             []string{"url"},
		"additionalProperties": false,
	}
}

// WebSearchSchema returns the JSON schema for the web_search tool.
func WebSearchSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{"type": "string", "description": "Search query"},
			"limit": map[string]any{"type": "integer", "description": "Max results (default: 10)", "default": defaultWebSearchLimit, "maximum": maxWebSearchLimit},
		},
		"required":             []string{"query"},
		"additionalProperties": false,
	}
}

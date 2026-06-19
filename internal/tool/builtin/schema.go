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
			"type":       map[string]any{"type": "string", "enum": []string{"create", "write", "replace", "line_replace", "delete_line", "delete", "move", "insert_before", "insert_after"}, "description": "Operation type"},
			"path":       map[string]any{"type": "string", "description": "Target path for create, write, replace, line_replace, delete_line, and delete"},
			"content":    map[string]any{"type": "string", "description": "File content for create, write, insert_before, and insert_after"},
			"old_string": map[string]any{"type": "string", "description": "Exact text to replace. Whitespace (including tabs vs spaces) must match the file exactly. Required for line_replace on existing files (prevents silent corruption from shifted line numbers). With line_count, acts as a validation guard: must appear exactly once in the target line range."},
			"new_string": map[string]any{"type": "string", "description": "Replacement text"},
			"assert_present": map[string]any{
				"type":        "array",
				"description": "Strings that must appear in the post-operation file content. Assertion failures abort the full batch before commit.",
				"items":       map[string]any{"type": "string"},
			},
			"assert_absent": map[string]any{
				"type":        "array",
				"description": "Strings that must be absent from the post-operation file content. Assertion failures abort the full batch before commit.",
				"items":       map[string]any{"type": "string"},
			},
			"replace_all": map[string]any{"type": "boolean", "description": "Replace all occurrences for replace", "default": false},
			"allow_empty": map[string]any{"type": "boolean", "description": "Allow writing empty content to an existing file with content. Required when content is empty and the target file is non-empty, to prevent accidental data loss.", "default": false},
			"line":        map[string]any{"type": "integer", "description": "1-based line number for line_replace, delete_line, insert_before, and insert_after. insert_after supports appending after the final line, even when the file has no trailing newline.", "minimum": 1},
			"line_count":  map[string]any{"type": "integer", "description": "Number of lines to replace or delete starting from line. Omit for single-line edits.", "minimum": 1, "default": 1},
			"file_hash":   map[string]any{"type": "string", "description": "4-char hex hash from read/grep result. When provided, it is validated against the initial disk snapshot captured when the batch starts, not after earlier in-memory operations. It only applies to existing files; missing targets fail explicitly instead of being silently accepted."},
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
			"dry_run": map[string]any{
				"type":        "boolean",
				"description": "Validate and preview mutations without writing files.",
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
			"max_size": map[string]any{"type": "integer", "description": "Max content length in runes", "default": 500000, "maximum": 1000000},
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
			"limit": map[string]any{"type": "integer", "description": "Max results (default: 10)", "default": 10, "maximum": 30},
		},
		"required":             []string{"query"},
		"additionalProperties": false,
	}
}

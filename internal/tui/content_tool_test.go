package tui

import "testing"

func TestSummarizeGlobArgs(t *testing.T) {
	tests := []struct {
		name     string
		args     map[string]any
		expected string
	}{
		{
			name:     "pattern and path together",
			args:     map[string]any{"pattern": "**/*.go", "path": "internal"},
			expected: "./internal/**/*.go",
		},
		{
			name:     "pattern and path with ./ prefix",
			args:     map[string]any{"pattern": "*.go", "path": "./src"},
			expected: "./src/*.go",
		},
		{
			name:     "pattern only",
			args:     map[string]any{"pattern": "**/*.go"},
			expected: "**/*.go",
		},
		{
			name:     "path only with ./ prefix",
			args:     map[string]any{"path": "./internal"},
			expected: "./internal",
		},
		{
			name:     "path only without ./ prefix",
			args:     map[string]any{"path": "internal"},
			expected: "./internal",
		},
		{
			name:     "empty args map",
			args:     map[string]any{},
			expected: "glob",
		},
		{
			name:     "path with trailing slash",
			args:     map[string]any{"pattern": "**/*.go", "path": "internal/"},
			expected: "./internal/**/*.go",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := summarizeGlobArgs(tt.args)
			if result != tt.expected {
				t.Errorf("summarizeGlobArgs(%v) = %q, want %q", tt.args, result, tt.expected)
			}
		})
	}
}

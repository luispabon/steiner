package provider

import "testing"

func TestSanitizeToolCallJSON(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "valid JSON no trailing comma",
			input:    `[1,2,3]`,
			expected: `[1,2,3]`,
		},
		{
			name:     "valid object no trailing comma",
			input:    `{"a":1,"b":2}`,
			expected: `{"a":1,"b":2}`,
		},
		{
			name:     "trailing comma in array",
			input:    `[1,2,]`,
			expected: `[1,2]`,
		},
		{
			name:     "trailing comma in object",
			input:    `{"a":1,}`,
			expected: `{"a":1}`,
		},
		{
			name:     "trailing comma with space",
			input:    `[1, 2, ]`,
			expected: `[1, 2]`,
		},
		{
			name:     "trailing comma with newline",
			input:    "[1, 2,\n]",
			expected: "[1, 2]",
		},
		{
			name:     "trailing comma with tabs",
			input:    "[1, 2,\t\t]",
			expected: "[1, 2]",
		},
		{
			name:     "nested trailing commas",
			input:    `[{"a":1,},]`,
			expected: `[{"a":1}]`,
		},
		{
			name:     "comma inside string preserved",
			input:    `{"a":"hello,}"}`,
			expected: `{"a":"hello,}"}`,
		},
		{
			name:     "escaped quote in string",
			input:    `{"a":"say \"hi,}\""}`,
			expected: `{"a":"say \"hi,}\""}`,
		},
		{
			name:     "empty array",
			input:    `[]`,
			expected: `[]`,
		},
		{
			name:     "empty object",
			input:    `{}`,
			expected: `{}`,
		},
		{
			name:     "multiple trailing commas",
			input:    `[1,,]`,
			expected: `[1,]`,
		},
		{
			name:     "complex nested structure",
			input:    `[{"type":"write","path":"foo.md"},]`,
			expected: `[{"type":"write","path":"foo.md"}]`,
		},
		{
			name:     "array of objects with trailing commas",
			input:    `[{"a":1,},{"b":2,},]`,
			expected: `[{"a":1},{"b":2}]`,
		},
		{
			name:     "string with escaped backslash before quote",
			input:    `{"a":"path\\\"test,}"}`,
			expected: `{"a":"path\\\"test,}"}`,
		},
		{
			name:     "comma not trailing (followed by content)",
			input:    `[1,2,3]`,
			expected: `[1,2,3]`,
		},
		{
			name:     "multiple trailing commas with spaces",
			input:    `[1 , , ]`,
			expected: `[1 , ]`,
		},
		{
			name:     "object property with comma in string value",
			input:    `{"url":"http://example.com?a=1,b=2",}`,
			expected: `{"url":"http://example.com?a=1,b=2"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeToolCallJSON(tt.input)
			if got != tt.expected {
				t.Errorf("sanitizeToolCallJSON(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

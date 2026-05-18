package output

import (
	"testing"
)

func TestBuildFetchURLPreview(t *testing.T) {
	tests := []struct {
		name   string
		result string
		want   ToolPreview
	}{
		{
			name:   "successful fetch",
			result: `{"url":"https://example.com","content":"# Hello\nWorld\n"}`,
			want: ToolPreview{
				Kind:     ToolPreviewKindFetchURL,
				Path:     "https://example.com",
				Language: "markdown",
				Contents: "# Hello\nWorld\n",
			},
		},
		{
			name:   "error payload",
			result: `{"url":"https://example.com","content":"","error":"404 not found"}`,
			want:   plainToolPreview(),
		},
		{
			name:   "empty content",
			result: `{"url":"https://example.com","content":""}`,
			want:   plainToolPreview(),
		},
		{
			name:   "malformed JSON",
			result: `{invalid`,
			want:   plainToolPreview(),
		},
		{
			name:   "non-JSON string",
			result: "not json",
			want:   plainToolPreview(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildFetchURLPreview(tt.result)
			if got.Kind != tt.want.Kind || got.Path != tt.want.Path || got.Language != tt.want.Language || got.Contents != tt.want.Contents {
				t.Errorf("buildFetchURLPreview(%q) = %+v, want %+v", tt.result, got, tt.want)
			}
		})
	}
}

func TestBuildWebSearchPreview(t *testing.T) {
	tests := []struct {
		name   string
		result string
		want   ToolPreview
	}{
		{
			name:   "successful search results",
			result: `[{"title":"Foo","url":"https://foo.com"},{"title":"Bar","url":"https://bar.com"}]`,
			want: ToolPreview{
				Kind:     ToolPreviewKindWebSearch,
				Path:     "search results",
				Language: "json",
				Contents: `[
  {
    "title": "Foo",
    "url": "https://foo.com"
  },
  {
    "title": "Bar",
    "url": "https://bar.com"
  }
]`,
				Returned: 2,
			},
		},
		{
			name:   "error payload",
			result: `{"error":"rate limit exceeded"}`,
			want:   plainToolPreview(),
		},
		{
			name:   "empty array",
			result: `[]`,
			want: ToolPreview{
				Kind:     ToolPreviewKindWebSearch,
				Path:     "search results",
				Language: "json",
				Contents: `[]`,
				Returned: 0,
			},
		},
		{
			name:   "malformed JSON",
			result: `{invalid`,
			want:   plainToolPreview(),
		},
		{
			name:   "non-JSON string",
			result: "not json",
			want:   plainToolPreview(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildWebSearchPreview(tt.result)
			if got.Kind != tt.want.Kind || got.Path != tt.want.Path || got.Language != tt.want.Language || got.Contents != tt.want.Contents || got.Returned != tt.want.Returned {
				t.Errorf("buildWebSearchPreview(%q) = %+v, want %+v", tt.result, got, tt.want)
			}
		})
	}
}

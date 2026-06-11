package output

import "testing"

func TestMediaTypeShort(t *testing.T) {
	tests := []struct {
		mediaType string
		expected  string
	}{
		{"image/png", "png"},
		{"image/jpeg", "jpg"},
		{"image/gif", "gif"},
		{"image/webp", "webp"},
		{"image/svg+xml", "image/svg+xml"},
		{"", ""},
	}
	for _, tt := range tests {
		got := MediaTypeShort(tt.mediaType)
		if got != tt.expected {
			t.Errorf("MediaTypeShort(%q): want %q, got %q", tt.mediaType, tt.expected, got)
		}
	}
}

func TestFormatFileSize(t *testing.T) {
	tests := []struct {
		bytes    int
		expected string
	}{
		{0, ""},
		{512, "512B"},
		{1024, "1KB"},
		{2048, "2KB"},
		{234 * 1024, "234KB"},
		{1048576, "1.0MB"},
		{1258291, "1.2MB"},
		{1536 * 1024, "1.5MB"},
		{10956800, "10.4MB"},
	}
	for _, tt := range tests {
		got := FormatFileSize(tt.bytes)
		if got != tt.expected {
			t.Errorf("FormatFileSize(%d): want %q, got %q", tt.bytes, tt.expected, got)
		}
	}
}

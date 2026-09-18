package tui

import "testing"

func TestStripProviderURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		url  string
		want string
	}{
		{
			name: "https URL with /v1 suffix",
			url:  "https://api.example.com/v1",
			want: "api.example.com",
		},
		{
			name: "http URL with /v1 suffix",
			url:  "http://localhost:8000/v1",
			want: "localhost:8000",
		},
		{
			name: "https URL with /v1/ (trailing slash)",
			url:  "https://host/v1/",
			want: "host",
		},
		{
			name: "http URL with /v1/ (trailing slash)",
			url:  "http://localhost:11434/v1/",
			want: "localhost:11434",
		},
		{
			name: "URL with only trailing slash",
			url:  "https://example.com/",
			want: "example.com",
		},
		{
			name: "URL with no suffix",
			url:  "https://example.com",
			want: "example.com",
		},
		{
			name: "URL with whitespace",
			url:  "  https://example.com  ",
			want: "example.com",
		},
		{
			name: "localhost without port",
			url:  "http://localhost",
			want: "localhost",
		},
		{
			name: "ipv4 with port and /v1",
			url:  "http://127.0.0.1:11434/v1",
			want: "127.0.0.1:11434",
		},
		{
			name: "ipv4 with port and /v1/",
			url:  "http://127.0.0.1:11434/v1/",
			want: "127.0.0.1:11434",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := stripProviderURL(tc.url)
			if got != tc.want {
				t.Errorf("stripProviderURL(%q) = %q, want %q", tc.url, got, tc.want)
			}
		})
	}
}

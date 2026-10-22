package quota

import "testing"

func TestAPIKeyAccountKey(t *testing.T) {
	tests := []struct {
		alias string
		want  string
	}{
		{"main", "provider:main"},
		{"litellm-b", "provider:litellm-b"},
	}
	for _, tt := range tests {
		if got := APIKeyAccountKey(tt.alias); got != tt.want {
			t.Errorf("APIKeyAccountKey(%q) = %q, want %q", tt.alias, got, tt.want)
		}
	}
}

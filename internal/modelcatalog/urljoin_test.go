package modelcatalog

import "testing"

func TestJoinModelsURL(t *testing.T) {
	tests := []struct {
		name, provider, base, want string
	}{
		{"openai bare", "openai", "https://example.test", "https://example.test/v1/models"},
		{"openai suffixed", "openai", "https://example.test/v1/", "https://example.test/v1/models"},
		{"compat bare", "openai_compat", "https://example.test/proxy", "https://example.test/proxy/v1/models"},
		{"litellm suffixed", "litellm", "https://example.test/proxy/v1", "https://example.test/proxy/v1/models"},
		{"openrouter bare", "openrouter", "https://example.test", "https://example.test/api/v1/models"},
		{"openrouter suffixed", "openrouter", "https://example.test/api/v1/", "https://example.test/api/v1/models"},
		{"anthropic bare", "anthropic", "https://example.test", "https://example.test/v1/models"},
		{"anthropic suffixed", "anthropic", "https://example.test/v1/", "https://example.test/v1/models"},
		{"ollama bare", "ollama", "http://localhost:11434/", "http://localhost:11434/api/tags"},
		{"ollama steiner default", "ollama", "http://localhost:11434/v1", "http://localhost:11434/api/tags"},
		{"ollama suffixed", "ollama", "http://example.test/proxy/v1/", "http://example.test/proxy/api/tags"},
		{"lmstudio bare", "lmstudio", "http://example.test", "http://example.test/api/v1/models"},
		{"lmstudio suffixed", "lmstudio", "http://example.test/v1/", "http://example.test/api/v1/models"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := joinModelsURL(test.provider, test.base)
			if err != nil {
				t.Fatalf("joinModelsURL: %v", err)
			}
			if got != test.want {
				t.Fatalf("URL: got %q, want %q", got, test.want)
			}
		})
	}
}

func TestJoinModelsURLErrors(t *testing.T) {
	for _, base := range []string{"", "localhost:1234", "://bad"} {
		if _, err := joinModelsURL("openai", base); err == nil {
			t.Errorf("joinModelsURL(%q): expected error", base)
		}
	}
}

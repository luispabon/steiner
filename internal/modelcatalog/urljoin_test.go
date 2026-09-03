package modelcatalog

import "testing"

func TestJoinModelsURL(t *testing.T) {
	tests := []struct {
		name, provider, base, want string
	}{
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
		if _, err := joinModelsURL("anthropic", base); err == nil {
			t.Errorf("joinModelsURL(%q): expected error", base)
		}
	}
	if _, err := joinModelsURL("bogus", "https://example.test"); err == nil {
		t.Error("joinModelsURL(bogus): expected error for unsupported provider type")
	}
}

func TestJoinOpenAIStyleModelsURL(t *testing.T) {
	// Every provider type routed to OpenAIEnumerator (openai, openai_compat,
	// litellm, opencode_go, opencode_zen, ...) shares this URL shape and
	// calls this helper directly rather than going through joinModelsURL's
	// provider-type switch — see enumerate_openai.go.
	tests := []struct {
		name, base, want string
	}{
		{"bare", "https://example.test", "https://example.test/v1/models"},
		{"v1 suffixed", "https://example.test/v1/", "https://example.test/v1/models"},
		{"proxy path bare", "https://example.test/proxy", "https://example.test/proxy/v1/models"},
		{"proxy path v1 suffixed", "https://example.test/proxy/v1", "https://example.test/proxy/v1/models"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := joinOpenAIStyleModelsURL(test.base)
			if err != nil {
				t.Fatalf("joinOpenAIStyleModelsURL: %v", err)
			}
			if got != test.want {
				t.Fatalf("URL: got %q, want %q", got, test.want)
			}
		})
	}
}

func TestJoinOpenAIStyleModelsURLErrors(t *testing.T) {
	for _, base := range []string{"", "localhost:1234", "://bad"} {
		if _, err := joinOpenAIStyleModelsURL(base); err == nil {
			t.Errorf("joinOpenAIStyleModelsURL(%q): expected error", base)
		}
	}
}

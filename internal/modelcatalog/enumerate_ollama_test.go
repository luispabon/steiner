package modelcatalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestOllamaEnumerator(t *testing.T) {
	fixture, err := os.ReadFile("testdata/ollama_models.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			t.Errorf("path: got %q, want /api/tags", r.URL.Path)
		}
		if got := r.Header.Get("X-Test"); got != "custom" {
			t.Errorf("custom header: got %q", got)
		}
		if got := r.Header.Get("Authorization"); got != "custom-auth" {
			t.Errorf("authorization: got %q", got)
		}
		_, _ = w.Write(fixture)
	}))
	defer server.Close()

	result, err := NewOllamaEnumerator(server.Client()).Enumerate(context.Background(), Endpoint{
		Alias: "local", Type: "ollama", BaseURL: server.URL + "/v1", APIKey: "ignored",
		Headers: map[string]string{"X-Test": "custom", "Authorization": "custom-auth"},
	}, EnumerationOptions{})
	if err != nil {
		t.Fatalf("Enumerate: %v", err)
	}
	if len(result.Models) != 3 {
		t.Fatalf("models: got %+v", result.Models)
	}
	if result.Models[0].ID != "llama3.2:latest" || result.Models[2].ID != "fallback-model" {
		t.Fatalf("models: got %+v", result.Models)
	}
}

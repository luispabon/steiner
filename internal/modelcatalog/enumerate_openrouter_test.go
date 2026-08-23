package modelcatalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
)

func TestOpenRouterEnumeratorPaginationAndFiltering(t *testing.T) {
	page1 := readModelFixture(t, "openrouter_models_page1.json")
	page2 := readModelFixture(t, "openrouter_models_page2.json")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/models" {
			t.Errorf("path: got %q", r.URL.Path)
		}
		if r.URL.Query().Get("output_modalities") != "" {
			t.Errorf("unexpected output_modalities query")
		}
		switch r.URL.Query().Get("page") {
		case "":
			_, _ = w.Write(page1)
		case "2":
			_, _ = w.Write(page2)
		default:
			t.Errorf("unexpected query: %q", r.URL.RawQuery)
		}
	}))
	defer server.Close()

	result, err := NewOpenRouterEnumerator(server.Client()).Enumerate(context.Background(), Endpoint{Alias: "router", Type: "openrouter", BaseURL: server.URL}, EnumerationOptions{})
	if err != nil {
		t.Fatalf("Enumerate: %v", err)
	}
	if len(result.Models) != 3 || result.Models[0].ID != "openai/gpt-4" || result.Models[1].ID != "anthropic/claude-3" || result.Models[2].DisplayName != "fallback-model" {
		t.Fatalf("models: %+v", result.Models)
	}
}

func TestOpenRouterEnumeratorStopsAtCrossHostNext(t *testing.T) {
	other := httptest.NewServer(http.NotFoundHandler())
	defer other.Close()
	fixture := []byte(`{"data":[{"id":"first","architecture":{"output_modalities":["text"]}}],"links":{"next":"` + other.URL + `/stolen"}}`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(fixture) }))
	defer server.Close()
	result, err := NewOpenRouterEnumerator(server.Client()).Enumerate(context.Background(), Endpoint{Type: "openrouter", BaseURL: server.URL}, EnumerationOptions{})
	if err != nil {
		t.Fatalf("Enumerate: %v", err)
	}
	if len(result.Models) != 1 || result.Models[0].ID != "first" {
		t.Fatalf("models: %+v", result.Models)
	}
}

func TestOpenRouterEnumeratorLoopGuard(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		_, _ = w.Write([]byte(`{"data":[],"links":{"next":"/api/v1/models"}}`))
	}))
	defer server.Close()
	_, err := NewOpenRouterEnumerator(server.Client()).Enumerate(context.Background(), Endpoint{Type: "openrouter", BaseURL: server.URL}, EnumerationOptions{})
	if err != nil {
		t.Fatalf("Enumerate: %v", err)
	}
	if requests != 1 {
		t.Fatalf("requests: got %d, want 1", requests)
	}
}

func TestSafeOpenRouterNextURL(t *testing.T) {
	base, _ := url.Parse("https://example.com/api/v1/models")
	if got, ok := safeOpenRouterNextURL(base, "?page=2"); !ok || got != "https://example.com/api/v1/models?page=2" {
		t.Fatalf("next URL: got %q, %v", got, ok)
	}
}

func readModelFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return data
}

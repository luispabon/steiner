package modelcatalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestOpenAIEnumerator(t *testing.T) {
	fixture, err := os.ReadFile("testdata/openai_models.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("path: got %q, want /v1/models", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer secret" {
			t.Errorf("authorization: got %q", got)
		}
		if got := r.Header.Get("X-Test"); got != "custom" {
			t.Errorf("custom header: got %q", got)
		}
		if got := r.Header.Get("ETag"); got != "" {
			t.Errorf("request etag: got %q", got)
		}
		w.Header().Set("ETag", "etag-1")
		_, _ = w.Write(fixture)
	}))
	defer server.Close()

	e := NewOpenAIEnumerator(server.Client())
	result, err := e.Enumerate(context.Background(), Endpoint{
		Alias: "main", Type: "openai", BaseURL: server.URL, APIKey: "secret",
		Headers: map[string]string{"X-Test": "custom", "Authorization": "wrong"},
	}, EnumerationOptions{ETag: "old"})
	if err != nil {
		t.Fatalf("Enumerate: %v", err)
	}
	if result.ETag != "etag-1" || result.NotModified {
		t.Fatalf("result metadata: %+v", result)
	}
	if len(result.Models) != 1 || result.Models[0].ID != "gpt-4.1" {
		t.Fatalf("models: %+v", result.Models)
	}
}

func TestLiteLLMModeOverridesHeuristic(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"embed-special","mode":"chat"},{"id":"ordinary","mode":"embedding"},{"id":"gpt-4.1"}]}`))
	}))
	defer server.Close()
	result, err := NewOpenAIEnumerator(server.Client()).Enumerate(context.Background(), Endpoint{Type: "litellm", BaseURL: server.URL}, EnumerationOptions{})
	if err != nil {
		t.Fatalf("Enumerate: %v", err)
	}
	if len(result.Models) != 2 || result.Models[0].ID != "embed-special" || result.Models[1].ID != "gpt-4.1" {
		t.Fatalf("models: %+v", result.Models)
	}
}

func TestOpenAIEnumeratorErrors(t *testing.T) {
	for _, test := range []struct {
		name string
		code int
		body string
	}{
		{"status", http.StatusBadGateway, `{}`},
		{"malformed", http.StatusOK, `{`},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.code)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			_, err := NewOpenAIEnumerator(server.Client()).Enumerate(context.Background(), Endpoint{Type: "openai", BaseURL: server.URL}, EnumerationOptions{})
			if err == nil {
				t.Fatal("Enumerate: expected error")
			}
		})
	}
}

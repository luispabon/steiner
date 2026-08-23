package modelcatalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestLMStudioEnumerator(t *testing.T) {
	fixture, err := os.ReadFile("testdata/lmstudio_models.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/models" {
			t.Errorf("path: got %q, want /api/v1/models", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer secret" {
			t.Errorf("authorization: got %q", got)
		}
		_, _ = w.Write(fixture)
	}))
	defer server.Close()

	result, err := NewLMStudioEnumerator(server.Client()).Enumerate(context.Background(), Endpoint{
		Alias: "studio", Type: "lmstudio", BaseURL: server.URL, APIKey: "secret",
	}, EnumerationOptions{})
	if err != nil {
		t.Fatalf("Enumerate: %v", err)
	}
	if len(result.Models) != 2 {
		t.Fatalf("models: got %+v", result.Models)
	}
	model := result.Models[0]
	if model.ID != "qwen/qwen3-8b" || model.DisplayName != "Qwen 3 8B" || model.ContextLength != 32768 || model.Description != "A local model" {
		t.Fatalf("model: %+v", model)
	}
	if len(model.SupportedEfforts) != 2 || model.SupportedEfforts[1] != "high" {
		t.Fatalf("efforts: %v", model.SupportedEfforts)
	}
	if result.Models[1].DisplayName != "plain-model" {
		t.Fatalf("fallback display: %+v", result.Models[1])
	}
}

func TestLMStudioEnumeratorWithoutAPIKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("authorization: got %q", got)
		}
		_, _ = w.Write([]byte(`{"models":[]}`))
	}))
	defer server.Close()
	_, err := NewLMStudioEnumerator(server.Client()).Enumerate(context.Background(), Endpoint{
		Type: "lmstudio", BaseURL: server.URL, Headers: map[string]string{"Authorization": ""},
	}, EnumerationOptions{})
	if err != nil {
		t.Fatalf("Enumerate: %v", err)
	}
}

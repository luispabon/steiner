package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/luispabon/steiner/internal/config"
)

func TestOllamaProbeContextWindow_ModelInfoContextLength(t *testing.T) {
	payload := ollamaShowResponse{
		ModelInfo: map[string]any{
			"general.context_length": float64(65536),
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/show" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(payload)
	}))
	defer srv.Close()

	got := ollamaProbeContextWindow(context.Background(), srv.URL, "llama3:latest", srv.Client())
	if got != 65536 {
		t.Errorf("ContextWindow: got %d, want 65536", got)
	}
}

func TestOllamaProbeContextWindow_FallbackToParameters(t *testing.T) {
	payload := ollamaShowResponse{
		ModelInfo:  map[string]any{},
		Parameters: "num_ctx 32768\ntemperature 0.7\n",
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(payload)
	}))
	defer srv.Close()

	got := ollamaProbeContextWindow(context.Background(), srv.URL, "llama3:latest", srv.Client())
	if got != 32768 {
		t.Errorf("ContextWindow: got %d, want 32768", got)
	}
}

func TestOllamaProbeContextWindow_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "model not found", http.StatusNotFound)
	}))
	defer srv.Close()

	got := ollamaProbeContextWindow(context.Background(), srv.URL, "unknown:model", srv.Client())
	if got != 0 {
		t.Errorf("expected 0 on error, got %d", got)
	}
}

func TestContextLenFromModelInfo(t *testing.T) {
	tests := []struct {
		name string
		info map[string]any
		want int
	}{
		{"nil map", nil, 0},
		{"missing key", map[string]any{"other": "value"}, 0},
		{"float64 value", map[string]any{"general.context_length": float64(128000)}, 128000},
		{"int value", map[string]any{"general.context_length": int(4096)}, 4096},
		{"int64 value", map[string]any{"general.context_length": int64(32768)}, 32768},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := contextLenFromModelInfo(tc.info)
			if got != tc.want {
				t.Errorf("got %d, want %d", got, tc.want)
			}
		})
	}
}

func TestContextLenFromParameters(t *testing.T) {
	tests := []struct {
		name   string
		params string
		want   int
	}{
		{"empty", "", 0},
		{"no num_ctx", "temperature 0.7\ntop_p 0.9\n", 0},
		{"has num_ctx", "temperature 0.7\nnum_ctx 32768\n", 32768},
		{"num_ctx first", "num_ctx 65536\nstop <|eot_id|>\n", 65536},
		{"malformed value", "num_ctx abc\n", 0},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := contextLenFromParameters(tc.params)
			if got != tc.want {
				t.Errorf("got %d, want %d", got, tc.want)
			}
		})
	}
}

// TestProbeSource_OllamaEndToEnd verifies probeSource still fills
// ContextWindow via the relocated Ollama probing logic when the catalog has
// no data for the model.
func TestProbeSource_OllamaEndToEnd(t *testing.T) {
	payload := ollamaShowResponse{
		ModelInfo: map[string]any{
			"general.context_length": float64(8192),
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(payload)
	}))
	defer srv.Close()

	ref := modelRef{
		Provider:       config.ProviderConfig{Type: config.ProviderTypeOllama, BaseURL: srv.URL},
		Profile:        profileFor(config.ProviderTypeOllama),
		BackendModelID: "llama3:latest",
	}
	s := probeSource{httpClient: srv.Client()}
	res := s.resolve(context.Background(), ref, fieldSet(fieldContextWindow|fieldMaxOutput))
	if !res.facts.ContextWindow.Known || res.facts.ContextWindow.Value != 8192 {
		t.Fatalf("ContextWindow = %+v, want Known with value 8192", res.facts.ContextWindow)
	}
	if res.facts.ContextWindow.Source != FactSourceDiscovery {
		t.Errorf("Source = %q, want %q", res.facts.ContextWindow.Source, FactSourceDiscovery)
	}
}

func TestProbeSource_NonOllamaProviderLeavesUnknown(t *testing.T) {
	ref := modelRef{
		Provider:       config.ProviderConfig{Type: config.ProviderTypeOpenRouter, BaseURL: "http://localhost"},
		Profile:        profileFor(config.ProviderTypeOpenRouter),
		BackendModelID: "openai/gpt-4o",
	}
	s := probeSource{httpClient: http.DefaultClient}
	res := s.resolve(context.Background(), ref, fieldSet(fieldContextWindow|fieldMaxOutput))
	if res.facts.ContextWindow.Known {
		t.Fatalf("expected ContextWindow unknown for non-Ollama provider, got %+v", res.facts.ContextWindow)
	}
}

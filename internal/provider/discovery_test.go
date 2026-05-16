package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/luispabon/steiner/internal/config"
)

// makeProviderConfig creates a minimal ProviderConfig for the given type string.
func makeProviderConfig(provType string) config.ProviderConfig {
	return config.ProviderConfig{
		Type:    config.ProviderType(provType),
		BaseURL: "http://localhost:1234",
	}
}

// --- OpenRouter tests ---

func TestOpenRouterDiscoverer_Success(t *testing.T) {
	payload := openRouterModelsResponse{
		Data: []openRouterModelEntry{
			{
				ID:         "openai/gpt-4o",
				ContextLen: 128000,
				TopProvider: openRouterTopProviderInfo{
					MaxCompletionTokens: 16384,
				},
			},
			{
				ID:         "other/model",
				ContextLen: 8192,
			},
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/models" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload)
	}))
	defer srv.Close()

	d := &openRouterDiscoverer{baseURL: srv.URL, apiKey: "test-key", httpClient: srv.Client()}
	meta, err := d.DiscoverModelMetadata(context.Background(), "openai/gpt-4o")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta.ContextWindow != 128000 {
		t.Errorf("ContextWindow: got %d, want 128000", meta.ContextWindow)
	}
	if meta.MaxOutputTokens != 16384 {
		t.Errorf("MaxOutputTokens: got %d, want 16384", meta.MaxOutputTokens)
	}
}

func TestOpenRouterDiscoverer_ModelNotFound(t *testing.T) {
	payload := openRouterModelsResponse{
		Data: []openRouterModelEntry{
			{ID: "other/model", ContextLen: 8192},
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(payload)
	}))
	defer srv.Close()

	d := &openRouterDiscoverer{baseURL: srv.URL, apiKey: "", httpClient: srv.Client()}
	meta, err := d.DiscoverModelMetadata(context.Background(), "missing/model")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta.ContextWindow != 0 || meta.MaxOutputTokens != 0 {
		t.Errorf("expected zero metadata for missing model, got %+v", meta)
	}
}

func TestOpenRouterDiscoverer_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	d := &openRouterDiscoverer{baseURL: srv.URL, apiKey: "", httpClient: srv.Client()}
	meta, err := d.DiscoverModelMetadata(context.Background(), "any/model")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta.ContextWindow != 0 || meta.MaxOutputTokens != 0 {
		t.Errorf("expected zero metadata on 404, got %+v", meta)
	}
}

func TestOpenRouterDiscoverer_InvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not-json"))
	}))
	defer srv.Close()

	d := &openRouterDiscoverer{baseURL: srv.URL, apiKey: "", httpClient: srv.Client()}
	meta, err := d.DiscoverModelMetadata(context.Background(), "any/model")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta.ContextWindow != 0 || meta.MaxOutputTokens != 0 {
		t.Errorf("expected zero metadata on parse error, got %+v", meta)
	}
}

// --- Ollama tests ---

func TestOllamaDiscoverer_ModelInfoContextLength(t *testing.T) {
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

	d := &ollamaDiscoverer{baseURL: srv.URL, httpClient: srv.Client()}
	meta, err := d.DiscoverModelMetadata(context.Background(), "llama3:latest")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta.ContextWindow != 65536 {
		t.Errorf("ContextWindow: got %d, want 65536", meta.ContextWindow)
	}
	if meta.MaxOutputTokens != 0 {
		t.Errorf("MaxOutputTokens: got %d, want 0", meta.MaxOutputTokens)
	}
}

func TestOllamaDiscoverer_FallbackToParameters(t *testing.T) {
	payload := ollamaShowResponse{
		ModelInfo:  map[string]any{},
		Parameters: "num_ctx 32768\ntemperature 0.7\n",
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(payload)
	}))
	defer srv.Close()

	d := &ollamaDiscoverer{baseURL: srv.URL, httpClient: srv.Client()}
	meta, err := d.DiscoverModelMetadata(context.Background(), "llama3:latest")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta.ContextWindow != 32768 {
		t.Errorf("ContextWindow: got %d, want 32768", meta.ContextWindow)
	}
}

func TestOllamaDiscoverer_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "model not found", http.StatusNotFound)
	}))
	defer srv.Close()

	d := &ollamaDiscoverer{baseURL: srv.URL, httpClient: srv.Client()}
	meta, err := d.DiscoverModelMetadata(context.Background(), "unknown:model")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta.ContextWindow != 0 || meta.MaxOutputTokens != 0 {
		t.Errorf("expected zero metadata on error, got %+v", meta)
	}
}

// --- Generic discoverer tests ---

func TestGenericDiscoverer_AlwaysZero(t *testing.T) {
	d := &genericDiscoverer{baseURL: "http://localhost:1234", httpClient: http.DefaultClient}
	meta, err := d.DiscoverModelMetadata(context.Background(), "any-model")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta.ContextWindow != 0 || meta.MaxOutputTokens != 0 {
		t.Errorf("expected zero metadata, got %+v", meta)
	}
}

// --- LM Studio discoverer tests ---

func TestLMStudioDiscoverer_AlwaysZero(t *testing.T) {
	d := &lmStudioDiscoverer{baseURL: "http://localhost:1234", httpClient: http.DefaultClient}
	meta, err := d.DiscoverModelMetadata(context.Background(), "any-model")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta.ContextWindow != 0 || meta.MaxOutputTokens != 0 {
		t.Errorf("expected zero metadata, got %+v", meta)
	}
}

// --- NewDiscoverer factory tests ---

func TestNewDiscoverer_ReturnsCorrectType(t *testing.T) {
	tests := []struct {
		providerType string
		wantNil      bool
		wantType     string
	}{
		{"openrouter", false, "*provider.openRouterDiscoverer"},
		{"ollama", false, "*provider.ollamaDiscoverer"},
		{"lmstudio", false, "*provider.lmStudioDiscoverer"},
		{"openai_compat", false, "*provider.genericDiscoverer"},
		{"openai", true, ""},
		{"anthropic", true, ""},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.providerType, func(t *testing.T) {
			from := makeProviderConfig(tc.providerType)
			d := NewDiscoverer(from, http.DefaultClient)
			if tc.wantNil && d != nil {
				t.Errorf("expected nil discoverer for %q, got %T", tc.providerType, d)
			}
			if !tc.wantNil && d == nil {
				t.Errorf("expected non-nil discoverer for %q", tc.providerType)
			}
		})
	}
}

func TestNewDiscoverer_NilHTTPClientFallsBack(t *testing.T) {
	from := makeProviderConfig("openrouter")
	d := NewDiscoverer(from, nil)
	if d == nil {
		t.Fatal("expected non-nil discoverer when httpClient is nil")
	}
}

// --- contextLen helper tests ---

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

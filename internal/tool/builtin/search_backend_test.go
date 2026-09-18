package builtin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/deepnoodle-ai/wonton/web"

	"github.com/luispabon/steiner/internal/config"
)

func TestNewSearchBackend(t *testing.T) {
	tests := []struct {
		name       string
		config     config.SearchConfig
		envSetup   map[string]string
		wantType   reflect.Type
		wantErrMsg string
	}{
		{
			name:   "empty backend returns nil",
			config: config.SearchConfig{Backend: ""},
		},
		{
			name:       "unknown backend returns error",
			config:     config.SearchConfig{Backend: "unknown"},
			wantErrMsg: `search backend: unsupported backend "unknown"`,
		},
		{
			name:       "google backend requires credentials",
			config:     config.SearchConfig{Backend: "google"},
			wantErrMsg: "missing google search cx",
		},
		{
			name:       "kagi backend requires credentials",
			config:     config.SearchConfig{Backend: "kagi"},
			wantErrMsg: "missing kagi api key",
		},
		{
			name:       "brave backend requires API key",
			config:     config.SearchConfig{Backend: "brave"},
			wantErrMsg: "brave searcher: api key is empty",
		},
		{
			name:     "brave backend with API key returns BraveSearcher",
			config:   config.SearchConfig{Backend: "brave", BraveAPIKey: "test-key"},
			wantType: reflect.TypeOf(&braveSearcher{}),
		},
		{
			name:       "searxng backend requires base URL",
			config:     config.SearchConfig{Backend: "searxng"},
			wantErrMsg: "searxng searcher: base url is empty",
		},
		{
			name:     "searxng backend with URL returns SearxngSearcher",
			config:   config.SearchConfig{Backend: "searxng", SearxngURL: "http://localhost:8888"},
			wantType: reflect.TypeOf(&searxngSearcher{}),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for key, val := range tt.envSetup {
				t.Setenv(key, val)
			}

			searcher, err := NewSearchBackend(tt.config)

			if tt.wantErrMsg != "" {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if err.Error() != tt.wantErrMsg {
					t.Fatalf("expected error %q, got %q", tt.wantErrMsg, err.Error())
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.wantType == nil {
				if searcher != nil {
					t.Fatalf("expected nil, got %T", searcher)
				}
			} else {
				if searcher == nil {
					t.Fatalf("expected searcher, got nil")
				}
				if gotType := reflect.TypeOf(searcher); gotType != tt.wantType {
					t.Fatalf("searcher type = %s, want %s", gotType, tt.wantType)
				}
			}
		})
	}
}

func TestSearxngSearcher(t *testing.T) {
	t.Run("constructor returns error on empty base URL", func(t *testing.T) {
		_, err := NewSearxngSearcher("")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if err.Error() != "searxng searcher: base url is empty" {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("successful search maps results", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			resp := map[string]any{
				"results": []map[string]string{
					{
						"url":     "https://example.com/1",
						"title":   "Example 1",
						"content": "This is example 1",
					},
					{
						"url":     "https://example.com/2",
						"title":   "Example 2",
						"content": "This is example 2",
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		}))
		defer server.Close()

		searcher, err := NewSearxngSearcher(server.URL)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		result, err := searcher.Search(context.Background(), &web.SearchInput{
			Query: "test",
			Limit: 10,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(result.Items) != 2 {
			t.Fatalf("expected 2 items, got %d", len(result.Items))
		}

		if result.Items[0].URL != "https://example.com/1" {
			t.Fatalf("expected URL https://example.com/1, got %q", result.Items[0].URL)
		}
		if result.Items[0].Title != "Example 1" {
			t.Fatalf("expected title Example 1, got %q", result.Items[0].Title)
		}
		if result.Items[0].Description != "This is example 1" {
			t.Fatalf("expected description 'This is example 1', got %q", result.Items[0].Description)
		}
	})

	t.Run("non-200 returns error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte("not found"))
		}))
		defer server.Close()

		searcher, err := NewSearxngSearcher(server.URL)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		_, err = searcher.Search(context.Background(), &web.SearchInput{
			Query: "test",
			Limit: 10,
		})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("context cancellation", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"results":[]}`))
		}))
		defer server.Close()

		searcher, err := NewSearxngSearcher(server.URL)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err = searcher.Search(ctx, &web.SearchInput{
			Query: "test",
			Limit: 10,
		})
		if err == nil {
			t.Fatal("expected error from cancelled context, got nil")
		}
	})

	t.Run("limit defaults and capping", func(t *testing.T) {
		var gotCount string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotCount = r.URL.Query().Get("count")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"results":[]}`))
		}))
		defer server.Close()

		searcher, err := NewSearxngSearcher(server.URL)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Test default limit
		_, err = searcher.Search(context.Background(), &web.SearchInput{
			Query: "test",
			Limit: 0,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if gotCount != "10" {
			t.Errorf("count = %q, want %q (default limit)", gotCount, "10")
		}

		// Test limit capping at 30
		_, err = searcher.Search(context.Background(), &web.SearchInput{
			Query: "test",
			Limit: 100,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if gotCount != "30" {
			t.Errorf("count = %q, want %q (capped limit)", gotCount, "30")
		}
	})
}

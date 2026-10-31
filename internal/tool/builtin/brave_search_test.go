package builtin

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/deepnoodle-ai/wonton/web"
)

func TestNewBraveSearcher(t *testing.T) {
	t.Run("returns error when api key is empty", func(t *testing.T) {
		_, err := NewBraveSearcher("")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("returns searcher when api key is provided", func(t *testing.T) {
		s, err := NewBraveSearcher("test-key")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if s == nil {
			t.Fatal("expected searcher, got nil")
		}
	})
}

func TestBraveSearcherSearch(t *testing.T) {
	tests := []struct {
		name           string
		statusCode     int
		responseBody   string
		input          *web.SearchInput
		expectedLen    int
		expectedTitle  string
		expectedURL    string
		expectedDesc   string
		expectedErrMsg string
	}{
		{
			name:       "successful search with default limit",
			statusCode: 200,
			responseBody: `{
				"web": {
					"results": [
						{
							"url": "https://example.com",
							"title": "Example",
							"description": "An example site"
						}
					]
				}
			}`,
			input:         &web.SearchInput{Query: "test"},
			expectedLen:   1,
			expectedTitle: "Example",
			expectedURL:   "https://example.com",
			expectedDesc:  "An example site",
		},
		{
			name:       "search with custom limit",
			statusCode: 200,
			responseBody: `{
				"web": {
					"results": [
						{
							"url": "https://result1.com",
							"title": "Result 1",
							"description": "First result"
						},
						{
							"url": "https://result2.com",
							"title": "Result 2",
							"description": "Second result"
						}
					]
				}
			}`,
			input:         &web.SearchInput{Query: "test", Limit: 2},
			expectedLen:   2,
			expectedTitle: "Result 1",
			expectedURL:   "https://result1.com",
			expectedDesc:  "First result",
		},
		{
			name:       "search with limit clamped to max 20",
			statusCode: 200,
			responseBody: `{
				"web": {
					"results": []
				}
			}`,
			input:       &web.SearchInput{Query: "test", Limit: 100},
			expectedLen: 0,
		},
		{
			name:           "http error status",
			statusCode:     500,
			responseBody:   "Internal Server Error",
			input:          &web.SearchInput{Query: "test"},
			expectedErrMsg: "http status 500",
		},
		{
			name:           "invalid json response",
			statusCode:     200,
			responseBody:   "not valid json",
			input:          &web.SearchInput{Query: "test"},
			expectedErrMsg: "decode response",
		},
		{
			name:         "empty results",
			statusCode:   200,
			responseBody: `{"web": {"results": []}}`,
			input:        &web.SearchInput{Query: "noresults"},
			expectedLen:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("X-Subscription-Token") == "" {
					t.Error("missing X-Subscription-Token header")
				}

				if r.Header.Get("Accept") != "application/json" {
					t.Errorf("Accept header = %s, want application/json", r.Header.Get("Accept"))
				}

				query := r.URL.Query()
				if query.Get("q") != tt.input.Query {
					t.Errorf("query q = %s, want %s", query.Get("q"), tt.input.Query)
				}

				w.WriteHeader(tt.statusCode)
				_, _ = w.Write([]byte(tt.responseBody))
			}))
			defer server.Close()

			s, err := NewBraveSearcher("test-api-key")
			if err != nil {
				t.Fatalf("failed to create searcher: %v", err)
			}
			searcher := s.(*braveSearcher)
			searcher.endpoint = server.URL

			ctx := context.Background()
			output, err := searcher.Search(ctx, tt.input)

			if tt.expectedErrMsg != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.expectedErrMsg)
				}
				if !strings.Contains(err.Error(), tt.expectedErrMsg) {
					t.Errorf("error = %v, want to contain %q", err, tt.expectedErrMsg)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if output == nil {
				t.Fatal("expected output, got nil")
			}

			if len(output.Items) != tt.expectedLen {
				t.Errorf("items length = %d, want %d", len(output.Items), tt.expectedLen)
			}

			if tt.expectedLen > 0 {
				if output.Items[0].Title != tt.expectedTitle {
					t.Errorf("title = %s, want %s", output.Items[0].Title, tt.expectedTitle)
				}
				if output.Items[0].URL != tt.expectedURL {
					t.Errorf("url = %s, want %s", output.Items[0].URL, tt.expectedURL)
				}
				if output.Items[0].Description != tt.expectedDesc {
					t.Errorf("description = %s, want %s", output.Items[0].Description, tt.expectedDesc)
				}
			}
		})
	}
}

func TestBraveSearcherContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"web": {"results": []}}`))
	}))
	defer server.Close()

	s, err := NewBraveSearcher("test-api-key")
	if err != nil {
		t.Fatalf("failed to create searcher: %v", err)
	}
	searcher := s.(*braveSearcher)
	searcher.endpoint = server.URL

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = searcher.Search(ctx, &web.SearchInput{Query: "test"})
	if err == nil {
		t.Fatal("expected error on cancelled context")
	}
}

func TestBraveSearcherNilInput(t *testing.T) {
	searcher, err := NewBraveSearcher("test-api-key")
	if err != nil {
		t.Fatalf("failed to create searcher: %v", err)
	}

	ctx := context.Background()
	_, err = searcher.Search(ctx, nil)
	if err == nil {
		t.Fatal("expected error for nil input")
	}
}

// TestBraveSearcherBoundsErrorBodyRead serves a large 500 body as a stream and
// checks the client stops at searchErrorBodyLimit: the error snippet stays at
// the first 100 bytes, and the server never gets to write the whole body.
func TestBraveSearcherBoundsErrorBodyRead(t *testing.T) {
	const (
		chunkCount = 512 // 16 MiB, far beyond any socket buffering
		chunkSize  = 32 << 10
	)
	var served atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		chunk := bytes.Repeat([]byte("x"), chunkSize)
		for range chunkCount {
			n, err := w.Write(chunk)
			served.Add(int64(n))
			if err != nil {
				return
			}
			w.(http.Flusher).Flush()
		}
	}))
	t.Cleanup(server.Close)

	s, err := NewBraveSearcher("test-api-key")
	if err != nil {
		t.Fatalf("failed to create searcher: %v", err)
	}
	searcher := s.(*braveSearcher)
	searcher.endpoint = server.URL

	_, err = searcher.Search(context.Background(), &web.SearchInput{Query: "test"})
	if err == nil {
		t.Fatal("expected error for a 500 response, got nil")
	}
	const statusPrefix = "search: http status 500: "
	if !strings.HasPrefix(err.Error(), statusPrefix) {
		t.Fatalf("error = %q, want prefix %q", err.Error(), statusPrefix)
	}
	if got := len(strings.TrimPrefix(err.Error(), statusPrefix)); got != 100 {
		t.Errorf("error body snippet = %d bytes, want 100", got)
	}
	if got := served.Load(); got >= chunkCount*chunkSize {
		t.Errorf("server wrote %d bytes of the error body, want fewer than %d: the client did not stop at the %d-byte cap", got, chunkCount*chunkSize, searchErrorBodyLimit)
	}
}

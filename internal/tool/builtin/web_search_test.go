package builtin

import (
	"context"
	"errors"
	"testing"

	"github.com/deepnoodle-ai/wonton/web"
)

// mockSearcher is a test mock that implements web.Searcher.
type mockSearcher struct {
	results []*web.SearchItem
	err     error
}

func (m *mockSearcher) Search(_ context.Context, _ *web.SearchInput) (*web.SearchOutput, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &web.SearchOutput{Items: m.results}, nil
}

func TestNewWebSearchTool(t *testing.T) {
	tests := []struct {
		name      string
		input     map[string]any
		searcher  web.Searcher
		wantItems int
		wantErr   bool
		wantKeys  []string
	}{
		{
			name: "successful search returns JSON array",
			input: map[string]any{
				"query": "test query",
				"limit": 10,
			},
			searcher: &mockSearcher{
				results: []*web.SearchItem{
					{
						URL:         "https://example.com/1",
						Title:       "Example 1",
						Description: "Result 1",
					},
					{
						URL:         "https://example.com/2",
						Title:       "Example 2",
						Description: "Result 2",
					},
				},
			},
			wantItems: 2,
			wantKeys:  []string{"url", "title", "description"},
		},
		{
			name: "empty query returns error",
			input: map[string]any{
				"query": "",
				"limit": 10,
			},
			searcher: &mockSearcher{},
			wantErr:  true,
		},
		{
			name: "search error returns structured error",
			input: map[string]any{
				"query": "test",
				"limit": 10,
			},
			searcher: &mockSearcher{
				err: errors.New("backend error"),
			},
			wantErr: false,
		},
		{
			name: "limit defaults to 10",
			input: map[string]any{
				"query": "test",
			},
			searcher: &mockSearcher{
				results: []*web.SearchItem{},
			},
			wantItems: 0,
		},
		{
			name: "limit capped at 30",
			input: map[string]any{
				"query": "test",
				"limit": 100,
			},
			searcher: &mockSearcher{
				results: []*web.SearchItem{},
			},
			wantItems: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			toolDef := NewWebSearchTool(tt.searcher)

			if toolDef.Name != "web_search" {
				t.Fatalf("expected name 'web_search', got %q", toolDef.Name)
			}

			result, err := toolDef.Handler(context.Background(), tt.input)

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			// Check if result is error map (from search backend error)
			if resultMap, ok := result.(map[string]any); ok {
				if _, hasError := resultMap["error"]; hasError {
					return
				}
			}

			// Check if result is array of items
			items, ok := result.([]map[string]string)
			if !ok {
				t.Fatalf("expected []map[string]string, got %T", result)
			}

			if len(items) != tt.wantItems {
				t.Fatalf("expected %d items, got %d", tt.wantItems, len(items))
			}

			if len(items) > 0 && len(tt.wantKeys) > 0 {
				for _, key := range tt.wantKeys {
					if _, ok := items[0][key]; !ok {
						t.Fatalf("expected key %q in result", key)
					}
				}
			}
		})
	}
}

func TestWebSearchInput(t *testing.T) {
	t.Run("NormalizeWebSearch defaults limit to 10", func(t *testing.T) {
		in := &WebSearchInput{Query: "test"}
		normalizeWebSearch(in)
		if in.Limit != 10 {
			t.Fatalf("expected limit 10, got %d", in.Limit)
		}
	})

	t.Run("NormalizeWebSearch caps limit at 30", func(t *testing.T) {
		in := &WebSearchInput{Query: "test", Limit: 100}
		normalizeWebSearch(in)
		if in.Limit != 30 {
			t.Fatalf("expected limit 30, got %d", in.Limit)
		}
	})

	t.Run("NormalizeWebSearch preserves valid limit", func(t *testing.T) {
		in := &WebSearchInput{Query: "test", Limit: 15}
		normalizeWebSearch(in)
		if in.Limit != 15 {
			t.Fatalf("expected limit 15, got %d", in.Limit)
		}
	})
}

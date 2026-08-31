package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/deepnoodle-ai/wonton/web"
)

// mockSearcher is a test mock that implements web.Searcher.
type mockSearcher struct {
	results []web.SearchItem
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
		name        string
		input       map[string]any
		searcher    web.Searcher
		wantResults []webSearchResultItem
		wantErr     bool
		wantErrText string
	}{
		{
			name: "successful search returns JSON array",
			input: map[string]any{
				"query": "test query",
				"limit": 10,
			},
			searcher: &mockSearcher{
				results: []web.SearchItem{
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
			wantResults: []webSearchResultItem{
				{URL: "https://example.com/1", Title: "Example 1", Description: "Result 1"},
				{URL: "https://example.com/2", Title: "Example 2", Description: "Result 2"},
			},
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
			name: "search error returns error",
			input: map[string]any{
				"query": "test",
				"limit": 10,
			},
			searcher: &mockSearcher{
				err: errors.New("backend error"),
			},
			wantErr:     true,
			wantErrText: "web_search: backend error",
		},
		{
			name: "limit defaults to 10",
			input: map[string]any{
				"query": "test",
			},
			searcher: &mockSearcher{
				results: []web.SearchItem{},
			},
			wantResults: []webSearchResultItem{},
		},
		{
			name: "limit capped at 30",
			input: map[string]any{
				"query": "test",
				"limit": 100,
			},
			searcher: &mockSearcher{
				results: []web.SearchItem{},
			},
			wantResults: []webSearchResultItem{},
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
				if tt.wantErrText != "" && err.Error() != tt.wantErrText {
					t.Fatalf("expected error %q, got %q", tt.wantErrText, err.Error())
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			items, ok := result.([]webSearchResultItem)
			if !ok {
				t.Fatalf("expected []webSearchResultItem, got %T", result)
			}

			if len(items) != len(tt.wantResults) {
				t.Fatalf("expected %d items, got %d", len(tt.wantResults), len(items))
			}

			for i, want := range tt.wantResults {
				if items[i].URL != want.URL {
					t.Errorf("item %d URL: expected %q, got %q", i, want.URL, items[i].URL)
				}
				if items[i].Title != want.Title {
					t.Errorf("item %d title: expected %q, got %q", i, want.Title, items[i].Title)
				}
				if items[i].Description != want.Description {
					t.Errorf("item %d description: expected %q, got %q", i, want.Description, items[i].Description)
				}
			}
		})
	}
}

func TestWebSearchResultItemJSON(t *testing.T) {
	item := webSearchResultItem{URL: "https://example.com"}

	got, err := json.Marshal(item)
	if err != nil {
		t.Fatalf("marshal result item: %v", err)
	}

	const want = `{"url":"https://example.com"}`
	if string(got) != want {
		t.Fatalf("expected JSON %s, got %s", want, got)
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

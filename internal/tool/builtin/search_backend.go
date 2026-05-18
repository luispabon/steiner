package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/deepnoodle-ai/dive/experimental/toolkit/google"
	"github.com/deepnoodle-ai/dive/experimental/toolkit/kagi"
	"github.com/deepnoodle-ai/wonton/web"

	"github.com/luispabon/steiner/internal/config"
)

// NewSearchBackend returns a web.Searcher for the given config, or nil, nil when backend is "".
func NewSearchBackend(cfg config.SearchConfig) (web.Searcher, error) {
	switch cfg.Backend {
	case "":
		return nil, nil
	case "google":
		return google.New()
	case "kagi":
		return kagi.New()
	case "brave":
		return NewBraveSearcher(os.Getenv("BRAVE_API_KEY"))
	case "searxng":
		return NewSearxngSearcher(cfg.SearxngURL)
	default:
		return nil, fmt.Errorf("search backend: unsupported backend %q", cfg.Backend)
	}
}

// searxngSearcher provides web search via SearXNG instance.
type searxngSearcher struct {
	baseURL    string
	httpClient *http.Client
}

type searxngResponse struct {
	Results []searxngResult `json:"results"`
}

type searxngResult struct {
	URL     string `json:"url"`
	Title   string `json:"title"`
	Content string `json:"content"`
}

// NewSearxngSearcher creates a new SearXNG searcher.
func NewSearxngSearcher(baseURL string) (web.Searcher, error) {
	if baseURL == "" {
		return nil, fmt.Errorf("searxng searcher: base url is empty")
	}
	return &searxngSearcher{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}, nil
}

// Search performs a search query against SearXNG.
func (s *searxngSearcher) Search(ctx context.Context, input *web.SearchInput) (*web.SearchOutput, error) {
	if input == nil {
		return nil, fmt.Errorf("search: input is nil")
	}

	limit := input.Limit
	if limit <= 0 {
		limit = 10
	}
	if limit > 30 {
		limit = 30
	}

	params := url.Values{}
	params.Set("q", input.Query)
	params.Set("format", "json")
	params.Set("categories", "general")
	params.Set("count", fmt.Sprintf("%d", limit))

	fullURL := s.baseURL + "/search?" + params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
	if err != nil {
		return nil, fmt.Errorf("search: create request: %w", err)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("search: http call: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		snippet := string(body)
		if len(snippet) > 100 {
			snippet = snippet[:100]
		}
		return nil, fmt.Errorf("search: http status %d: %s", resp.StatusCode, snippet)
	}

	var searxResp searxngResponse
	if err := json.NewDecoder(resp.Body).Decode(&searxResp); err != nil {
		return nil, fmt.Errorf("search: decode response: %w", err)
	}

	items := make([]*web.SearchItem, len(searxResp.Results))
	for i, r := range searxResp.Results {
		items[i] = &web.SearchItem{
			URL:         r.URL,
			Title:       r.Title,
			Description: r.Content,
		}
	}

	return &web.SearchOutput{Items: items}, nil
}

package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/deepnoodle-ai/wonton/web"
)

type braveSearcher struct {
	apiKey     string
	httpClient *http.Client
	endpoint   string
}

type braveResponse struct {
	Web struct {
		Results []braveResult `json:"results"`
	} `json:"web"`
}

type braveResult struct {
	URL         string `json:"url"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

// NewBraveSearcher returns a web.Searcher backed by the Brave Search API.
func NewBraveSearcher(apiKey string) (web.Searcher, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("brave searcher: api key is empty")
	}
	return &braveSearcher{
		apiKey: apiKey,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
		endpoint: "https://api.search.brave.com/res/v1/web/search",
	}, nil
}

func (bs *braveSearcher) Search(ctx context.Context, input *web.SearchInput) (*web.SearchOutput, error) {
	if input == nil {
		return nil, fmt.Errorf("search: input is nil")
	}

	limit := input.Limit
	if limit <= 0 {
		limit = 10
	}
	if limit > 20 {
		limit = 20
	}

	params := url.Values{}
	params.Set("q", input.Query)
	params.Set("count", fmt.Sprintf("%d", limit))

	fullURL := bs.endpoint + "?" + params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
	if err != nil {
		return nil, fmt.Errorf("search: create request: %w", err)
	}

	req.Header.Set("X-Subscription-Token", bs.apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := bs.httpClient.Do(req)
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

	var braveResp braveResponse
	if err := json.NewDecoder(resp.Body).Decode(&braveResp); err != nil {
		return nil, fmt.Errorf("search: decode response: %w", err)
	}

	items := make([]web.SearchItem, len(braveResp.Web.Results))
	for i, r := range braveResp.Web.Results {
		items[i] = web.SearchItem{
			URL:         r.URL,
			Title:       r.Title,
			Description: r.Description,
		}
	}

	return &web.SearchOutput{Items: items}, nil
}

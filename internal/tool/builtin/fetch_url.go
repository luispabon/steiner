package builtin

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/deepnoodle-ai/dive/toolkit"
	"github.com/deepnoodle-ai/wonton/fetch"

	"github.com/luispabon/steiner/internal/tool"
)

// FetchURLResult is the result from a fetch_url tool call.
type FetchURLResult struct {
	URL           string `json:"url"`
	Title         string `json:"title,omitempty"`
	Description   string `json:"description,omitempty"`
	Content       string `json:"content"`
	ContentLength int    `json:"content_length"`
}

// FetchURLError is an error result from a fetch_url tool call.
type FetchURLError struct {
	URL   string `json:"url"`
	Error string `json:"error"`
}

// NewFetchURLTool creates a ToolDef for the fetch_url tool.
func NewFetchURLTool(env Env) tool.ToolDef {
	return tool.ToolDef{
		Name:            "fetch_url",
		Description:     "Fetch and convert a URL to markdown content. Returns structured result with title, description, and markdown content.",
		ParameterSchema: FetchURLSchema(),
		Handler: func(ctx context.Context, input map[string]any) (any, error) {
			in, err := decodeInput[FetchURLInput](input)
			if err != nil {
				return nil, fmt.Errorf("fetch_url: %w", err)
			}

			if in.URL == "" {
				return nil, fmt.Errorf("fetch_url: invalid url: %w", fmt.Errorf("empty url"))
			}

			parsedURL, err := url.Parse(in.URL)
			if err != nil {
				return nil, fmt.Errorf("fetch_url: invalid url: %w", err)
			}

			if parsedURL.Scheme == "" || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") {
				return nil, fmt.Errorf("fetch_url: invalid url: %w", fmt.Errorf("missing or invalid scheme"))
			}

			NormalizeFetchURL(&in)

			httpClient := toolkit.SafeHTTPClient(15 * time.Second)
			fetcher := fetch.NewHTTPFetcher(fetch.HTTPFetcherOptions{
				Client:  httpClient,
				Timeout: 15 * time.Second,
			})

			req := &fetch.Request{
				URL:         in.URL,
				Formats:     []string{"markdown"},
				ExcludeTags: toolkit.DefaultFetchExcludeTags,
			}

			resp, err := fetcher.Fetch(ctx, req)
			if err != nil {
				return &FetchURLError{
					URL:   in.URL,
					Error: err.Error(),
				}, nil
			}

			if resp.StatusCode != 200 {
				return &FetchURLError{
					URL:   in.URL,
					Error: fmt.Sprintf("HTTP %d", resp.StatusCode),
				}, nil
			}

			content := resp.Markdown
			runes := []rune(content)
			if len(runes) > in.MaxSize {
				runes = runes[:in.MaxSize]
				content = string(runes)
			}

			return &FetchURLResult{
				URL:           resp.URL,
				Title:         resp.Metadata.Title,
				Description:   resp.Metadata.Description,
				Content:       content,
				ContentLength: len(runes),
			}, nil
		},
	}
}

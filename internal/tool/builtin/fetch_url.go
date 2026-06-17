package builtin

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/deepnoodle-ai/dive/toolkit"
	"github.com/deepnoodle-ai/wonton/fetch"

	"github.com/luispabon/steiner/internal/tool"
)

// FetchURLResult is the result from a fetch_url tool call.
type FetchURLResult struct {
	URL           string      `json:"url"`
	Title         string      `json:"title,omitempty"`
	Description   string      `json:"description,omitempty"`
	Content       string      `json:"content"`
	ContentLength int         `json:"content_length"`
	StatusCode    int         `json:"status_code,omitempty"`
	Image         *ImageBlock `json:"image,omitempty"`
}

// FetchURLError is an error result from a fetch_url tool call.
type FetchURLError struct {
	URL   string `json:"url"`
	Error string `json:"error"`
}

// NewFetchURLTool creates a ToolDef for the fetch_url tool.
// nolint:gocyclo // handler closure complexity is unavoidable with multi-branch content-type routing and image extension fallback
func NewFetchURLTool(_ Env) tool.ToolDef {
	return tool.ToolDef{
		Name:            "fetch_url",
		Description:     "Fetch a URL and return its content as markdown, or as image data (png, jpeg, gif, webp) for vision-capable providers.",
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

			// Peek at Content-Type via HEAD to decide how to handle the response.
			contentType := ""
			headReq, headErr := http.NewRequestWithContext(ctx, http.MethodHead, in.URL, nil)
			if headErr == nil {
				headResp, doErr := httpClient.Do(headReq)
				if doErr == nil {
					contentType = headResp.Header.Get("Content-Type")
					_ = headResp.Body.Close()
				}
				// On HEAD failure, fall through to wonton/fetch.
			}

			// Decide how to handle based on Content-Type.
			switch {
			case isImageContentType(contentType):
				imgBlock, statusCode, imgErr := fetchImageBytes(ctx, httpClient, in.URL, contentType)
				if imgErr != nil {
					return &FetchURLError{
						URL:   in.URL,
						Error: imgErr.Error(),
					}, nil
				}
				return &FetchURLResult{
					URL:        in.URL,
					StatusCode: statusCode,
					Image:      imgBlock,
				}, nil

			case contentType == "" || cleanContentType(contentType) == "application/octet-stream":
				// Extension fallback: treat as image if URL suggests an image.
				if hasImageExtension(in.URL) {
					imgBlock, statusCode, imgErr := fetchImageBytes(ctx, httpClient, in.URL, contentType)
					if imgErr != nil {
						return &FetchURLError{
							URL:   in.URL,
							Error: imgErr.Error(),
						}, nil
					}
					return &FetchURLResult{
						URL:        in.URL,
						StatusCode: statusCode,
						Image:      imgBlock,
					}, nil
				}
				// No image extension — fall through to wonton/fetch.

			case !isTextLikeContentType(contentType):
				return &FetchURLError{
					URL:   in.URL,
					Error: fmt.Sprintf("unsupported content type: %s", cleanContentType(contentType)),
				}, nil
			}

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
				StatusCode:    resp.StatusCode,
			}, nil
		},
	}
}

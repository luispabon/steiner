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

// inlineThreshold is the maximum number of content runes returned inline
// before content is saved to disk and a preview is returned instead.
const inlineThreshold = 10000

// FetchURLResult is the result from a fetch_url tool call.
type FetchURLResult struct {
	URL           string      `json:"url"`
	Title         string      `json:"title,omitempty"`
	Description   string      `json:"description,omitempty"`
	Content       string      `json:"content"`
	ContentLength int         `json:"content_length"`
	StatusCode    int         `json:"status_code,omitempty"`
	Image         *ImageBlock `json:"image,omitempty"`
	FilePath      string      `json:"file_path,omitempty"`
	NextOffset    int         `json:"next_offset,omitempty"`
	Message       string      `json:"message,omitempty"`
	Truncated     bool        `json:"truncated,omitempty"`
}

// FetchURLError is an error result from a fetch_url tool call.
type FetchURLError struct {
	URL   string `json:"url"`
	Error string `json:"error"`
}

// NewFetchURLTool creates a ToolDef for the fetch_url tool.
// nolint:gocyclo // handler closure complexity is unavoidable with multi-branch content-type routing and image extension fallback
func NewFetchURLTool(env Env) tool.ToolDef {
	return tool.ToolDef{
		Name:            "fetch_url",
		Description:     "Fetch a URL and return its content. Supports HTML (converted to markdown), text formats (JSON, YAML, plain text, CSV, etc.), and images (png, jpeg, gif, webp). Images are always saved to .steiner/tmp/fetched; use the read tool with the returned file path to inspect them. Large responses are saved to disk — use the read tool to paginate.",
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

			normalizeFetchURL(&in)

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
				img, statusCode, imgErr := fetchImageBytes(ctx, httpClient, in.URL, contentType)
				if imgErr != nil {
					return &FetchURLError{
						URL:   in.URL,
						Error: imgErr.Error(),
					}, nil
				}
				result, saveErr := saveFetchedImage(env.WorkDir, img)
				if saveErr != nil {
					return nil, fmt.Errorf("fetch_url: %w", saveErr)
				}
				result.URL = in.URL
				result.StatusCode = statusCode
				return result, nil

			case contentType == "" || cleanContentType(contentType) == "application/octet-stream":
				// Extension fallback: treat as image if URL suggests an image.
				if hasImageExtension(in.URL) {
					img, statusCode, imgErr := fetchImageBytes(ctx, httpClient, in.URL, contentType)
					if imgErr != nil {
						return &FetchURLError{
							URL:   in.URL,
							Error: imgErr.Error(),
						}, nil
					}
					result, saveErr := saveFetchedImage(env.WorkDir, img)
					if saveErr != nil {
						return nil, fmt.Errorf("fetch_url: %w", saveErr)
					}
					result.URL = in.URL
					result.StatusCode = statusCode
					return result, nil
				}
				// No image extension, fall through to wonton/fetch.

			case isTextLikeContentType(contentType) && !isHTMLContentType(contentType) && contentType != "":
				return fetchRawText(ctx, httpClient, in, env.WorkDir, contentType)

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

			return buildHTMLResult(in.URL, resp, env.WorkDir, in.MaxSize)
		},
	}
}

// buildHTMLResult converts a successful wonton/fetch response into a
// fetch_url result, applying max_size truncation and the inline/disk-saved
// inlineThreshold gate. inURL is the originally requested URL, used for
// error results (resp.URL reflects the final URL after redirects and is
// used for success results).
func buildHTMLResult(inURL string, resp *fetch.Response, workDir string, maxSize int) (any, error) {
	if resp.StatusCode != 200 {
		return &FetchURLError{
			URL:   inURL,
			Error: fmt.Sprintf("HTTP %d", resp.StatusCode),
		}, nil
	}

	content := resp.Markdown
	runes := []rune(content)
	// maxSize is applied in runes here (decoded markdown text), unlike
	// fetchRawText which applies it in bytes to the raw HTTP body.
	truncated := len(runes) > maxSize
	if truncated {
		runes = runes[:maxSize]
		content = string(runes)
	}

	if len(runes) <= inlineThreshold {
		return &FetchURLResult{
			URL:           resp.URL,
			Title:         resp.Metadata.Title,
			Description:   resp.Metadata.Description,
			Content:       content,
			ContentLength: len(runes),
			StatusCode:    resp.StatusCode,
			Truncated:     truncated,
		}, nil
	}

	result, err := saveFetchedContent(workDir, content, "text/html", truncated)
	if err != nil {
		return nil, fmt.Errorf("fetch_url: %w", err)
	}
	result.URL = resp.URL
	result.Title = resp.Metadata.Title
	result.Description = resp.Metadata.Description
	result.StatusCode = resp.StatusCode
	return result, nil
}

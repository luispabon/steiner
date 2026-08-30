package builtin

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"unicode/utf8"
)

// fetchRawText performs a direct GET for non-HTML text-like content types,
// bypassing wonton/fetch, and returns the raw text. Content at or under
// inlineThreshold runes is returned inline; larger content is saved to disk
// and a bounded preview is returned instead.
func fetchRawText(ctx context.Context, httpClient *http.Client, in FetchURLInput, workDir, contentType string) (any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, in.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("fetch_url: %w", err)
	}
	req.Header.Set("User-Agent", fetchUserAgent)

	resp, err := httpClient.Do(req)
	if err != nil {
		return &FetchURLError{URL: in.URL, Error: err.Error()}, nil
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != 200 {
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, errorBodySnippetRunes*utf8.UTFMax))
		if readErr != nil {
			return &FetchURLError{URL: in.URL, Error: readErr.Error()}, nil
		}
		return &FetchURLError{
			URL:        in.URL,
			Error:      fmt.Sprintf("HTTP %d", resp.StatusCode),
			StatusCode: resp.StatusCode,
			RetryAfter: resp.Header.Get("Retry-After"),
			Body:       boundedRuneSnippet(trimIncompleteUTF8SuffixString(string(body)), errorBodySnippetRunes),
		}, nil
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, int64(in.MaxSize)+1))
	if err != nil {
		return &FetchURLError{URL: in.URL, Error: err.Error()}, nil
	}

	truncated := len(data) > in.MaxSize
	if truncated {
		data = data[:in.MaxSize]
	}

	if truncated {
		data = trimIncompleteUTF8Suffix(data)
	}

	if isBinaryContent(data) {
		return &FetchURLError{
			URL:   in.URL,
			Error: fmt.Sprintf("response contains binary data despite content type %s; fetch_url only supports text content.", cleanContentType(contentType)),
		}, nil
	}

	content := string(data)
	runes := []rune(content)

	if len(runes) <= inlineThreshold {
		return &FetchURLResult{
			URL:           in.URL,
			Content:       content,
			ContentLength: len(runes),
			StatusCode:    resp.StatusCode,
			Truncated:     truncated,
		}, nil
	}

	result, err := saveFetchedContent(workDir, content, contentType, truncated)
	if err != nil {
		return nil, fmt.Errorf("fetch_url: %w", err)
	}
	result.URL = in.URL
	result.StatusCode = resp.StatusCode
	return result, nil
}

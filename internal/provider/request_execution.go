package provider

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

func boolValuePtr(v bool) *bool {
	return &v
}

// shouldDisableRemoteStorage reports whether the backend is an OpenAI-operated
// host, where request storage must be opted out of explicitly.
func shouldDisableRemoteStorage(baseURL *url.URL) bool {
	if baseURL == nil {
		return false
	}
	host := strings.ToLower(strings.TrimSpace(baseURL.Hostname()))
	return host == "api.openai.com" ||
		strings.HasSuffix(host, ".openai.com") ||
		host == "chatgpt.com" ||
		strings.HasSuffix(host, ".chatgpt.com")
}

func buildJSONPostRequest(ctx context.Context, target string, body []byte, stream bool, apiKey string, headers map[string]string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if stream {
		req.Header.Set("Accept", "text/event-stream")
	}
	if strings.TrimSpace(apiKey) != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	return req, nil
}

func (c *Client) executeHTTP(_ context.Context, req *http.Request) (*http.Response, error) {
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		defer closeResponseBody(resp.Body)
		return nil, c.readErrorResponse(resp)
	}
	return resp, nil
}

func closeResponseBody(body io.ReadCloser) {
	if body == nil {
		return
	}
	_ = body.Close()
}

func (c *Client) readErrorResponse(resp *http.Response) error {
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8192))
	if err != nil {
		return fmt.Errorf("read error response body: %w", err)
	}
	return &HTTPError{
		StatusCode: resp.StatusCode,
		Status:     resp.Status,
		Body:       summarizeErrorBody(resp.Header.Get("Content-Type"), string(body)),
		Header:     resp.Header.Clone(),
	}
}

func summarizeErrorBody(contentType, body string) string {
	text := strings.TrimSpace(body)
	if text == "" {
		return ""
	}
	if strings.Contains(strings.ToLower(contentType), "html") || looksLikeHTML(text) {
		text = htmlToErrorText(text)
	}
	const maxErrorBody = 1000
	if len(text) > maxErrorBody {
		text = strings.TrimSpace(text[:maxErrorBody]) + "..."
	}
	return text
}

func looksLikeHTML(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	return strings.HasPrefix(lower, "<!doctype html") || strings.HasPrefix(lower, "<html") || strings.Contains(lower, "<body")
}

func htmlToErrorText(body string) string {
	text := regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`).ReplaceAllString(body, " ")
	text = regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`).ReplaceAllString(text, " ")
	text = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`).ReplaceAllString(text, " title: $1 ")
	text = regexp.MustCompile(`(?s)<[^>]+>`).ReplaceAllString(text, " ")
	text = strings.NewReplacer("&nbsp;", " ", "&amp;", "&", "&lt;", "<", "&gt;", ">", "&#39;", "'", "&quot;", `"`).Replace(text)
	return strings.Join(strings.Fields(text), " ")
}

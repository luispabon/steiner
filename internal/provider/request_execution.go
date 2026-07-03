package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
)

func (p *OpenAICompat) buildRequestPayload(request ChatRequest, stream bool) ([]byte, error) {
	return p.marshalRequest(request, stream)
}

func boolValuePtr(v bool) *bool {
	return &v
}

func (p *OpenAICompat) buildHTTPRequest(ctx context.Context, _ ChatRequest, body []byte, stream bool) (*http.Request, error) {
	return buildJSONPostRequest(ctx, p.chatCompletionsURL(), body, stream, p.apiKey, p.headers)
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

func (p *OpenAICompat) executeHTTP(_ context.Context, req *http.Request) (*http.Response, error) {
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		defer closeResponseBody(resp.Body)
		return nil, p.readErrorResponse(resp)
	}
	return resp, nil
}

func (p *OpenAICompat) decodeNonStreamResponse(resp *http.Response) (*openAIResponse, error) {
	var payload openAIResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("%w: %w", errDecodeChatCompletionResponse, err)
	}
	return &payload, nil
}

func (p *OpenAICompat) decodeStreamResponse(ctx context.Context, body io.Reader, out chan<- ChatChunk) error {
	return decodeChatStream(ctx, body, out)
}

func closeResponseBody(body io.ReadCloser) {
	if body == nil {
		return
	}
	_ = body.Close()
}

func (p *OpenAICompat) readErrorResponse(resp *http.Response) error {
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

func (p *OpenAICompat) chatCompletionsURL() string {
	base := *p.baseURL
	base.Path = strings.TrimRight(base.Path, "/") + "/chat/completions"
	return base.String()
}

func (p *OpenAICompat) marshalRequest(request ChatRequest, stream bool) ([]byte, error) {
	wire, err := chatRequestWire(request, p.model, stream)
	if err != nil {
		return nil, err
	}
	if p.shouldDisableRemoteStorage() {
		wire.Store = boolValuePtr(false)
	}
	return json.Marshal(wire)
}

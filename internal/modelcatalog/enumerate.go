package modelcatalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/luispabon/steiner/internal/config"
)

// Enumerator discovers models from one provider wire format.
type Enumerator interface {
	Enumerate(ctx context.Context, ep Endpoint, opts EnumerationOptions) (EnumerationResult, error)
}

// Endpoint describes the configured provider endpoint used for enumeration.
type Endpoint struct {
	Alias   string
	Type    string
	BaseURL string
	APIKey  string
	Headers map[string]string
	Prepare func(context.Context) (Endpoint, error)
}

// EnumerationOptions controls conditional enumeration requests.
type EnumerationOptions struct {
	ETag string
}

// EnumerationResult contains models discovered by an Enumerator.
type EnumerationResult struct {
	Models      []DiscoveredModel
	ETag        string
	NotModified bool
}

// ForType returns an Enumerator for a supported provider type.
func ForType(t string) (Enumerator, error) {
	return ForTypeWithClient(config.ProviderType(t), nil)
}

// ForTypeWithClient returns an Enumerator using client for HTTP requests. A nil
// client uses a new default http.Client.
func ForTypeWithClient(t config.ProviderType, client *http.Client) (Enumerator, error) {
	switch t {
	case config.ProviderTypeOpenAI, config.ProviderTypeOpenAICompat, config.ProviderTypeLiteLLM, config.ProviderTypeOpencodeGo, config.ProviderTypeOpencodeZen:
		return NewOpenAIEnumerator(client), nil
	case config.ProviderTypeOllama:
		return NewOllamaEnumerator(client), nil
	case config.ProviderTypeLMStudio:
		return NewLMStudioEnumerator(client), nil
	case config.ProviderTypeOpenRouter:
		return NewOpenRouterEnumerator(client), nil
	case config.ProviderTypeAnthropic:
		return NewAnthropicEnumerator(client), nil
	case config.ProviderTypeCodex:
		return NewCodexEnumerator(client, "", nil), nil
	default:
		return nil, fmt.Errorf("unknown enumerator type %q", t)
	}
}

// SupportsType reports whether provider type has a built-in model enumerator.
func SupportsType(providerType config.ProviderType) bool {
	switch providerType {
	case config.ProviderTypeOpenAI, config.ProviderTypeOpenAICompat, config.ProviderTypeLiteLLM,
		config.ProviderTypeOllama, config.ProviderTypeLMStudio, config.ProviderTypeOpenRouter,
		config.ProviderTypeAnthropic, config.ProviderTypeCodex, config.ProviderTypeOpencodeGo, config.ProviderTypeOpencodeZen:
		return true
	default:
		return false
	}
}

// maxResponseBytes caps any enumeration response body. Real provider model
// lists are well under 2 MB; 10 MB leaves generous headroom.
const maxResponseBytes = 10 << 20

// maxErrorBodyBytes caps how much of a non-success body is read and embedded
// in an error message.
const maxErrorBodyBytes = 512

// clientOrDefault returns client, or a new client when nil. A client without a
// CheckRedirect gets a copy with one that refuses cross-origin redirects, so
// provider credentials in custom headers (which net/http does not strip) never
// reach another origin. The caller's client is not mutated.
func clientOrDefault(client *http.Client) *http.Client {
	if client == nil {
		return &http.Client{CheckRedirect: refuseCrossOriginRedirect}
	}
	if client.CheckRedirect != nil {
		return client
	}
	clone := *client
	clone.CheckRedirect = refuseCrossOriginRedirect
	return &clone
}

// originOf returns scheme://host:port for u, with the scheme's default port made explicit.
func originOf(u *url.URL) string {
	port := u.Port()
	if port == "" {
		switch u.Scheme {
		case "https":
			port = "443"
		case "http":
			port = "80"
		}
	}
	return strings.ToLower(u.Scheme) + "://" + net.JoinHostPort(strings.ToLower(u.Hostname()), port)
}

// refuseCrossOriginRedirect stops redirects that leave the original request's origin.
func refuseCrossOriginRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	if from, to := originOf(via[0].URL), originOf(req.URL); from != to {
		return fmt.Errorf("refusing cross-origin redirect from %s to %s", from, to)
	}
	return nil
}

func newGETRequest(ctx context.Context, ep Endpoint, endpoint string, authorization string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create model enumeration request: %w", err)
	}
	for key, value := range ep.Headers {
		req.Header.Set(key, value)
	}
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	return req, nil
}

func bearerAuthorization(apiKey string) string {
	if apiKey == "" {
		return ""
	}
	return "Bearer " + apiKey
}

func doJSONRequest(client *http.Client, req *http.Request, response any) (string, error) {
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("request model enumeration: %w", err)
	}
	defer func() { _ = resp.Body.Close() }() // Response body cleanup errors do not change enumeration result.
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", fmt.Errorf("enumerate models: unexpected status code %d", resp.StatusCode)
	}
	decoder := json.NewDecoder(capBody(resp.Body))
	if err := decoder.Decode(response); err != nil {
		return "", fmt.Errorf("decode model enumeration response: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return "", fmt.Errorf("decode model enumeration response: unexpected trailing JSON")
		}
		return "", fmt.Errorf("decode model enumeration response: %w", err)
	}
	return resp.Header.Get("ETag"), nil
}

var errBodyTooLarge = errors.New("response body exceeds size limit")

type cappedReader struct {
	r         io.Reader
	remaining int64
}

// capBody wraps r so reading more than maxResponseBytes fails with
// errBodyTooLarge instead of silently truncating.
func capBody(r io.Reader) io.Reader {
	return &cappedReader{r: r, remaining: maxResponseBytes}
}

func (c *cappedReader) Read(p []byte) (int, error) {
	if c.remaining < 0 {
		return 0, errBodyTooLarge
	}
	if int64(len(p)) > c.remaining+1 {
		p = p[:c.remaining+1]
	}
	n, err := c.r.Read(p)
	c.remaining -= int64(n)
	if c.remaining < 0 {
		return n - int(-c.remaining), errBodyTooLarge
	}
	return n, err
}

// truncateForError shortens s to at most maxErrorBodyBytes for embedding in errors.
func truncateForError(s string) string {
	s = strings.ToValidUTF8(s, "")
	if len(s) > maxErrorBodyBytes {
		return s[:maxErrorBodyBytes] + "..."
	}
	return s
}

package provider

import (
	"fmt"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var defaultHTTPClient = &http.Client{}

// ClientConfig configures a Provider Request Execution client.
type ClientConfig struct {
	BaseURL            string
	APIKey             string
	Headers            map[string]string
	Model              string
	Timeout            time.Duration
	Retry              RetryConfig
	HTTPClient         *http.Client
	Scheduler          *Scheduler
	ProviderType       string
	StreamErrorLog     *StreamErrorLogger
	MinRequestInterval time.Duration
}

// RetryConfig controls retry behavior for transient provider failures.
type RetryConfig struct {
	Enabled        bool
	MaxAttempts    int
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
	RetryAfterMax  time.Duration
}

// NewOpenAICompat creates a client that speaks the OpenAI chat-completions wire.
func NewOpenAICompat(cfg ClientConfig) (*Client, error) {
	client, err := newClient(cfg)
	if err != nil {
		return nil, err
	}
	client.wire = &openaiWire{
		baseURL:      client.baseURL,
		apiKey:       client.apiKey,
		headers:      client.headers,
		model:        client.model,
		providerType: client.providerType,
	}
	return client, nil
}

// NewAnthropic creates a client that speaks the Anthropic Messages wire.
func NewAnthropic(cfg ClientConfig) (*Client, error) {
	if strings.TrimSpace(cfg.ProviderType) == "" {
		cfg.ProviderType = "anthropic"
	}
	client, err := newClient(cfg)
	if err != nil {
		return nil, err
	}
	client.wire = &anthropicWire{
		baseURL: client.baseURL,
		apiKey:  client.apiKey,
		headers: client.headers,
		model:   client.model,
	}
	return client, nil
}

// NewCodexResponses creates a client that speaks the OpenAI Responses wire used
// by the Codex OAuth backend, paced by cfg.MinRequestInterval.
func NewCodexResponses(cfg ClientConfig) (*Client, error) {
	client, err := newClient(cfg)
	if err != nil {
		return nil, err
	}
	client.wire = &responsesWire{
		baseURL: client.baseURL,
		apiKey:  client.apiKey,
		headers: client.headers,
		model:   client.model,
	}
	client.minInterval = cfg.MinRequestInterval
	return client, nil
}

func newClient(cfg ClientConfig) (*Client, error) {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return nil, fmt.Errorf("base URL is required")
	}
	parsed, err := url.Parse(cfg.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse base URL: %w", err)
	}
	if strings.TrimSpace(cfg.Model) == "" {
		return nil, fmt.Errorf("model is required")
	}
	if cfg.Scheduler == nil {
		return nil, fmt.Errorf("scheduler is required")
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = defaultHTTPClient
	}
	if cfg.Timeout > 0 {
		// Clone the client AND its transport. A shallow client copy shares the
		// Transport pointer, so the upstream ResponseHeaderTimeout would still
		// fire first on slow servers and surface as
		// `net/http: timeout awaiting response headers` regardless of
		// Client.Timeout. Clear ResponseHeaderTimeout on the cloned transport;
		// Client.Timeout already bounds the entire request, including headers.
		cloned := *httpClient
		cloned.Timeout = cfg.Timeout
		if base, ok := cloned.Transport.(*http.Transport); ok && base != nil {
			t := base.Clone()
			t.ResponseHeaderTimeout = 0
			cloned.Transport = t
		}
		httpClient = &cloned
	}
	client := &Client{
		baseURL:        parsed,
		apiKey:         cfg.APIKey,
		headers:        copyHeaders(cfg.Headers),
		model:          cfg.Model,
		retry:          cfg.Retry,
		httpClient:     httpClient,
		scheduler:      cfg.Scheduler,
		providerType:   cfg.ProviderType,
		streamErrorLog: cfg.StreamErrorLog,
		sleep:          defaultRetrySleep,
		rand:           rand.New(rand.NewSource(time.Now().UnixNano())),
	}
	client.jitter = client.fullJitter
	return client, nil
}

func copyHeaders(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]string, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

package mcp

import (
	"fmt"
	"net/http"
	"net/url"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// newHTTPTransport constructs an HTTP transport for the server at spec.URL,
// with headers injected from spec.Headers. The SDK's StreamableClientTransport
// handles Server-Sent Events over HTTP; the headerTransport wrapper injects
// configured headers into every request. MaxRetries, DisableStandaloneSSE, and
// OAuthHandler are left at their zero values so SDK defaults apply; Timeout is
// not set on the HTTP client because SSE streams are long-lived and a client
// timeout would sever the connection (the connect deadline comes from the
// context in ConnectSession).
func newHTTPTransport(spec ServerSpec) (mcpsdk.Transport, error) {
	if spec.URL == "" {
		return nil, fmt.Errorf("build http transport for mcp server %q: URL is empty", spec.Name)
	}

	if _, err := url.Parse(spec.URL); err != nil {
		return nil, fmt.Errorf("build http transport for mcp server %q: invalid URL %q: %w", spec.Name, spec.URL, err)
	}

	transport := &mcpsdk.StreamableClientTransport{
		Endpoint: spec.URL,
		HTTPClient: &http.Client{
			Transport: &headerTransport{headers: spec.Headers},
		},
	}

	return transport, nil
}

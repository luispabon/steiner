package mcp

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"

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

	parsed, err := url.Parse(spec.URL)
	if err != nil {
		return nil, fmt.Errorf("build http transport for mcp server %q: invalid URL %q: %w", spec.Name, spec.URL, err)
	}

	transport := &mcpsdk.StreamableClientTransport{
		Endpoint: spec.URL,
		HTTPClient: &http.Client{
			Transport:     &headerTransport{headers: spec.Headers, origin: originOf(parsed)},
			CheckRedirect: refuseCrossOriginRedirect,
		},
	}

	return transport, nil
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

// refuseCrossOriginRedirect stops redirects that leave the original request's
// origin so configured credentials are never forwarded to another host.
func refuseCrossOriginRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	if from, to := originOf(via[0].URL), originOf(req.URL); from != to {
		return fmt.Errorf("refusing cross-origin redirect from %s to %s", from, to)
	}
	return nil
}

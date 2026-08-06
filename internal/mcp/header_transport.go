package mcp

import (
	"net/http"
	"sort"
)

// headerTransport injects a fixed set of headers onto every outgoing request.
//
// mcp.StreamableClientTransport has no field for static headers, so the supported way
// to attach them is a custom *http.Client whose Transport sets them. The SDK sets its
// own headers (Content-Type, Accept, Mcp-Protocol-Version, Mcp-Session-Id,
// Last-Event-Id, Mcp-Method, Mcp-Name, Mcp-Param-*) before calling client.Do, so they
// are already present here; config validation rejects those names so this type never
// has to arbitrate. Authorization is deliberately not reserved: the SDK only sets it
// when OAuthHandler is non-nil, which steiner never does.
type headerTransport struct {
	base    http.RoundTripper // nil means http.DefaultTransport
	headers map[string]string
}

// RoundTrip injects configured headers onto the request and delegates to the base transport.
// The caller's request is not modified; a clone is made if headers need to be set.
func (t *headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.base == nil {
		t.base = http.DefaultTransport
	}

	if len(t.headers) == 0 {
		return t.base.RoundTrip(req)
	}

	// Clone the request to avoid mutating the caller's request.
	req = req.Clone(req.Context())

	// Set headers in sorted order for deterministic behaviour.
	keys := make([]string, 0, len(t.headers))
	for k := range t.headers {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		req.Header.Set(k, t.headers[k])
	}

	return t.base.RoundTrip(req)
}

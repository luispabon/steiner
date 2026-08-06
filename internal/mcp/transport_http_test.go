package mcp

import (
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestNewHTTPTransport(t *testing.T) {
	tests := []struct {
		name    string
		spec    ServerSpec
		wantErr bool
		check   func(*testing.T, mcpsdk.Transport)
	}{
		{
			name: "valid URL with no headers",
			spec: ServerSpec{
				Name: "test",
				URL:  "http://localhost:8000/mcp",
			},
			wantErr: false,
			check: func(t *testing.T, tr mcpsdk.Transport) {
				st, ok := tr.(*mcpsdk.StreamableClientTransport)
				if !ok {
					t.Fatalf("transport type = %T, want *mcpsdk.StreamableClientTransport", tr)
				}
				if st.Endpoint != "http://localhost:8000/mcp" {
					t.Errorf("Endpoint = %q, want %q", st.Endpoint, "http://localhost:8000/mcp")
				}
				if st.HTTPClient == nil {
					t.Error("HTTPClient is nil, want non-nil")
				}
			},
		},
		{
			name: "valid URL with headers",
			spec: ServerSpec{
				Name:    "test",
				URL:     "https://example.com/api",
				Headers: map[string]string{"X-API-Key": "secret"},
			},
			wantErr: false,
			check: func(t *testing.T, tr mcpsdk.Transport) {
				st, ok := tr.(*mcpsdk.StreamableClientTransport)
				if !ok {
					t.Fatalf("transport type = %T, want *mcpsdk.StreamableClientTransport", tr)
				}
				if st.Endpoint != "https://example.com/api" {
					t.Errorf("Endpoint = %q, want %q", st.Endpoint, "https://example.com/api")
				}
				if st.HTTPClient == nil {
					t.Error("HTTPClient is nil, want non-nil")
				}
				// Verify the transport is a headerTransport.
				ht, ok := st.HTTPClient.Transport.(*headerTransport)
				if !ok {
					t.Fatalf("HTTPClient.Transport type = %T, want *headerTransport", st.HTTPClient.Transport)
				}
				if ht.headers["X-API-Key"] != "secret" {
					t.Errorf("headers[X-API-Key] = %q, want %q", ht.headers["X-API-Key"], "secret")
				}
			},
		},
		{
			name:    "empty URL",
			spec:    ServerSpec{Name: "test", URL: ""},
			wantErr: true,
		},
		{
			name:    "invalid URL",
			spec:    ServerSpec{Name: "test", URL: "not a valid url at all\n"},
			wantErr: true,
		},
		{
			name: "URL with multiple headers",
			spec: ServerSpec{
				Name: "test",
				URL:  "http://localhost:8000",
				Headers: map[string]string{
					"X-API-Key":     "secret",
					"Authorization": "Bearer token123",
				},
			},
			wantErr: false,
			check: func(t *testing.T, tr mcpsdk.Transport) {
				st, ok := tr.(*mcpsdk.StreamableClientTransport)
				if !ok {
					t.Fatalf("transport type = %T, want *mcpsdk.StreamableClientTransport", tr)
				}
				ht, ok := st.HTTPClient.Transport.(*headerTransport)
				if !ok {
					t.Fatalf("HTTPClient.Transport type = %T, want *headerTransport", st.HTTPClient.Transport)
				}
				if ht.headers["X-API-Key"] != "secret" {
					t.Errorf("headers[X-API-Key] = %q, want %q", ht.headers["X-API-Key"], "secret")
				}
				if ht.headers["Authorization"] != "Bearer token123" {
					t.Errorf("headers[Authorization] = %q, want %q", ht.headers["Authorization"], "Bearer token123")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr, err := newHTTPTransport(tt.spec)
			if (err != nil) != tt.wantErr {
				t.Errorf("newHTTPTransport error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err != nil {
				return
			}
			if tt.check != nil {
				tt.check(t, tr)
			}
		})
	}
}

func TestNewHTTPTransportHTTPClientNoTimeout(t *testing.T) {
	// Verify that the HTTP client's Timeout field is not set (zero value),
	// allowing long-lived SSE streams to persist.
	spec := ServerSpec{
		Name: "test",
		URL:  "http://localhost:8000/mcp",
	}

	tr, err := newHTTPTransport(spec)
	if err != nil {
		t.Fatalf("newHTTPTransport error: %v", err)
	}

	st, ok := tr.(*mcpsdk.StreamableClientTransport)
	if !ok {
		t.Fatalf("transport type = %T, want *mcpsdk.StreamableClientTransport", tr)
	}

	if st.HTTPClient.Timeout != 0 {
		t.Errorf("HTTPClient.Timeout = %v, want 0 (no timeout on SSE streams)", st.HTTPClient.Timeout)
	}
}

func TestNewHTTPTransportTransportFieldsAtDefaults(t *testing.T) {
	// Verify that MaxRetries, DisableStandaloneSSE, and OAuthHandler are at
	// their zero values, letting SDK defaults apply.
	spec := ServerSpec{
		Name: "test",
		URL:  "http://localhost:8000/mcp",
	}

	tr, err := newHTTPTransport(spec)
	if err != nil {
		t.Fatalf("newHTTPTransport error: %v", err)
	}

	st, ok := tr.(*mcpsdk.StreamableClientTransport)
	if !ok {
		t.Fatalf("transport type = %T, want *mcpsdk.StreamableClientTransport", tr)
	}

	if st.MaxRetries != 0 {
		t.Errorf("MaxRetries = %d, want 0 (SDK default)", st.MaxRetries)
	}
	if st.DisableStandaloneSSE != false {
		t.Errorf("DisableStandaloneSSE = %v, want false (SDK default)", st.DisableStandaloneSSE)
	}
	if st.OAuthHandler != nil {
		t.Errorf("OAuthHandler = %v, want nil", st.OAuthHandler)
	}
}

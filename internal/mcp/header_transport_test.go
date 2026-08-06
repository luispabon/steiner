package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHeaderTransport(t *testing.T) {
	tests := []struct {
		name               string
		configHeaders      map[string]string
		requestHeaders     map[string]string
		expectedHeaders    map[string]string
		notExpectedHeaders []string
	}{
		{
			name:            "configured headers arrive",
			configHeaders:   map[string]string{"X-Custom": "value1", "X-Another": "value2"},
			requestHeaders:  map[string]string{},
			expectedHeaders: map[string]string{"X-Custom": "value1", "X-Another": "value2"},
		},
		{
			name:               "empty headers map adds nothing",
			configHeaders:      map[string]string{},
			requestHeaders:     map[string]string{},
			expectedHeaders:    map[string]string{},
			notExpectedHeaders: []string{"X-Custom"},
		},
		{
			name:               "nil headers map adds nothing",
			configHeaders:      nil,
			requestHeaders:     map[string]string{},
			expectedHeaders:    map[string]string{},
			notExpectedHeaders: []string{"X-Custom"},
		},
		{
			name:               "configured header overwrites caller's header",
			configHeaders:      map[string]string{"X-Custom": "configured-value"},
			requestHeaders:     map[string]string{"X-Custom": "caller-value"},
			expectedHeaders:    map[string]string{"X-Custom": "configured-value"},
			notExpectedHeaders: []string{},
		},
		{
			name:               "multiple headers with overwrite",
			configHeaders:      map[string]string{"X-A": "new-a", "X-B": "new-b", "X-C": "value-c"},
			requestHeaders:     map[string]string{"X-A": "old-a", "X-B": "old-b"},
			expectedHeaders:    map[string]string{"X-A": "new-a", "X-B": "new-b", "X-C": "value-c"},
			notExpectedHeaders: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a test server that records the request headers.
			var recordedHeaders http.Header
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				recordedHeaders = r.Header
				w.WriteHeader(http.StatusOK)
			}))
			defer srv.Close()

			// Create a client with the header transport.
			client := &http.Client{
				Transport: &headerTransport{
					headers: tt.configHeaders,
				},
			}

			// Create a request with the caller's headers.
			req, err := http.NewRequestWithContext(context.Background(), "GET", srv.URL, nil)
			if err != nil {
				t.Fatalf("NewRequest failed: %v", err)
			}
			for k, v := range tt.requestHeaders {
				req.Header.Set(k, v)
			}

			// Record the caller's original headers before the request.
			originalHeaders := make(http.Header)
			for k, vv := range req.Header {
				originalHeaders[k] = append([]string{}, vv...)
			}

			// Execute the request.
			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("client.Do failed: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()

			// Assert expected headers reached the server.
			for k, v := range tt.expectedHeaders {
				got := recordedHeaders.Get(k)
				if got != v {
					t.Errorf("server header %q = %q, want %q", k, got, v)
				}
			}

			// Assert unexpected headers did not reach the server.
			for _, k := range tt.notExpectedHeaders {
				if got := recordedHeaders.Get(k); got != "" {
					t.Errorf("server header %q = %q, want absent", k, got)
				}
			}

			// Assert the caller's original request headers are unchanged.
			for k, vv := range originalHeaders {
				got := req.Header[k]
				if len(got) != len(vv) {
					t.Errorf("request.Header[%q] length = %d, want %d", k, len(got), len(vv))
					continue
				}
				for i, v := range vv {
					if got[i] != v {
						t.Errorf("request.Header[%q][%d] = %q, want %q", k, i, got[i], v)
					}
				}
			}

			// Assert that headers not originally in the request are still not present.
			for k := range tt.configHeaders {
				if _, wasInOriginal := originalHeaders[k]; !wasInOriginal {
					if _, nowInRequest := req.Header[k]; nowInRequest {
						t.Errorf("request.Header[%q] was modified; original request should not be mutated", k)
					}
				}
			}
		})
	}
}

func TestHeaderTransportBaseNilUseDefaultTransport(t *testing.T) {
	// Create a test server that records the request.
	var recordedHeaders http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recordedHeaders = r.Header
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// Create a client with the header transport and nil base (should use DefaultTransport).
	client := &http.Client{
		Transport: &headerTransport{
			base:    nil,
			headers: map[string]string{"X-Custom": "value"},
		},
	}

	// Execute the request.
	req, err := http.NewRequestWithContext(context.Background(), "GET", srv.URL, nil)
	if err != nil {
		t.Fatalf("NewRequest failed: %v", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("client.Do failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Assert the configured header reached the server via the default transport.
	if got := recordedHeaders.Get("X-Custom"); got != "value" {
		t.Errorf("server header X-Custom = %q, want %q", got, "value")
	}
}

func TestHeaderTransportRequestNotMutated(t *testing.T) {
	// This test explicitly verifies that request cloning works:
	// if the clone line is removed, this test should fail.

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := &http.Client{
		Transport: &headerTransport{
			headers: map[string]string{"X-Injected": "injected-value"},
		},
	}

	req, err := http.NewRequestWithContext(context.Background(), "GET", srv.URL, nil)
	if err != nil {
		t.Fatalf("NewRequest failed: %v", err)
	}

	// Verify no X-Injected header in the original request.
	if req.Header.Get("X-Injected") != "" {
		t.Fatalf("original request should not have X-Injected header")
	}

	// Execute the request.
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("client.Do failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// After RoundTrip returns, the original request must still not have the injected header.
	// This verifies that req.Clone was called and the original was not mutated.
	if got := req.Header.Get("X-Injected"); got != "" {
		t.Errorf("original request was mutated; X-Injected header = %q, want absent", got)
	}
}

package main

import (
	"crypto/tls"
	"crypto/x509"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// TestRuntimeHTTPClientAllowsParallelRequestsToOneHost proves the shared
// transport does not cap connections per host. The handler blocks until all
// requests have arrived, so a MaxConnsPerHost of 1 deadlocks until the bounded
// wait expires and answers 504.
func TestRuntimeHTTPClientAllowsParallelRequestsToOneHost(t *testing.T) {
	const want = 4
	const barrierWait = 5 * time.Second

	var (
		mu      sync.Mutex
		arrived int
		release = make(chan struct{})
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		arrived++
		if arrived == want {
			close(release)
		}
		mu.Unlock()
		select {
		case <-release:
			w.WriteHeader(http.StatusOK)
		case <-time.After(barrierWait):
			http.Error(w, "requests were serialized", http.StatusGatewayTimeout)
		}
	}))
	defer srv.Close()

	client := runtimeHTTPClient()
	defer client.CloseIdleConnections()

	var wg sync.WaitGroup
	errs := make([]error, want)
	statuses := make([]int, want)
	for i := 0; i < want; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			resp, err := client.Get(srv.URL)
			if err != nil {
				errs[i] = err
				return
			}
			defer resp.Body.Close()
			//nolint:errcheck // draining the body only enables connection reuse.
			_, _ = io.Copy(io.Discard, resp.Body)
			statuses[i] = resp.StatusCode
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("request %d failed: %v", i, err)
		}
	}
	for i, status := range statuses {
		if status != http.StatusOK {
			t.Errorf("request %d status = %d, want %d (requests were serialized)", i, status, http.StatusOK)
		}
	}
}

// TestRuntimeHTTPClientNegotiatesHTTP2 covers the runtime transport and a
// transport produced by Clone(), the shape providers use when config.timeout
// is set. RootCAs are attached to clones so the client built by
// runtimeHTTPClient is never mutated.
func TestRuntimeHTTPClientNegotiatesHTTP2(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	defer srv.Close()

	roots := x509.NewCertPool()
	roots.AddCert(srv.Certificate())
	trusted := func(base *http.Transport) *http.Transport {
		tr := base.Clone()
		tr.TLSClientConfig = &tls.Config{RootCAs: roots}
		return tr
	}

	cases := []struct {
		name      string
		transport func(base *http.Transport) *http.Transport
	}{
		{
			name:      "runtime transport",
			transport: trusted,
		},
		{
			name: "cloned transport",
			transport: func(base *http.Transport) *http.Transport {
				tr := trusted(base)
				// Mirrors provider newClient, which clears the header timeout
				// when a client-level timeout is configured.
				tr.ResponseHeaderTimeout = 0
				return tr
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			base := runtimeHTTPClient().Transport.(*http.Transport)
			tr := tc.transport(base)
			defer tr.CloseIdleConnections()
			client := &http.Client{Transport: tr, Timeout: 10 * time.Second}

			resp, err := client.Get(srv.URL)
			if err != nil {
				t.Fatalf("GET %s: %v", srv.URL, err)
			}
			defer resp.Body.Close()
			//nolint:errcheck // draining the body only enables connection reuse.
			_, _ = io.Copy(io.Discard, resp.Body)
			if resp.ProtoMajor != 2 {
				t.Errorf("proto = %s, want HTTP/2", resp.Proto)
			}
		})
	}
}

func TestRuntimeHTTPClientTransportSettings(t *testing.T) {
	client := runtimeHTTPClient()
	if client.Timeout != 0 {
		t.Errorf("client timeout = %v, want 0", client.Timeout)
	}
	tr, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport type = %T, want *http.Transport", client.Transport)
	}

	checks := []struct {
		name string
		got  any
		want any
	}{
		{"Proxy", tr.Proxy != nil, true},
		{"DialContext", tr.DialContext != nil, true},
		{"ForceAttemptHTTP2", tr.ForceAttemptHTTP2, true},
		{"MaxConnsPerHost", tr.MaxConnsPerHost, 0},
		{"MaxIdleConns", tr.MaxIdleConns, 100},
		{"MaxIdleConnsPerHost", tr.MaxIdleConnsPerHost, 16},
		{"IdleConnTimeout", tr.IdleConnTimeout, 90 * time.Second},
		{"TLSHandshakeTimeout", tr.TLSHandshakeTimeout, 10 * time.Second},
		{"ExpectContinueTimeout", tr.ExpectContinueTimeout, 1 * time.Second},
		{"ResponseHeaderTimeout", tr.ResponseHeaderTimeout, 30 * time.Second},
		{"TLSClientConfig nil", tr.TLSClientConfig == nil, true},
		{"HTTP2 config nil", tr.HTTP2 == nil, true},
	}
	for _, check := range checks {
		if check.got != check.want {
			t.Errorf("%s = %v, want %v", check.name, check.got, check.want)
		}
	}
}

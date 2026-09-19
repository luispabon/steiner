package mcp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestRedirectHeaders(t *testing.T) {
	var bSaw atomic.Int32
	b := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			bSaw.Add(1)
		}
	}))
	defer b.Close()

	var sameGot atomic.Value
	a := httptest.NewServer(nil)
	defer a.Close()
	a.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cross":
			http.Redirect(w, r, b.URL, http.StatusFound)
		case "/same":
			http.Redirect(w, r, "/final", http.StatusFound)
		case "/final":
			sameGot.Store(r.Header.Get("Authorization"))
		}
	})

	client := newHTTPClientForTest(t, a.URL)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, a.URL+"/cross", nil)
	if err != nil {
		t.Fatalf("NewRequestWithContext: %v", err)
	}
	resp, err := client.Do(req)
	if err == nil {
		_ = resp.Body.Close() // test cleanup
		t.Fatal("cross-origin redirect succeeded, want error")
	}
	if !strings.Contains(err.Error(), "cross-origin") {
		t.Errorf("error = %v, want cross-origin refusal", err)
	}
	if bSaw.Load() != 0 {
		t.Error("cross-origin server received the Authorization header")
	}

	req, err = http.NewRequestWithContext(t.Context(), http.MethodGet, a.URL+"/same", nil)
	if err != nil {
		t.Fatalf("NewRequestWithContext: %v", err)
	}
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("same-origin redirect: %v", err)
	}
	_ = resp.Body.Close() // test cleanup
	if got, _ := sameGot.Load().(string); got != "Bearer SECRET" {
		t.Errorf("same-origin Authorization = %q, want Bearer SECRET", got)
	}
}

func TestHeaderTransportSkipsOtherOrigin(t *testing.T) {
	var saw atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			saw.Add(1)
		}
	}))
	defer s.Close()
	c := &http.Client{Transport: &headerTransport{headers: map[string]string{"Authorization": "x"}, origin: "http://other.example:1"}}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, s.URL, nil)
	if err != nil {
		t.Fatalf("NewRequestWithContext: %v", err)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close() // test cleanup
	if saw.Load() != 0 {
		t.Error("header injected for non-matching origin")
	}
}

func newHTTPClientForTest(t *testing.T, url string) *http.Client {
	t.Helper()
	tr, err := newHTTPTransport(ServerSpec{Name: "s", URL: url, Headers: map[string]string{"Authorization": "Bearer SECRET"}})
	if err != nil {
		t.Fatalf("newHTTPTransport: %v", err)
	}
	return tr.(*mcpsdk.StreamableClientTransport).HTTPClient
}

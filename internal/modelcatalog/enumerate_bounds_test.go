package modelcatalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestRedirectDoesNotLeakCredentials(t *testing.T) {
	var gotB http.Header
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotB = r.Header.Clone()
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer b.Close()
	a := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, b.URL+"/v1/models", http.StatusFound)
	}))
	defer a.Close()

	ep := Endpoint{Alias: "x", BaseURL: a.URL + "/v1", APIKey: "sk-secret", Headers: map[string]string{"X-Custom-Secret": "hunter2", "x-api-key": "sk-secret"}}
	_, err := NewOpenAIEnumerator(nil).Enumerate(context.Background(), ep, EnumerationOptions{})
	if err == nil || !strings.Contains(err.Error(), "cross-origin") {
		t.Fatalf("Enumerate() error = %v, want cross-origin redirect refusal", err)
	}
	if gotB != nil {
		t.Fatalf("redirect target was contacted with headers %v", gotB)
	}
}

func TestSameOriginRedirectStillWorks(t *testing.T) {
	var hdr string
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/v1/models2", http.StatusFound)
	})
	mux.HandleFunc("/v1/models2", func(w http.ResponseWriter, r *http.Request) {
		hdr = r.Header.Get("X-Custom-Secret")
		_, _ = w.Write([]byte(`{"data":[{"id":"m1"}]}`))
	})
	s := httptest.NewServer(mux)
	defer s.Close()
	ep := Endpoint{Alias: "x", BaseURL: s.URL + "/v1", Headers: map[string]string{"X-Custom-Secret": "hunter2"}}
	res, err := NewOpenAIEnumerator(nil).Enumerate(context.Background(), ep, EnumerationOptions{})
	if err != nil {
		t.Fatalf("Enumerate() error = %v", err)
	}
	if len(res.Models) != 1 || hdr != "hunter2" {
		t.Fatalf("models=%d header=%q", len(res.Models), hdr)
	}
}

func bigBodyServer(status int, prefix, suffix string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(prefix))
		chunk := []byte(strings.Repeat("a", 1<<20))
		for i := 0; i < 22; i++ {
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
		_, _ = w.Write([]byte(suffix))
	}))
}

func TestOversizeResponsesRejected(t *testing.T) {
	tests := []struct {
		name string
		run  func(url string) error
	}{
		{"openai", func(u string) error {
			_, err := NewOpenAIEnumerator(nil).Enumerate(context.Background(), Endpoint{Alias: "x", BaseURL: u + "/v1"}, EnumerationOptions{})
			return err
		}},
		{"anthropic", func(u string) error {
			_, err := NewAnthropicEnumerator(nil).Enumerate(context.Background(), Endpoint{Alias: "x", BaseURL: u + "/v1", APIKey: "k"}, EnumerationOptions{})
			return err
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := bigBodyServer(http.StatusOK, `{"data":[{"id":"`, `"}]}`)
			defer srv.Close()
			if err := tt.run(srv.URL); err == nil {
				t.Fatal("error = nil, want oversize rejection")
			}
		})
	}
}

func TestAnthropicErrorBodyBounded(t *testing.T) {
	srv := bigBodyServer(http.StatusBadRequest, "limit exceeded ", "")
	defer srv.Close()
	_, _, _, err := doAnthropicRequest(http.DefaultClient, mustGET(t, srv.URL))
	if err == nil || !strings.Contains(err.Error(), "page size rejected") {
		t.Fatalf("error = %v", err)
	}
	if len(err.Error()) > 2048 {
		t.Fatalf("error length = %d, want bounded", len(err.Error()))
	}
}

func mustGET(t *testing.T, u string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		t.Fatal(err)
	}
	return req
}

func TestCacheDirsAre0700(t *testing.T) {
	dir := t.TempDir() + "/cache"
	c := &Cache{Dir: dir}
	release, err := c.lock("alias")
	if err != nil {
		t.Fatal(err)
	}
	release()
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Fatalf("dir perm = %o, want 700", got)
	}
}

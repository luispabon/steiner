package modelcatalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestAnthropicEnumeratorPaginationAndLimitFallback(t *testing.T) {
	page1 := readModelFixture(t, "anthropic_models_page1.json")
	page2 := readModelFixture(t, "anthropic_models_page2.json")
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.URL.RawQuery)
		if r.Header.Get("anthropic-version") != anthropicVersion || r.Header.Get("x-api-key") != "key" {
			t.Errorf("headers: version=%q key=%q", r.Header.Get("anthropic-version"), r.Header.Get("x-api-key"))
		}
		if r.URL.Query().Get("limit") == "1000" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"limit out of range"}`))
			return
		}
		if r.URL.Query().Get("after_id") == "" {
			_, _ = w.Write(page1)
		} else if r.URL.Query().Get("after_id") == "page-one" {
			_, _ = w.Write(page2)
		} else {
			t.Errorf("unexpected cursor: %q", r.URL.Query().Get("after_id"))
		}
	}))
	defer server.Close()
	result, err := NewAnthropicEnumerator(server.Client()).Enumerate(context.Background(), Endpoint{Alias: "anthropic", Type: "anthropic", BaseURL: server.URL, APIKey: "key"}, EnumerationOptions{})
	if err != nil {
		t.Fatalf("Enumerate: %v", err)
	}
	if len(requests) != 3 || requests[0] != "limit=1000" || requests[1] != "limit=20" || requests[2] != "after_id=page-one\u0026limit=20" {
		t.Fatalf("requests: %v", requests)
	}
	if len(result.Models) != 2 || result.Models[0].SupportedEfforts[0] != "low" || result.Models[1].DisplayName != "claude-3-opus" {
		t.Fatalf("models: %+v", result.Models)
	}
}

func TestAnthropicBearerHeaderOverride(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer token" || r.Header.Get("x-api-key") != "" {
			t.Errorf("auth headers: authorization=%q x-api-key=%q", r.Header.Get("Authorization"), r.Header.Get("x-api-key"))
		}
		_, _ = w.Write([]byte(`{"data":[],"has_more":false}`))
	}))
	defer server.Close()
	_, err := NewAnthropicEnumerator(server.Client()).Enumerate(context.Background(), Endpoint{Type: "anthropic", BaseURL: server.URL, APIKey: "Bearer token", Headers: map[string]string{"Authorization": "Bearer token"}}, EnumerationOptions{})
	if err != nil {
		t.Fatalf("Enumerate: %v", err)
	}
}

func TestAnthropicPageURLPreservesQuery(t *testing.T) {
	got, err := anthropicPageURL("https://example.com/v1/models?foo=bar", 20, "cursor")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(got)
	if u.Query().Get("foo") != "bar" || u.Query().Get("after_id") != "cursor" {
		t.Fatalf("URL: %s", got)
	}
}

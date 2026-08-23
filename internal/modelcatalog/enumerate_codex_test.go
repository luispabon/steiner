package modelcatalog

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCodexEnumerator(t *testing.T) {
	fixture := readModelFixture(t, "codex_models.json")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/backend-api/codex/models" || r.URL.Query().Get("client_version") != "1.2.3" {
			t.Errorf("request: %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		if r.Header.Get("Authorization") != "Bearer access" || r.Header.Get("ChatGPT-Account-ID") != "account" || r.Header.Get("OAI-Product-Sku") != "codex" {
			t.Errorf("headers: authorization=%q account=%q sku=%q", r.Header.Get("Authorization"), r.Header.Get("ChatGPT-Account-ID"), r.Header.Get("OAI-Product-Sku"))
		}
		if r.Header.Get("If-None-Match") != "old" {
			t.Errorf("if-none-match: %q", r.Header.Get("If-None-Match"))
		}
		w.Header().Set("ETag", "new")
		_, _ = w.Write(fixture)
	}))
	defer server.Close()
	result, err := NewCodexEnumerator(server.Client(), "1.2.3", func(context.Context) (string, string, error) { return "access", "account", nil }).Enumerate(context.Background(), Endpoint{Alias: "codex", Type: "codex", BaseURL: server.URL + "/backend-api/codex"}, EnumerationOptions{ETag: "old"})
	if err != nil {
		t.Fatalf("Enumerate: %v", err)
	}
	if result.ETag != "new" || len(result.Models) != 1 || result.Models[0].ID != "gpt-5-codex" || result.Models[0].Priority != 7 {
		t.Fatalf("result: %+v", result)
	}
}

func TestCodexEnumeratorNotModified(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", "fresh")
		w.WriteHeader(http.StatusNotModified)
	}))
	defer server.Close()
	result, err := NewCodexEnumerator(server.Client(), "v", func(context.Context) (string, string, error) { return "access", "account", nil }).Enumerate(context.Background(), Endpoint{BaseURL: server.URL}, EnumerationOptions{ETag: "old"})
	if err != nil || !result.NotModified || result.ETag != "fresh" || result.Models != nil {
		t.Fatalf("result: %+v err=%v", result, err)
	}
}

func TestCodexEnumeratorCredentialErrors(t *testing.T) {
	_, err := NewCodexEnumerator(nil, "v", func(context.Context) (string, string, error) { return "", "", errors.New("token store failed") }).Enumerate(context.Background(), Endpoint{BaseURL: "https://example.com"}, EnumerationOptions{})
	if err == nil {
		t.Fatal("Enumerate: expected credentials error")
	}
	_, err = NewCodexEnumerator(nil, "v", func(context.Context) (string, string, error) { return "", "", nil }).Enumerate(context.Background(), Endpoint{BaseURL: "https://example.com"}, EnumerationOptions{})
	if err == nil {
		t.Fatal("Enumerate: expected missing credentials error")
	}
}

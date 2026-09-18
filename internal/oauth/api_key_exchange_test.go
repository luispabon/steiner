package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestExchangeOpenAIAPIKey(t *testing.T) {
	var gotForm url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm() error = %v", err)
		}
		gotForm = r.PostForm
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]string{"access_token": "sk-codex"}); err != nil {
			t.Fatalf("Encode() error = %v", err)
		}
	}))
	defer server.Close()

	got, err := ExchangeOpenAIAPIKey(context.Background(), server.URL, "client-id", "id-token", server.Client())
	if err != nil {
		t.Fatalf("ExchangeOpenAIAPIKey() error = %v", err)
	}
	if got != "sk-codex" {
		t.Fatalf("ExchangeOpenAIAPIKey() = %q, want sk-codex", got)
	}

	wants := map[string]string{
		"grant_type":         "urn:ietf:params:oauth:grant-type:token-exchange",
		"client_id":          "client-id",
		"requested_token":    "openai-api-key",
		"subject_token":      "id-token",
		"subject_token_type": "urn:ietf:params:oauth:token-type:id_token",
	}
	for key, want := range wants {
		if got := gotForm.Get(key); got != want {
			t.Fatalf("form %s = %q, want %q", key, got, want)
		}
	}
}

func TestExchangeOpenAIAPIKeyReturnsErrorBodyReadError(t *testing.T) {
	readErr := errors.New("body read failed")
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusBadRequest,
			Status:     "400 Bad Request",
			Body:       io.NopCloser(errorReader{err: readErr}),
		}, nil
	})}

	_, err := ExchangeOpenAIAPIKey(context.Background(), "http://example.test/token", "client-id", "id-token", client)
	if !errors.Is(err, readErr) {
		t.Fatalf("error = %v, want body read error", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

type errorReader struct{ err error }

func (r errorReader) Read([]byte) (int, error) { return 0, r.err }

func TestExchangeOpenAIAPIKeyRequiresIDToken(t *testing.T) {
	_, err := ExchangeOpenAIAPIKey(context.Background(), "http://example.test/token", "client-id", "", nil)
	if err == nil {
		t.Fatal("ExchangeOpenAIAPIKey() error = nil, want error")
	}
}

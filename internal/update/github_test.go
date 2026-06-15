package update

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFetchLatestRelease_HappyPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := release{
			TagName: "v1.2.3",
			Assets: []asset{
				{Name: "steiner-linux-amd64", DownloadURL: "https://example.com/linux"},
				{Name: "steiner-darwin-amd64", DownloadURL: "https://example.com/darwin"},
			},
		}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("encode response: %v", err)
		}
	}))
	defer server.Close()

	defer saveHTTPClient()()
	httpClient = newTestClient(server.URL)

	rel, err := fetchLatestRelease(context.Background(), "owner", "repo", "")
	if err != nil {
		t.Fatalf("fetchLatestRelease: %v", err)
	}
	if rel.TagName != "v1.2.3" {
		t.Errorf("TagName = %q, want %q", rel.TagName, "v1.2.3")
	}
	if len(rel.Assets) != 2 {
		t.Fatalf("len(Assets) = %d, want 2", len(rel.Assets))
	}
	if rel.Assets[0].Name != "steiner-linux-amd64" {
		t.Errorf("Asset[0].Name = %q, want %q", rel.Assets[0].Name, "steiner-linux-amd64")
	}
	if rel.Assets[0].DownloadURL != "https://example.com/linux" {
		t.Errorf("Asset[0].DownloadURL = %q, want %q", rel.Assets[0].DownloadURL, "https://example.com/linux")
	}
	if rel.Assets[1].Name != "steiner-darwin-amd64" {
		t.Errorf("Asset[1].Name = %q, want %q", rel.Assets[1].Name, "steiner-darwin-amd64")
	}
}

func TestFetchLatestRelease_Non200(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
	}{
		{"404", http.StatusNotFound},
		{"500", http.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.statusCode)
			}))
			defer server.Close()

			defer saveHTTPClient()()
			httpClient = newTestClient(server.URL)

			_, err := fetchLatestRelease(context.Background(), "owner", "repo", "")
			if err == nil {
				t.Fatal("fetchLatestRelease: expected error, got nil")
			}
			want := fmt.Sprintf("GitHub API returned %d", tt.statusCode)
			if err.Error() != want {
				t.Errorf("fetchLatestRelease error = %q, want %q", err.Error(), want)
			}
		})
	}
}

func TestFetchLatestRelease_WithToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer token123" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		resp := release{TagName: "v1.0.0"}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("encode response: %v", err)
		}
	}))
	defer server.Close()

	defer saveHTTPClient()()
	httpClient = newTestClient(server.URL)

	rel, err := fetchLatestRelease(context.Background(), "owner", "repo", "token123")
	if err != nil {
		t.Fatalf("fetchLatestRelease with token: %v", err)
	}
	if rel.TagName != "v1.0.0" {
		t.Errorf("TagName = %q, want %q", rel.TagName, "v1.0.0")
	}
}

func TestFetchLatestRelease_MalformedJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{invalid json}`))
	}))
	defer server.Close()

	defer saveHTTPClient()()
	httpClient = newTestClient(server.URL)

	_, err := fetchLatestRelease(context.Background(), "owner", "repo", "")
	if err == nil {
		t.Fatal("fetchLatestRelease: expected error for malformed JSON, got nil")
	}
	if err.Error() != "decode release: invalid character 'i' looking for beginning of object key string" {
		t.Errorf("fetchLatestRelease error = %q, want decode error", err.Error())
	}
}

func TestFetchReleaseByTag_HappyPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/owner/repo/releases/tags/dev" {
			t.Errorf("unexpected path: %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		resp := release{
			TagName: "dev",
			Assets: []asset{
				{Name: "steiner-linux-amd64", DownloadURL: "https://example.com/linux"},
			},
		}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("encode response: %v", err)
		}
	}))
	defer server.Close()

	defer saveHTTPClient()()
	httpClient = newTestClient(server.URL)

	rel, err := fetchReleaseByTag(context.Background(), "owner", "repo", "dev", "")
	if err != nil {
		t.Fatalf("fetchReleaseByTag: %v", err)
	}
	if rel.TagName != "dev" {
		t.Errorf("TagName = %q, want %q", rel.TagName, "dev")
	}
	if len(rel.Assets) != 1 {
		t.Fatalf("len(Assets) = %d, want 1", len(rel.Assets))
	}
}

func TestFetchReleaseByTag_Non200(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	defer saveHTTPClient()()
	httpClient = newTestClient(server.URL)

	_, err := fetchReleaseByTag(context.Background(), "owner", "repo", "dev", "")
	if err == nil {
		t.Fatal("fetchReleaseByTag: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "GitHub API returned 404") {
		t.Errorf("fetchReleaseByTag error = %q, want GitHub API 404", err.Error())
	}
}

package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/quota"
)

// apiKeyQuotaServer serves one response and checks the request method,
// Authorization header, and path.
func apiKeyQuotaServer(t *testing.T, wantAuth, wantPath string, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != wantAuth {
			t.Errorf("Authorization = %q, want %q", got, wantAuth)
		}
		if r.URL.Path != wantPath {
			t.Errorf("path = %q, want %q", r.URL.Path, wantPath)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// apiKeyQuotaFetch builds a fetcher for cfg and returns one successful fetch.
func apiKeyQuotaFetch(t *testing.T, cfg APIKeyQuotaConfig, client *http.Client) quota.Snapshot {
	t.Helper()
	f, err := NewAPIKeyQuotaFetcher(cfg, client)
	if err != nil {
		t.Fatalf("NewAPIKeyQuotaFetcher: %v", err)
	}
	snap, err := f.Fetch(context.Background(), quota.FetchOptions{})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	return snap
}

// apiKeyQuotaFixture returns the contents of testdata/quota/name.
func apiKeyQuotaFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/quota/" + name)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return string(b)
}

// apiKeyQuotaAssertJSON compares got and want by their JSON encoding. It keeps
// nil and empty slices distinct and compares times by their RFC 3339 value.
func apiKeyQuotaAssertJSON(t *testing.T, name string, got, want any) {
	t.Helper()
	g, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal %s: %v", name, err)
	}
	w, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal want %s: %v", name, err)
	}
	if string(g) != string(w) {
		t.Errorf("%s = %s, want %s", name, g, w)
	}
}

func apiKeyQuotaTime(t *testing.T, s string) *time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parse time %q: %v", s, err)
	}
	return &parsed
}

func apiKeyQuotaPtr[T any](v T) *T {
	return &v
}

func TestNewAPIKeyQuotaFetcherRejectsUnsupportedProvider(t *testing.T) {
	for _, providerType := range []string{"", "openai", "anthropic", "claude_subscription", "OpenRouter"} {
		t.Run(providerType, func(t *testing.T) {
			if _, err := NewAPIKeyQuotaFetcher(APIKeyQuotaConfig{ProviderType: providerType, BaseURL: "http://localhost", APIKey: "sk-test"}, nil); err == nil {
				t.Errorf("NewAPIKeyQuotaFetcher(%q) error = nil, want error", providerType)
			}
		})
	}
}

func TestAPIKeyQuotaFetchOpenCodeGoFixture(t *testing.T) {
	srv := apiKeyQuotaServer(t, "Bearer sk-test", "/zen/go/v1/usage", http.StatusOK, apiKeyQuotaFixture(t, "opencode_go_usage.json"))
	snap := apiKeyQuotaFetch(t, APIKeyQuotaConfig{ProviderType: "opencode_go", BaseURL: " " + srv.URL + "/zen/go/v1/ ", APIKey: "sk-test"}, srv.Client())

	if want := quota.APIKeyAccountKey("opencode_go", srv.URL+"/zen/go/v1", "sk-test"); snap.AccountKey != want {
		t.Errorf("AccountKey = %q, want %q", snap.AccountKey, want)
	}
	if snap.ProviderType != "opencode_go" || snap.Source != quota.SourceFetch || snap.FetchedAt.IsZero() {
		t.Errorf("snapshot = {ProviderType:%q Source:%q FetchedAt zero:%t}, want opencode_go, fetch, set", snap.ProviderType, snap.Source, snap.FetchedAt.IsZero())
	}
	apiKeyQuotaAssertJSON(t, "Windows", snap.Windows, []quota.Window{
		{Label: "5h", Duration: 5 * time.Hour, UsedPercent: 1, ResetsAt: *apiKeyQuotaTime(t, "2026-10-14T15:14:56Z"), Status: "ok"},
		{Label: "weekly", Duration: 7 * 24 * time.Hour, UsedPercent: 6, ResetsAt: *apiKeyQuotaTime(t, "2026-10-19T00:00:00Z"), Status: "ok"},
		{Label: "monthly", Duration: 30 * 24 * time.Hour, UsedPercent: 23, ResetsAt: *apiKeyQuotaTime(t, "2026-10-27T19:40:38Z"), Status: "ok"},
	})
	apiKeyQuotaAssertJSON(t, "Budgets", snap.Budgets, []quota.Budget(nil))
}

func TestAPIKeyQuotaFetchOpenRouterFixture(t *testing.T) {
	srv := apiKeyQuotaServer(t, "Bearer sk-test", "/api/v1/key", http.StatusOK, apiKeyQuotaFixture(t, "openrouter_key.json"))
	snap := apiKeyQuotaFetch(t, APIKeyQuotaConfig{ProviderType: "openrouter", BaseURL: srv.URL + "/api/v1", APIKey: "sk-test"}, srv.Client())

	apiKeyQuotaAssertJSON(t, "Budgets", snap.Budgets, []quota.Budget{
		{Label: "budget", Used: 12.5, Limit: apiKeyQuotaPtr(50.0), Currency: "USD", Period: "monthly", ResetsAt: apiKeyQuotaTime(t, "2026-11-01T00:00:00Z")},
		{Label: "budget", Used: 0.5, Currency: "USD", Period: "daily"},
		{Label: "today", Used: 1.25, Currency: "USD", Period: "day"},
		{Label: "week", Used: 4.5, Currency: "USD", Period: "week"},
		{Label: "month", Used: 12.5, Currency: "USD", Period: "month"},
	})
	apiKeyQuotaAssertJSON(t, "Windows", snap.Windows, []quota.Window(nil))
}

func TestAPIKeyQuotaOpenRouterBudgetShapes(t *testing.T) {
	tests := []struct {
		name string
		body string
		want []quota.Budget
	}{
		{
			name: "limit fallback without budgets",
			body: `{"data":{"usage":3,"limit":10,"limit_reset":"monthly","usage_daily":null}}`,
			want: []quota.Budget{{Label: "limit", Used: 3, Limit: apiKeyQuotaPtr(10.0), Currency: "USD", Period: "monthly"}},
		},
		{
			name: "null limit keeps spend only",
			body: `{"data":{"usage":3,"limit":null,"limit_reset":null,"usage_daily":1}}`,
			want: []quota.Budget{{Label: "today", Used: 1, Currency: "USD", Period: "day"}},
		},
		{
			name: "budgets suppress limit fallback",
			body: `{"data":{"usage":3,"limit":10,"limit_reset":"monthly","budgets":[{"spend_usd":2,"limit_usd":5,"reset_interval":"weekly","resets_at":null}]}}`,
			want: []quota.Budget{{Label: "budget", Used: 2, Limit: apiKeyQuotaPtr(5.0), Currency: "USD", Period: "weekly"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := apiKeyQuotaServer(t, "Bearer sk-test", "/api/v1/key", http.StatusOK, tt.body)
			snap := apiKeyQuotaFetch(t, APIKeyQuotaConfig{ProviderType: "openrouter", BaseURL: srv.URL + "/api/v1", APIKey: "sk-test"}, srv.Client())
			apiKeyQuotaAssertJSON(t, "Budgets", snap.Budgets, tt.want)
		})
	}
}

func TestAPIKeyQuotaFetchLiteLLMFixture(t *testing.T) {
	srv := apiKeyQuotaServer(t, "Bearer sk-test", "/key/info", http.StatusOK, apiKeyQuotaFixture(t, "litellm_key_info.json"))
	snap := apiKeyQuotaFetch(t, APIKeyQuotaConfig{ProviderType: "litellm", BaseURL: srv.URL + "/v1/", APIKey: "sk-test"}, srv.Client())

	apiKeyQuotaAssertJSON(t, "Budgets", snap.Budgets, []quota.Budget{
		{Label: "budget", Used: 3.25, Limit: apiKeyQuotaPtr(10.0), Currency: "USD", Period: "1d", ResetsAt: apiKeyQuotaTime(t, "2026-10-15T00:00:00Z")},
	})
}

func TestAPIKeyQuotaLiteLLMSpendOnly(t *testing.T) {
	body := `{"info":{"spend":2,"max_budget":null,"budget_duration":null,"budget_reset_at":null}}`
	srv := apiKeyQuotaServer(t, "Bearer sk-test", "/key/info", http.StatusOK, body)
	snap := apiKeyQuotaFetch(t, APIKeyQuotaConfig{ProviderType: "litellm", BaseURL: srv.URL + "/v1", APIKey: "sk-test"}, srv.Client())

	apiKeyQuotaAssertJSON(t, "Budgets", snap.Budgets, []quota.Budget{{Label: "spend", Used: 2, Currency: "USD"}})
}

func TestAPIKeyQuotaFetchErrors(t *testing.T) {
	cases := []struct {
		name         string
		providerType string
		baseURL      string
		wantPath     string
		label        string
	}{
		{"opencode_go", "opencode_go", "/zen/go/v1", "/zen/go/v1/usage", "usage"},
		{"openrouter", "openrouter", "/api/v1", "/api/v1/key", "key"},
		{"litellm", "litellm", "/v1", "/key/info", "key/info"},
	}
	for _, tc := range cases {
		for _, failure := range []struct {
			name   string
			status int
			body   string
			want   string
		}{
			{"forbidden", http.StatusForbidden, `{"error":"denied"}`, tc.label + ": 403"},
			{"malformed", http.StatusOK, "{", tc.label + ": bad response"},
		} {
			t.Run(tc.name+"/"+failure.name, func(t *testing.T) {
				srv := apiKeyQuotaServer(t, "Bearer sk-test", tc.wantPath, failure.status, failure.body)
				f, err := NewAPIKeyQuotaFetcher(APIKeyQuotaConfig{ProviderType: tc.providerType, BaseURL: srv.URL + tc.baseURL, APIKey: "sk-test"}, srv.Client())
				if err != nil {
					t.Fatalf("NewAPIKeyQuotaFetcher: %v", err)
				}
				_, err = f.Fetch(context.Background(), quota.FetchOptions{})
				if err == nil {
					t.Fatalf("Fetch error = nil, want %q", failure.want)
				}
				if err.Error() != failure.want {
					t.Errorf("Fetch error = %q, want %q", err.Error(), failure.want)
				}
				if strings.Contains(err.Error(), "sk-test") {
					t.Errorf("Fetch error %q contains the API key", err.Error())
				}
			})
		}
	}
}

func TestAPIKeyQuotaAccountKeyFollowsKeyWithoutRawKey(t *testing.T) {
	body := apiKeyQuotaFixture(t, "litellm_key_info.json")
	first := apiKeyQuotaServer(t, "Bearer sk-first-secret", "/key/info", http.StatusOK, body)
	second := apiKeyQuotaServer(t, "Bearer sk-second-secret", "/key/info", http.StatusOK, body)

	gotFirst := apiKeyQuotaFetch(t, APIKeyQuotaConfig{ProviderType: "litellm", BaseURL: first.URL + "/v1", APIKey: "sk-first-secret"}, first.Client())
	gotSecond := apiKeyQuotaFetch(t, APIKeyQuotaConfig{ProviderType: "litellm", BaseURL: second.URL + "/v1", APIKey: "sk-second-secret"}, second.Client())

	if gotFirst.AccountKey == gotSecond.AccountKey {
		t.Errorf("AccountKey is the same for different API keys: %q", gotFirst.AccountKey)
	}
	if want := quota.APIKeyAccountKey("litellm", first.URL+"/v1", "sk-first-secret"); gotFirst.AccountKey != want {
		t.Errorf("AccountKey = %q, want %q", gotFirst.AccountKey, want)
	}
	for _, snap := range []quota.Snapshot{gotFirst, gotSecond} {
		encoded, err := json.Marshal(snap)
		if err != nil {
			t.Fatalf("marshal snapshot: %v", err)
		}
		for _, raw := range []string{"sk-first-secret", "sk-second-secret"} {
			if strings.Contains(string(encoded), raw) || strings.Contains(snap.AccountKey, raw) {
				t.Errorf("snapshot leaks raw key %q: account key %q", raw, snap.AccountKey)
			}
		}
	}
}

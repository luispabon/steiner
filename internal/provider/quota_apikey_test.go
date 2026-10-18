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

// apiKeyQuotaTryFetch builds a fetcher for cfg and fetches once.
func apiKeyQuotaTryFetch(t *testing.T, cfg APIKeyQuotaConfig, client *http.Client) (quota.Snapshot, error) {
	t.Helper()
	f, err := NewAPIKeyQuotaFetcher(cfg, client)
	if err != nil {
		t.Fatalf("NewAPIKeyQuotaFetcher: %v", err)
	}
	return f.Fetch(context.Background(), quota.FetchOptions{})
}

// apiKeyQuotaFetch is apiKeyQuotaTryFetch that fails the test on error.
func apiKeyQuotaFetch(t *testing.T, cfg APIKeyQuotaConfig, client *http.Client) quota.Snapshot {
	t.Helper()
	snap, err := apiKeyQuotaTryFetch(t, cfg, client)
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

const (
	apiKeyQuotaWeeklyJSON  = `{"status":"ok","percent":6,"resetsAt":"2026-10-19T00:00:00.000Z"}`
	apiKeyQuotaMonthlyJSON = `{"status":"ok","percent":23,"resetsAt":"2026-10-27T19:40:38.000Z"}`
)

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
	snap := apiKeyQuotaFetch(t, APIKeyQuotaConfig{ProviderType: "opencode_go", BaseURL: " " + srv.URL + "/zen/go/v1/ ", APIKey: "sk-test", AccountKey: "provider:main"}, srv.Client())

	if snap.AccountKey != "provider:main" {
		t.Errorf("AccountKey = %q, want provider:main", snap.AccountKey)
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

func TestAPIKeyQuotaOpenCodeGoWindowShapes(t *testing.T) {
	weekly := quota.Window{Label: "weekly", Duration: 7 * 24 * time.Hour, UsedPercent: 6, Status: "ok", ResetsAt: *apiKeyQuotaTime(t, "2026-10-19T00:00:00Z")}
	monthly := quota.Window{Label: "monthly", Duration: 30 * 24 * time.Hour, UsedPercent: 23, Status: "ok", ResetsAt: *apiKeyQuotaTime(t, "2026-10-27T19:40:38Z")}
	tests := []struct {
		name    string
		body    string
		want    []quota.Window
		wantErr string
	}{
		{
			name: "null rolling is skipped",
			body: `{"usage":{"rolling":null,"weekly":` + apiKeyQuotaWeeklyJSON + `,"monthly":` + apiKeyQuotaMonthlyJSON + `}}`,
			want: []quota.Window{weekly, monthly},
		},
		{
			name: "missing rolling and weekly are skipped",
			body: `{"usage":{"monthly":` + apiKeyQuotaMonthlyJSON + `}}`,
			want: []quota.Window{monthly},
		},
		{
			name: "empty usage object yields no windows",
			body: `{"usage":{}}`,
			want: []quota.Window{},
		},
		{
			name: "absent resetsAt keeps the window with a zero reset",
			body: `{"usage":{"rolling":{"status":"ok","percent":1}}}`,
			want: []quota.Window{{Label: "5h", Duration: 5 * time.Hour, UsedPercent: 1, Status: "ok"}},
		},
		{
			name:    "malformed resetsAt is a bad response",
			body:    `{"usage":{"rolling":{"status":"ok","percent":1,"resetsAt":"soon"}}}`,
			wantErr: "usage: bad response",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := apiKeyQuotaServer(t, "Bearer sk-test", "/zen/go/v1/usage", http.StatusOK, tt.body)
			cfg := APIKeyQuotaConfig{ProviderType: "opencode_go", BaseURL: srv.URL + "/zen/go/v1", APIKey: "sk-test"}
			snap, err := apiKeyQuotaTryFetch(t, cfg, srv.Client())
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Errorf("Fetch error = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Fetch: %v", err)
			}
			apiKeyQuotaAssertJSON(t, "Windows", snap.Windows, tt.want)
		})
	}
}

func TestAPIKeyQuotaFetchOpenRouterFixture(t *testing.T) {
	srv := apiKeyQuotaServer(t, "Bearer sk-test", "/api/v1/key", http.StatusOK, apiKeyQuotaFixture(t, "openrouter_key.json"))
	snap := apiKeyQuotaFetch(t, APIKeyQuotaConfig{ProviderType: "openrouter", BaseURL: srv.URL + "/api/v1", APIKey: "sk-test"}, srv.Client())

	apiKeyQuotaAssertJSON(t, "Budgets", snap.Budgets, []quota.Budget{
		{Label: "budget", Used: 12.5, Limit: apiKeyQuotaPtr(50.0), Currency: "USD", Period: "monthly", ResetsAt: apiKeyQuotaTime(t, "2026-11-01T00:00:00Z")},
		{Label: "budget", Used: 0.5, Currency: "USD", Period: "daily"},
		{Label: "today", Used: 1.25, Currency: "USD", Period: "daily"},
		{Label: "week", Used: 4.5, Currency: "USD", Period: "weekly"},
		{Label: "month", Used: 12.5, Currency: "USD", Period: "monthly"},
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
			want: []quota.Budget{{Label: "today", Used: 1, Currency: "USD", Period: "daily"}},
		},
		{
			name: "budgets suppress limit fallback",
			body: `{"data":{"usage":3,"limit":10,"limit_reset":"monthly","budgets":[{"spend_usd":2,"limit_usd":5,"reset_interval":"weekly","resets_at":null}]}}`,
			want: []quota.Budget{{Label: "budget", Used: 2, Limit: apiKeyQuotaPtr(5.0), Currency: "USD", Period: "weekly"}},
		},
		{
			name: "malformed budget resets_at keeps the budget with a nil reset",
			body: `{"data":{"usage":0,"budgets":[{"spend_usd":2,"limit_usd":5,"reset_interval":"monthly","resets_at":"next month"}]}}`,
			want: []quota.Budget{{Label: "budget", Used: 2, Limit: apiKeyQuotaPtr(5.0), Currency: "USD", Period: "monthly"}},
		},
		{
			name: "absent budget resets_at keeps the budget with a nil reset",
			body: `{"data":{"usage":0,"budgets":[{"spend_usd":2,"limit_usd":5,"reset_interval":"monthly"}]}}`,
			want: []quota.Budget{{Label: "budget", Used: 2, Limit: apiKeyQuotaPtr(5.0), Currency: "USD", Period: "monthly"}},
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

func TestAPIKeyQuotaLiteLLMBudgetShapes(t *testing.T) {
	tests := []struct {
		name string
		body string
		want []quota.Budget
	}{
		{
			name: "spend only",
			body: `{"info":{"spend":2,"max_budget":null,"budget_duration":null,"budget_reset_at":null}}`,
			want: []quota.Budget{{Label: "spend", Used: 2, Currency: "USD"}},
		},
		{
			name: "malformed budget_reset_at keeps the budget with a nil reset",
			body: `{"info":{"spend":1,"max_budget":4,"budget_duration":"1d","budget_reset_at":"tomorrow"}}`,
			want: []quota.Budget{{Label: "budget", Used: 1, Limit: apiKeyQuotaPtr(4.0), Currency: "USD", Period: "1d"}},
		},
		{
			name: "absent budget_reset_at keeps the budget with a nil reset",
			body: `{"info":{"spend":1,"max_budget":4,"budget_duration":"1d"}}`,
			want: []quota.Budget{{Label: "budget", Used: 1, Limit: apiKeyQuotaPtr(4.0), Currency: "USD", Period: "1d"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := apiKeyQuotaServer(t, "Bearer sk-test", "/key/info", http.StatusOK, tt.body)
			snap := apiKeyQuotaFetch(t, APIKeyQuotaConfig{ProviderType: "litellm", BaseURL: srv.URL + "/v1", APIKey: "sk-test"}, srv.Client())
			apiKeyQuotaAssertJSON(t, "Budgets", snap.Budgets, tt.want)
		})
	}
}

func TestAPIKeyQuotaMissingTopLevelIsBadResponse(t *testing.T) {
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
		for _, body := range []string{`{}`, `null`} {
			t.Run(tc.name+"/"+body, func(t *testing.T) {
				srv := apiKeyQuotaServer(t, "Bearer sk-test", tc.wantPath, http.StatusOK, body)
				cfg := APIKeyQuotaConfig{ProviderType: tc.providerType, BaseURL: srv.URL + tc.baseURL, APIKey: "sk-test"}
				_, err := apiKeyQuotaTryFetch(t, cfg, srv.Client())
				if want := tc.label + ": bad response"; err == nil || err.Error() != want {
					t.Errorf("Fetch error = %v, want %q", err, want)
				}
			})
		}
	}
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
				_, err := apiKeyQuotaTryFetch(t, APIKeyQuotaConfig{ProviderType: tc.providerType, BaseURL: srv.URL + tc.baseURL, APIKey: "sk-test"}, srv.Client())
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

func TestAPIKeyQuotaSnapshotUsesConfiguredAccountKey(t *testing.T) {
	body := apiKeyQuotaFixture(t, "litellm_key_info.json")
	srv := apiKeyQuotaServer(t, "Bearer sk-test", "/key/info", http.StatusOK, body)

	for _, base := range []string{
		srv.URL + "/v1",
		" " + srv.URL + "/v1 ",
		srv.URL + "/v1/",
		" " + srv.URL + "/v1/ ",
	} {
		snap := apiKeyQuotaFetch(t, APIKeyQuotaConfig{ProviderType: "litellm", BaseURL: base, APIKey: "sk-test", AccountKey: "provider:litellm"}, srv.Client())
		if snap.AccountKey != "provider:litellm" {
			t.Errorf("base %q: AccountKey = %q, want provider:litellm", base, snap.AccountKey)
		}
	}
}

func TestAPIKeyQuotaAccountKeyFollowsKeyWithoutRawKey(t *testing.T) {
	body := apiKeyQuotaFixture(t, "litellm_key_info.json")
	first := apiKeyQuotaServer(t, "Bearer sk-first-secret", "/key/info", http.StatusOK, body)
	second := apiKeyQuotaServer(t, "Bearer sk-second-secret", "/key/info", http.StatusOK, body)

	gotFirst := apiKeyQuotaFetch(t, APIKeyQuotaConfig{ProviderType: "litellm", BaseURL: first.URL + "/v1", APIKey: "sk-first-secret", AccountKey: "provider:litellm"}, first.Client())
	gotSecond := apiKeyQuotaFetch(t, APIKeyQuotaConfig{ProviderType: "litellm", BaseURL: second.URL + "/v1", APIKey: "sk-second-secret", AccountKey: "provider:litellm"}, second.Client())

	for _, snap := range []quota.Snapshot{gotFirst, gotSecond} {
		if snap.AccountKey != "provider:litellm" {
			t.Errorf("AccountKey = %q, want provider:litellm for either API key", snap.AccountKey)
		}
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

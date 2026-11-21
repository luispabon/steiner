package provider

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/quota"
)

// mistralQuotaServer serves the /v1-prefixed budget and subscription endpoints
// with the given status and body, checking the bearer header on every request.
func mistralQuotaServer(t *testing.T, budgetStatus int, budgetBody string, subStatus int, subBody string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sk-test" {
			t.Errorf("Authorization = %q, want %q", got, "Bearer sk-test")
		}
		switch r.URL.Path {
		case "/v1/billing/budget":
			w.WriteHeader(budgetStatus)
			_, _ = w.Write([]byte(budgetBody))
		case "/v1/billing/subscription":
			w.WriteHeader(subStatus)
			_, _ = w.Write([]byte(subBody))
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestAPIKeyQuotaFetchMistralFixture(t *testing.T) {
	srv := mistralQuotaServer(t, http.StatusOK, apiKeyQuotaFixture(t, "mistral_budget.json"), http.StatusOK, apiKeyQuotaFixture(t, "mistral_subscription.json"))
	snap := apiKeyQuotaFetch(t, APIKeyQuotaConfig{ProviderType: "mistral", BaseURL: srv.URL + "/v1/", APIKey: "sk-test", AccountKey: "provider:api"}, srv.Client())

	if snap.AccountKey != "provider:api" {
		t.Errorf("AccountKey = %q, want provider:api", snap.AccountKey)
	}
	if snap.ProviderType != "mistral" || snap.Source != quota.SourceFetch || snap.FetchedAt.IsZero() {
		t.Errorf("snapshot = {ProviderType:%q Source:%q FetchedAt zero:%t}, want mistral, fetch, set", snap.ProviderType, snap.Source, snap.FetchedAt.IsZero())
	}
	if snap.Plan != "INDIVIDUAL" {
		t.Errorf("Plan = %q, want INDIVIDUAL", snap.Plan)
	}
	apiKeyQuotaAssertJSON(t, "Budgets", snap.Budgets, []quota.Budget{
		{Label: "api", Used: 5, Limit: apiKeyQuotaPtr(50.0), Currency: "EUR", ResetsAt: apiKeyQuotaTime(t, "2026-12-01T00:00:00Z")},
		{Label: "vibe", Used: 20, Limit: apiKeyQuotaPtr(80.0), Currency: "EUR", ResetsAt: apiKeyQuotaTime(t, "2026-12-01T00:00:00Z")},
	})
}

func TestAPIKeyQuotaMistralBudgetShapes(t *testing.T) {
	subscription := apiKeyQuotaFixture(t, "mistral_subscription.json")
	tests := []struct {
		name string
		body string
		want []quota.Budget
	}{
		{
			name: "vibe budget null",
			body: `{"api_budget":{"usage_percentage":10,"initial_budget":50,"currency":"EUR","reset_at":"2026-12-01T00:00:00Z"},"vibe_budget":null}`,
			want: []quota.Budget{{Label: "api", Used: 5, Limit: apiKeyQuotaPtr(50.0), Currency: "EUR", ResetsAt: apiKeyQuotaTime(t, "2026-12-01T00:00:00Z")}},
		},
		{
			name: "api budget null",
			body: `{"api_budget":null,"vibe_budget":{"usage_percentage":25,"initial_budget":80,"currency":"EUR"}}`,
			want: []quota.Budget{{Label: "vibe", Used: 20, Limit: apiKeyQuotaPtr(80.0), Currency: "EUR"}},
		},
		{
			name: "both budgets null yields no budgets and no error",
			body: `{"api_budget":null,"vibe_budget":null,"usage_percentage":10,"initial_budget":50,"currency":"EUR","reset_at":"2026-12-01T00:00:00Z"}`,
			want: []quota.Budget{},
		},
		{
			name: "missing usage percentage is skipped",
			body: `{"api_budget":{"initial_budget":50,"currency":"EUR"}}`,
			want: []quota.Budget{},
		},
		{
			name: "missing initial budget is skipped",
			body: `{"api_budget":{"usage_percentage":10,"currency":"EUR","reset_at":"2026-12-01T00:00:00Z"}}`,
			want: []quota.Budget{},
		},
		{
			name: "partial budgets on both rows yield no budgets",
			body: `{"api_budget":{"initial_budget":50,"currency":"EUR"},"vibe_budget":{"usage_percentage":25,"currency":"EUR"}}`,
			want: []quota.Budget{},
		},
		{
			name: "empty budget object is skipped",
			body: `{"api_budget":{},"vibe_budget":{"usage_percentage":25,"initial_budget":80,"currency":"EUR"}}`,
			want: []quota.Budget{{Label: "vibe", Used: 20, Limit: apiKeyQuotaPtr(80.0), Currency: "EUR"}},
		},
		{
			name: "no budget data yields no budgets",
			body: `{}`,
			want: []quota.Budget{},
		},
		{
			name: "malformed reset_at keeps the budget with a nil reset",
			body: `{"api_budget":{"usage_percentage":10,"initial_budget":50,"currency":"EUR","reset_at":"next month"}}`,
			want: []quota.Budget{{Label: "api", Used: 5, Limit: apiKeyQuotaPtr(50.0), Currency: "EUR"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := mistralQuotaServer(t, http.StatusOK, tt.body, http.StatusOK, subscription)
			cfg := APIKeyQuotaConfig{ProviderType: "mistral", BaseURL: srv.URL + "/v1", APIKey: "sk-test"}
			snap := apiKeyQuotaFetch(t, cfg, srv.Client())
			apiKeyQuotaAssertJSON(t, "Budgets", snap.Budgets, tt.want)
		})
	}
}

func TestAPIKeyQuotaMistralSubscriptionIsBestEffort(t *testing.T) {
	budget := apiKeyQuotaFixture(t, "mistral_budget.json")
	want := []quota.Budget{
		{Label: "api", Used: 5, Limit: apiKeyQuotaPtr(50.0), Currency: "EUR", ResetsAt: apiKeyQuotaTime(t, "2026-12-01T00:00:00Z")},
		{Label: "vibe", Used: 20, Limit: apiKeyQuotaPtr(80.0), Currency: "EUR", ResetsAt: apiKeyQuotaTime(t, "2026-12-01T00:00:00Z")},
	}
	tests := []struct {
		name   string
		status int
		body   string
	}{
		{name: "server error", status: http.StatusInternalServerError, body: `{"error":"down"}`},
		{name: "not found", status: http.StatusNotFound, body: `{}`},
		{name: "malformed body", status: http.StatusOK, body: "{"},
		{name: "empty plan name", status: http.StatusOK, body: `{"plan_name":""}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := mistralQuotaServer(t, http.StatusOK, budget, tt.status, tt.body)
			snap := apiKeyQuotaFetch(t, APIKeyQuotaConfig{ProviderType: "mistral", BaseURL: srv.URL + "/v1", APIKey: "sk-test"}, srv.Client())
			if snap.Plan != "" {
				t.Errorf("Plan = %q, want empty", snap.Plan)
			}
			apiKeyQuotaAssertJSON(t, "Budgets", snap.Budgets, want)
		})
	}
}

func TestAPIKeyQuotaMistralBudgetErrors(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{name: "unauthorized", status: http.StatusUnauthorized, body: `{"error":"denied"}`, want: "billing/budget: 401"},
		{name: "forbidden", status: http.StatusForbidden, body: `{"error":"denied"}`, want: "billing/budget: 403"},
		{name: "not found", status: http.StatusNotFound, body: `{"error":"missing"}`, want: "billing/budget: 404"},
		{name: "malformed", status: http.StatusOK, body: "{", want: "billing/budget: bad response"},
		{name: "null body", status: http.StatusOK, body: "null", want: "billing/budget: bad response"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := mistralQuotaServer(t, tt.status, tt.body, http.StatusOK, "{}")
			_, err := apiKeyQuotaTryFetch(t, APIKeyQuotaConfig{ProviderType: "mistral", BaseURL: srv.URL + "/v1", APIKey: "sk-test"}, srv.Client())
			if err == nil || err.Error() != tt.want {
				t.Fatalf("Fetch error = %v, want %q", err, tt.want)
			}
			if strings.Contains(err.Error(), "sk-test") {
				t.Errorf("Fetch error %q contains the API key", err.Error())
			}
		})
	}
}

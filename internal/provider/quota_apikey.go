package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/luispabon/steiner/internal/quota"
)

// APIKeyQuotaConfig identifies one API-key account whose quota is fetched.
// Callers resolve ProviderType, BaseURL, and APIKey before calling
// NewAPIKeyQuotaFetcher.
type APIKeyQuotaConfig struct {
	ProviderType string
	BaseURL      string
	APIKey       string
}

// apiKeyQuotaMaxBody bounds the bytes decoded from one quota response.
const apiKeyQuotaMaxBody = 1 << 20

// apiKeyQuotaFetcher fetches quota for one API-key account. base is the
// trimmed base URL used for every request and for the account key.
type apiKeyQuotaFetcher struct {
	cfg    APIKeyQuotaConfig
	base   string
	client *http.Client
}

// NewAPIKeyQuotaFetcher returns a quota.Fetcher for an opencode_go, openrouter,
// or litellm API-key account. A nil client uses http.DefaultClient. Other
// provider types return an error.
func NewAPIKeyQuotaFetcher(cfg APIKeyQuotaConfig, client *http.Client) (quota.Fetcher, error) {
	switch cfg.ProviderType {
	case "opencode_go", "openrouter", "litellm":
	default:
		return nil, fmt.Errorf("api key quota: unsupported provider type %q", cfg.ProviderType)
	}
	if client == nil {
		client = http.DefaultClient
	}
	return &apiKeyQuotaFetcher{
		cfg:    cfg,
		base:   strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/"),
		client: client,
	}, nil
}

// Fetch returns the quota snapshot for the configured provider type.
func (f *apiKeyQuotaFetcher) Fetch(ctx context.Context, _ quota.FetchOptions) (quota.Snapshot, error) {
	snap := quota.Snapshot{
		AccountKey:   quota.APIKeyAccountKey(f.cfg.ProviderType, f.base, f.cfg.APIKey),
		ProviderType: f.cfg.ProviderType,
		FetchedAt:    time.Now(),
		Source:       quota.SourceFetch,
	}
	var err error
	switch f.cfg.ProviderType {
	case "opencode_go":
		snap.Windows, err = f.apiKeyQuotaOpenCode(ctx)
	case "openrouter":
		snap.Budgets, err = f.apiKeyQuotaOpenRouter(ctx)
	case "litellm":
		snap.Budgets, err = f.apiKeyQuotaLiteLLM(ctx)
	}
	if err != nil {
		return quota.Snapshot{}, err
	}
	return snap, nil
}

// apiKeyQuotaGet GETs url with the API key as a bearer token and decodes the
// JSON body into v. Errors name label and never include the API key. A non-2xx
// status returns "label: status" and an undecodable body returns
// "label: bad response".
func (f *apiKeyQuotaFetcher) apiKeyQuotaGet(ctx context.Context, url, label string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("%s: %w", label, err)
	}
	req.Header.Set("Authorization", "Bearer "+f.cfg.APIKey)
	req.Header.Set("Accept", "application/json")
	resp, err := f.client.Do(req)
	if err != nil {
		return fmt.Errorf("%s: %w", label, err)
	}
	defer func() { _ = resp.Body.Close() }() // a close error after the body is read is not actionable
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("%s: %d", label, resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, apiKeyQuotaMaxBody)).Decode(v); err != nil {
		return fmt.Errorf("%s: bad response", label)
	}
	return nil
}

// apiKeyQuotaOpenCode fetches {base}/usage and maps the rolling, weekly, and
// monthly windows. A null or missing window is skipped. A window with an absent
// or null resetsAt keeps a zero ResetsAt, which the quota package treats as
// unknown. A present resetsAt that is not RFC 3339 is a bad response.
func (f *apiKeyQuotaFetcher) apiKeyQuotaOpenCode(ctx context.Context) ([]quota.Window, error) {
	const label = "usage"
	type window struct {
		Status   string  `json:"status"`
		Percent  float64 `json:"percent"`
		ResetsAt string  `json:"resetsAt"`
	}
	var body struct {
		Usage *struct {
			Rolling *window `json:"rolling"`
			Weekly  *window `json:"weekly"`
			Monthly *window `json:"monthly"`
		} `json:"usage"`
	}
	if err := f.apiKeyQuotaGet(ctx, f.base+"/usage", label, &body); err != nil {
		return nil, err
	}
	if body.Usage == nil {
		return nil, fmt.Errorf("%s: bad response", label)
	}
	specs := []struct {
		duration time.Duration
		window   *window
	}{
		{5 * time.Hour, body.Usage.Rolling},
		{7 * 24 * time.Hour, body.Usage.Weekly},
		{30 * 24 * time.Hour, body.Usage.Monthly},
	}
	windows := make([]quota.Window, 0, len(specs))
	for _, s := range specs {
		if s.window == nil {
			continue
		}
		var resets time.Time
		if s.window.ResetsAt != "" {
			parsed, err := time.Parse(time.RFC3339Nano, s.window.ResetsAt)
			if err != nil {
				return nil, fmt.Errorf("%s: bad response", label)
			}
			resets = parsed
		}
		windows = append(windows, quota.Window{
			Label:       quota.WindowLabel(s.duration),
			Duration:    s.duration,
			UsedPercent: s.window.Percent,
			ResetsAt:    resets,
			Status:      s.window.Status,
		})
	}
	return windows, nil
}

// apiKeyQuotaOpenRouter fetches {base}/key. Each data.budgets entry becomes a
// "budget" budget. Without budgets, a non-null data.limit becomes a "limit"
// budget. usage_daily, usage_weekly, and usage_monthly add spend-only budgets
// when present.
func (f *apiKeyQuotaFetcher) apiKeyQuotaOpenRouter(ctx context.Context) ([]quota.Budget, error) {
	const label = "key"
	var body struct {
		Data *struct {
			Usage        float64  `json:"usage"`
			Limit        *float64 `json:"limit"`
			LimitReset   *string  `json:"limit_reset"`
			UsageDaily   *float64 `json:"usage_daily"`
			UsageWeekly  *float64 `json:"usage_weekly"`
			UsageMonthly *float64 `json:"usage_monthly"`
			Budgets      []struct {
				SpendUSD      float64  `json:"spend_usd"`
				LimitUSD      *float64 `json:"limit_usd"`
				ResetInterval string   `json:"reset_interval"`
				ResetsAt      *string  `json:"resets_at"`
			} `json:"budgets"`
		} `json:"data"`
	}
	if err := f.apiKeyQuotaGet(ctx, f.base+"/key", label, &body); err != nil {
		return nil, err
	}
	d := body.Data
	if d == nil {
		return nil, fmt.Errorf("%s: bad response", label)
	}
	var budgets []quota.Budget
	for _, b := range d.Budgets {
		budgets = append(budgets, quota.Budget{
			Label:    "budget",
			Used:     b.SpendUSD,
			Limit:    b.LimitUSD,
			Currency: "USD",
			Period:   b.ResetInterval,
			ResetsAt: apiKeyQuotaParseTime(b.ResetsAt),
		})
	}
	if len(budgets) == 0 && d.Limit != nil {
		budgets = append(budgets, quota.Budget{
			Label:    "limit",
			Used:     d.Usage,
			Limit:    d.Limit,
			Currency: "USD",
			Period:   apiKeyQuotaDeref(d.LimitReset),
		})
	}
	spend := []struct {
		label, period string
		used          *float64
	}{
		{"today", "daily", d.UsageDaily},
		{"week", "weekly", d.UsageWeekly},
		{"month", "monthly", d.UsageMonthly},
	}
	for _, s := range spend {
		if s.used == nil {
			continue
		}
		budgets = append(budgets, quota.Budget{
			Label:    s.label,
			Used:     *s.used,
			Currency: "USD",
			Period:   s.period,
		})
	}
	return budgets, nil
}

// apiKeyQuotaLiteLLM fetches {base}/key/info, where base has a trailing /v1
// removed. A non-null info.max_budget becomes a "budget" budget. Otherwise the
// spend is a spend-only "spend" budget.
func (f *apiKeyQuotaFetcher) apiKeyQuotaLiteLLM(ctx context.Context) ([]quota.Budget, error) {
	const label = "key/info"
	var body struct {
		Info *struct {
			Spend          float64  `json:"spend"`
			MaxBudget      *float64 `json:"max_budget"`
			BudgetDuration string   `json:"budget_duration"`
			BudgetResetAt  *string  `json:"budget_reset_at"`
		} `json:"info"`
	}
	if err := f.apiKeyQuotaGet(ctx, strings.TrimSuffix(f.base, "/v1")+"/key/info", label, &body); err != nil {
		return nil, err
	}
	if body.Info == nil {
		return nil, fmt.Errorf("%s: bad response", label)
	}
	info := body.Info
	if info.MaxBudget == nil {
		return []quota.Budget{{Label: "spend", Used: info.Spend, Currency: "USD"}}, nil
	}
	return []quota.Budget{{
		Label:    "budget",
		Used:     info.Spend,
		Limit:    info.MaxBudget,
		Currency: "USD",
		Period:   info.BudgetDuration,
		ResetsAt: apiKeyQuotaParseTime(info.BudgetResetAt),
	}}, nil
}

// apiKeyQuotaParseTime parses an optional RFC 3339 timestamp. A nil or empty
// value returns nil. A malformed value also returns nil, so the budget keeps its
// amounts. The reset time is optional, so dropping it is intentional.
func apiKeyQuotaParseTime(s *string) *time.Time {
	if s == nil || *s == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339Nano, *s)
	if err != nil {
		return nil
	}
	return &t
}

// apiKeyQuotaDeref returns the value of s, or "" when s is nil.
func apiKeyQuotaDeref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

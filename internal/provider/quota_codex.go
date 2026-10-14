package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/quota"
)

// codexQuotaUsageURL is the ChatGPT backend endpoint that reports the usage
// windows and credits of the signed-in Codex account.
const codexQuotaUsageURL = "https://chatgpt.com/backend-api/wham/usage"

// codexQuotaMaxBody bounds how much of a wham/usage response is read.
const codexQuotaMaxBody = 1 << 20

// codexQuotaRateLimitsEvent is the WebSocket frame type carrying rate limits.
const codexQuotaRateLimitsEvent = "codex.rate_limits"

// codexQuotaMainFamily is the header prefix of the main Codex rate-limit family.
const codexQuotaMainFamily = "x-codex"

// CodexQuotaAccountKey returns the quota account key for a Codex ChatGPT account.
func CodexQuotaAccountKey(accountID string) string {
	return "codex:" + accountID
}

// NewCodexQuotaFetcher returns a quota.Fetcher that reads Codex usage from the
// ChatGPT wham/usage endpoint. Each Fetch resolves the stored Codex credentials
// again. A nil httpClient uses the default client.
func NewCodexQuotaFetcher(httpClient *http.Client) quota.Fetcher {
	return &codexQuotaFetcher{httpClient: httpClient}
}

type codexQuotaFetcher struct {
	httpClient *http.Client
}

// Fetch implements quota.Fetcher.
func (f *codexQuotaFetcher) Fetch(ctx context.Context, _ quota.FetchOptions) (quota.Snapshot, error) {
	auth, err := ResolveCodexAuth(ctx, f.httpClient)
	if err != nil {
		return quota.Snapshot{}, err
	}
	if auth.AccountID == "" {
		return quota.Snapshot{}, errors.New("codex token missing ChatGPT account metadata")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, codexQuotaUsageURL, nil)
	if err != nil {
		return quota.Snapshot{}, fmt.Errorf("build wham/usage request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+auth.AccessToken)
	req.Header.Set("ChatGPT-Account-Id", auth.AccountID)
	req.Header.Set("User-Agent", "codex-cli")

	client := f.httpClient
	if client == nil {
		client = defaultHTTPClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return quota.Snapshot{}, fmt.Errorf("wham/usage: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return quota.Snapshot{}, fmt.Errorf("wham/usage: %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, codexQuotaMaxBody))
	if err != nil {
		return quota.Snapshot{}, fmt.Errorf("read wham/usage body: %w", err)
	}
	snap, err := parseCodexUsageBody(body, CodexQuotaAccountKey(auth.AccountID))
	if err != nil {
		return quota.Snapshot{}, err
	}
	snap.Source = quota.SourceFetch
	return snap, nil
}

// codexQuotaWhamUsage is the wham/usage response body.
type codexQuotaWhamUsage struct {
	PlanType             string                      `json:"plan_type"`
	RateLimit            *codexQuotaRateLimit        `json:"rate_limit"`
	Credits              codexQuotaCredits           `json:"credits"`
	AdditionalRateLimits []codexQuotaAdditionalLimit `json:"additional_rate_limits"`
}

type codexQuotaAdditionalLimit struct {
	LimitName string               `json:"limit_name"`
	RateLimit *codexQuotaRateLimit `json:"rate_limit"`
}

// codexQuotaRateLimit holds the primary and secondary windows of one limit.
// A null window decodes to nil and is skipped.
type codexQuotaRateLimit struct {
	Primary   *codexQuotaWhamWindow `json:"primary_window"`
	Secondary *codexQuotaWhamWindow `json:"secondary_window"`
}

type codexQuotaWhamWindow struct {
	UsedPercent        float64 `json:"used_percent"`
	LimitWindowSeconds int64   `json:"limit_window_seconds"`
	ResetAt            int64   `json:"reset_at"`
}

type codexQuotaCredits struct {
	HasCredits bool   `json:"has_credits"`
	Unlimited  bool   `json:"unlimited"`
	Balance    string `json:"balance"`
}

// parseCodexUsageBody parses a wham/usage response body into a snapshot. The
// main limit's windows come first, then each additional limit's windows labelled
// "<limit_name> <window>".
func parseCodexUsageBody(body []byte, accountKey string) (quota.Snapshot, error) {
	var usage codexQuotaWhamUsage
	if err := json.Unmarshal(body, &usage); err != nil {
		return quota.Snapshot{}, fmt.Errorf("decode wham/usage: %w", err)
	}
	snap := codexQuotaNewSnapshot(accountKey, usage.PlanType)
	snap.Windows = codexQuotaAppendWhamWindows(snap.Windows, "", usage.RateLimit)
	for _, extra := range usage.AdditionalRateLimits {
		snap.Windows = codexQuotaAppendWhamWindows(snap.Windows, extra.LimitName+" ", extra.RateLimit)
	}
	snap.Credits = codexQuotaCreditsText(usage.Credits)
	return snap, nil
}

func codexQuotaAppendWhamWindows(windows []quota.Window, prefix string, limit *codexQuotaRateLimit) []quota.Window {
	if limit == nil {
		return windows
	}
	for _, w := range []*codexQuotaWhamWindow{limit.Primary, limit.Secondary} {
		if w == nil {
			continue
		}
		d := time.Duration(w.LimitWindowSeconds) * time.Second
		windows = append(windows, codexQuotaWindow(prefix+quota.WindowLabel(d), d, w.UsedPercent, w.ResetAt))
	}
	return windows
}

// codexQuotaFrame is a codex.rate_limits WebSocket frame.
type codexQuotaFrame struct {
	Type       string                `json:"type"`
	PlanType   string                `json:"plan_type"`
	RateLimits codexQuotaFrameLimits `json:"rate_limits"`
	Credits    codexQuotaCredits     `json:"credits"`
}

type codexQuotaFrameLimits struct {
	Primary   *codexQuotaFrameWindow `json:"primary"`
	Secondary *codexQuotaFrameWindow `json:"secondary"`
}

type codexQuotaFrameWindow struct {
	UsedPercent   float64 `json:"used_percent"`
	WindowMinutes int64   `json:"window_minutes"`
	ResetAt       int64   `json:"reset_at"`
}

// parseCodexRateLimitsFrame parses a codex.rate_limits frame. Any other frame
// type, and any payload that does not decode, returns ok false.
func parseCodexRateLimitsFrame(raw []byte, accountKey string) (quota.Snapshot, bool) {
	var frame codexQuotaFrame
	if err := json.Unmarshal(raw, &frame); err != nil || frame.Type != codexQuotaRateLimitsEvent {
		return quota.Snapshot{}, false
	}
	snap := codexQuotaNewSnapshot(accountKey, frame.PlanType)
	snap.Source = quota.SourcePassive
	for _, w := range []*codexQuotaFrameWindow{frame.RateLimits.Primary, frame.RateLimits.Secondary} {
		if w == nil {
			continue
		}
		d := time.Duration(w.WindowMinutes) * time.Minute
		snap.Windows = append(snap.Windows, codexQuotaWindow(quota.WindowLabel(d), d, w.UsedPercent, w.ResetAt))
	}
	snap.Credits = codexQuotaCreditsText(frame.Credits)
	return snap, true
}

// parseCodexQuotaHeaders parses Codex rate-limit response headers. A header
// family is identified by a name ending in "-primary-used-percent". The main
// family is "x-codex"; other families such as "x-codex-other" label their
// windows with the family name ("codex_other 5h"). ok is false when no family
// header is present.
func parseCodexQuotaHeaders(h http.Header, accountKey string) (quota.Snapshot, bool) {
	values := make(map[string]string, len(h))
	for name, vs := range h {
		if len(vs) > 0 {
			values[strings.ToLower(name)] = vs[0]
		}
	}
	families := codexQuotaHeaderFamilies(values)
	if len(families) == 0 {
		return quota.Snapshot{}, false
	}

	snap := codexQuotaNewSnapshot(accountKey, "")
	snap.Source = quota.SourcePassive
	for _, family := range families {
		prefix := ""
		if family != codexQuotaMainFamily {
			prefix = codexQuotaHeaderLabel(family) + " "
		}
		for _, kind := range []string{"primary", "secondary"} {
			base := family + "-" + kind
			percent, err := strconv.ParseFloat(values[base+"-used-percent"], 64)
			if err != nil {
				continue
			}
			d := time.Duration(codexQuotaHeaderInt(values, base+"-window-minutes")) * time.Minute
			resetAt := codexQuotaHeaderInt(values, base+"-reset-at")
			snap.Windows = append(snap.Windows, codexQuotaWindow(prefix+quota.WindowLabel(d), d, percent, resetAt))
		}
	}

	snap.Credits = codexQuotaCreditsText(codexQuotaCredits{
		HasCredits: strings.EqualFold(values["x-codex-credits-has-credits"], "true"),
		Unlimited:  strings.EqualFold(values["x-codex-credits-unlimited"], "true"),
		Balance:    values["x-codex-credits-balance"],
	})
	return snap, true
}

// codexQuotaHeaderFamilies returns the header family prefixes that carry a
// primary used percent. The main family comes first, the rest sorted by name.
func codexQuotaHeaderFamilies(values map[string]string) []string {
	var families []string
	for name := range values {
		if family, ok := strings.CutSuffix(name, "-primary-used-percent"); ok && family != "" {
			families = append(families, family)
		}
	}
	sort.Strings(families)
	sort.SliceStable(families, func(i, j int) bool {
		return families[i] == codexQuotaMainFamily && families[j] != codexQuotaMainFamily
	})
	return families
}

// codexQuotaHeaderLabel turns a family prefix such as "x-codex-other" into the
// window label prefix "codex_other".
func codexQuotaHeaderLabel(family string) string {
	return strings.ReplaceAll(strings.TrimPrefix(family, "x-"), "-", "_")
}

// codexQuotaHeaderInt reads an integer header, returning 0 when it is absent or
// not a number.
func codexQuotaHeaderInt(values map[string]string, name string) int64 {
	n, err := strconv.ParseInt(values[name], 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// codexQuotaAccountKeyFromHeaders returns the quota account key for the
// ChatGPT-Account-ID header, or "" when the header is absent.
func codexQuotaAccountKeyFromHeaders(headers map[string]string) string {
	for name, value := range headers {
		if strings.EqualFold(name, "ChatGPT-Account-ID") && value != "" {
			return CodexQuotaAccountKey(value)
		}
	}
	return ""
}

// codexQuotaObserveResponse passes the rate-limit headers of a successful
// response to the observer. It runs on the request path and holds no lock.
func (c *Client) codexQuotaObserveResponse(h http.Header) {
	if c.quotaAccountKey == "" {
		return
	}
	if snap, ok := parseCodexQuotaHeaders(h, c.quotaAccountKey); ok {
		c.quotaObserver(snap)
	}
}

func codexQuotaNewSnapshot(accountKey, plan string) quota.Snapshot {
	return quota.Snapshot{
		AccountKey:   accountKey,
		ProviderType: string(config.ProviderTypeCodex),
		Plan:         plan,
	}
}

// codexQuotaWindow builds a window. A reset value that is absent, zero, or
// negative, including an unparseable header (which decodes as 0), leaves
// ResetsAt as the zero time rather than the Unix epoch.
func codexQuotaWindow(label string, d time.Duration, usedPercent float64, resetAt int64) quota.Window {
	var resetsAt time.Time
	if resetAt > 0 {
		resetsAt = time.Unix(resetAt, 0).UTC()
	}
	return quota.Window{
		Label:       label,
		Duration:    d,
		UsedPercent: usedPercent,
		ResetsAt:    resetsAt,
	}
}

// codexQuotaCreditsText formats a credit balance as "$<balance>". Unlimited,
// absent, or empty credits produce "".
func codexQuotaCreditsText(c codexQuotaCredits) string {
	if !c.HasCredits || c.Unlimited || c.Balance == "" {
		return ""
	}
	return "$" + c.Balance
}

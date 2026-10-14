package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/quota"
)

// ClaudeSubQuotaAccountKey is the quota account key of the claude_subscription
// provider.
const ClaudeSubQuotaAccountKey = "claude_subscription"

// claudeSubQuotaFetchTimeout bounds one quota fetch. A fetch with no live
// session may start a probe CLI process, so it needs more than the quota
// service's default timeout.
const claudeSubQuotaFetchTimeout = 20 * time.Second

// claudeSubQuotaProbeKey keys the probe session that answers a quota fetch when
// no live session runs. It has no advisor suffix, so the reaper never closes it;
// Close does.
const claudeSubQuotaProbeKey = "quota-probe"

// claudeSubQuotaWindow maps one get_usage rate_limits key to a quota window.
type claudeSubQuotaWindow struct {
	key      string
	label    string
	duration time.Duration
}

// claudeSubQuotaWindows lists the rate_limits windows steiner reports, in the
// order they appear in a snapshot. Other rate_limits keys are ignored.
var claudeSubQuotaWindows = []claudeSubQuotaWindow{
	{key: "five_hour", label: quota.WindowLabel(5 * time.Hour), duration: 5 * time.Hour},
	{key: "seven_day", label: quota.WindowLabel(7 * 24 * time.Hour), duration: 7 * 24 * time.Hour},
	{key: "seven_day_opus", label: "weekly opus", duration: 7 * 24 * time.Hour},
	{key: "seven_day_sonnet", label: "weekly sonnet", duration: 7 * 24 * time.Hour},
	{key: "seven_day_oauth_apps", label: "weekly apps", duration: 7 * 24 * time.Hour},
}

// parseClaudeSubUsage converts a get_usage reply into a quota snapshot. It fails
// when rate_limits_available is not true, when rate_limits is missing or null, or
// when no recognized window is usable. A mapped window that is missing, null,
// malformed, lacks utilization, or has a malformed resets_at is skipped. Unknown
// keys are ignored.
func parseClaudeSubUsage(raw json.RawMessage) (quota.Snapshot, error) {
	var body struct {
		SubscriptionType    string                     `json:"subscription_type"`
		RateLimitsAvailable bool                       `json:"rate_limits_available"`
		RateLimits          map[string]json.RawMessage `json:"rate_limits"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return quota.Snapshot{}, fmt.Errorf("decode claude usage: %w", err)
	}
	if !body.RateLimitsAvailable {
		return quota.Snapshot{}, errors.New("claude usage has no rate limits: rate_limits_available is not true")
	}
	if body.RateLimits == nil {
		return quota.Snapshot{}, errors.New("claude usage rate_limits missing")
	}
	snap := quota.Snapshot{
		AccountKey:   ClaudeSubQuotaAccountKey,
		ProviderType: string(config.ProviderTypeClaudeSubscription),
		Plan:         body.SubscriptionType,
	}
	for _, w := range claudeSubQuotaWindows {
		if window, ok := claudeSubQuotaParseWindow(w, body.RateLimits[w.key]); ok {
			snap.Windows = append(snap.Windows, window)
		}
	}
	if len(snap.Windows) == 0 {
		return quota.Snapshot{}, errors.New("claude usage has no usable rate-limit windows")
	}
	return snap, nil
}

// claudeSubQuotaParseWindow decodes one rate_limits entry. ok is false when the
// entry is missing, null, malformed, lacks utilization, or has a malformed
// resets_at. Such an entry is skipped rather than failing the whole snapshot.
func claudeSubQuotaParseWindow(w claudeSubQuotaWindow, raw json.RawMessage) (quota.Window, bool) {
	if len(raw) == 0 {
		return quota.Window{}, false
	}
	var limit struct {
		Utilization *float64 `json:"utilization"`
		ResetsAt    *string  `json:"resets_at"`
	}
	if json.Unmarshal(raw, &limit) != nil || limit.Utilization == nil {
		return quota.Window{}, false
	}
	window := quota.Window{Label: w.label, Duration: w.duration, UsedPercent: *limit.Utilization}
	if limit.ResetsAt != nil {
		resets, err := time.Parse(time.RFC3339, *limit.ResetsAt)
		if err != nil {
			return quota.Window{}, false
		}
		window.ResetsAt = resets
	}
	return window, true
}

// QuotaSnapshot returns the claude_subscription quota state. It reads usage
// through the control channel of a live session, busy or idle, without taking
// that session's whole-call lock: control requests are matched by request_id, so
// a usage query can run during a turn. A non-advisor session is preferred. With
// no live session, QuotaSnapshot returns an error unless allowSpawn is set, in
// which case it starts a probe session that stays in the pool until Close.
func (p *ClaudeSubscriptionPool) QuotaSnapshot(ctx context.Context, allowSpawn bool) (quota.Snapshot, error) {
	control := p.claudeSubQuotaLiveControl()
	if control != nil {
		return claudeSubQuotaUsage(ctx, control)
	}
	if !allowSpawn {
		return quota.Snapshot{}, errors.New("no live claude session")
	}
	s, err := p.acquire(ctx, claudeSubQuotaProbeKey, claudeSubStartSpec{})
	if err != nil {
		return quota.Snapshot{}, err
	}
	defer p.release(s)
	return claudeSubQuotaUsage(ctx, s.control)
}

// claudeSubQuotaLiveControl returns the control channel of a live session, or nil
// when none runs. It holds the pool mutex only to read the session table and
// never takes a session's whole-call lock.
func (p *ClaudeSubscriptionPool) claudeSubQuotaLiveControl() *claudeSubControl {
	p.mu.Lock()
	defer p.mu.Unlock()
	var advisor *claudeSubControl
	for key, s := range p.sessions {
		if s.exited() || s.isGone() || s.control == nil {
			continue
		}
		if !claudeSubSessionIsAdvisor(key) {
			return s.control
		}
		advisor = s.control
	}
	return advisor
}

// claudeSubQuotaUsage asks control for usage and parses the reply.
func claudeSubQuotaUsage(ctx context.Context, control *claudeSubControl) (quota.Snapshot, error) {
	raw, err := control.getUsage(ctx)
	if err != nil {
		return quota.Snapshot{}, fmt.Errorf("get claude usage: %w", err)
	}
	return parseClaudeSubUsage(raw)
}

// claudeSubQuotaFetcher fetches claude_subscription quota through a pool.
type claudeSubQuotaFetcher struct {
	pool *ClaudeSubscriptionPool
}

// NewClaudeSubQuotaFetcher returns a quota.Fetcher that reads usage through pool.
func NewClaudeSubQuotaFetcher(pool *ClaudeSubscriptionPool) quota.Fetcher {
	return claudeSubQuotaFetcher{pool: pool}
}

// Fetch returns the current quota snapshot, marked as a fetch.
func (f claudeSubQuotaFetcher) Fetch(ctx context.Context, opts quota.FetchOptions) (quota.Snapshot, error) {
	snap, err := f.pool.QuotaSnapshot(ctx, opts.AllowSpawn)
	if err != nil {
		return quota.Snapshot{}, err
	}
	snap.Source = quota.SourceFetch
	return snap, nil
}

// FetchTimeout implements quota.TimeoutFetcher.
func (claudeSubQuotaFetcher) FetchTimeout() time.Duration {
	return claudeSubQuotaFetchTimeout
}

// claudeSubQuotaObservedGetUsage wraps getUsage so each successful get_usage
// reply that parses is also passed to the pool's QuotaObserver as a passive
// snapshot. The reply is returned unchanged, so the usage gate decides on exactly
// what the CLI sent. A reply that does not parse is not observed.
func claudeSubQuotaObservedGetUsage(pool *ClaudeSubscriptionPool, getUsage func(context.Context) (json.RawMessage, error)) func(context.Context) (json.RawMessage, error) {
	if pool == nil || pool.opts.QuotaObserver == nil {
		return getUsage
	}
	observe := pool.opts.QuotaObserver
	return func(ctx context.Context) (json.RawMessage, error) {
		raw, err := getUsage(ctx)
		if err != nil {
			return raw, err
		}
		if snap, perr := parseClaudeSubUsage(raw); perr == nil {
			snap.Source = quota.SourcePassive
			observe(snap)
		}
		return raw, nil
	}
}

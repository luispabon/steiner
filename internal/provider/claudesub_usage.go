package provider

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	// claudeSubCreditsEnabledMsg refuses claude_subscription when the account has
	// paid extra usage (usage credits) turned on (D5).
	claudeSubCreditsEnabledMsg = "usage credits (extra usage) are enabled on this Claude account, so claude_subscription is refused to avoid paid usage: turn them off at claude.ai → Settings → Usage, or use the anthropic provider with an API key"
	// claudeSubGateFailClosedMsg refuses claude_subscription when the extra-usage
	// state cannot be confirmed (D5: fail closed for all accounts).
	claudeSubGateFailClosedMsg = "could not confirm that usage credits are off for this Claude account, so claude_subscription is refused (fail closed)"
)

// claudeSubUsageGate enforces the strict extra-usage gate (D5): it passes only
// when rate_limits_available is true and rate_limits.extra_usage.is_enabled is
// false. Every other outcome (missing, null, undecodable) fails closed.
func claudeSubUsageGate(raw json.RawMessage) error {
	var body struct {
		RateLimitsAvailable *bool `json:"rate_limits_available"`
		RateLimits          *struct {
			ExtraUsage *struct {
				IsEnabled      *bool  `json:"is_enabled"`
				DisabledReason string `json:"disabled_reason"`
			} `json:"extra_usage"`
		} `json:"rate_limits"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return errors.New(claudeSubGateFailClosedMsg)
	}
	if body.RateLimitsAvailable == nil || !*body.RateLimitsAvailable {
		return errors.New(claudeSubGateFailClosedMsg)
	}
	if body.RateLimits == nil || body.RateLimits.ExtraUsage == nil || body.RateLimits.ExtraUsage.IsEnabled == nil {
		return errors.New(claudeSubGateFailClosedMsg)
	}
	if *body.RateLimits.ExtraUsage.IsEnabled {
		return errors.New(claudeSubCreditsEnabledMsg)
	}
	return nil
}

// claudeSubRateLimitInfo is the shape of a rate_limit_event's rate_limit_info.
type claudeSubRateLimitInfo struct {
	Status         string `json:"status"`
	RateLimitType  string `json:"rateLimitType"`
	OverageStatus  string `json:"overageStatus"`
	ResetsAt       int64  `json:"resetsAt"`
	IsUsingOverage *bool  `json:"isUsingOverage"`
	OverageInUse   *bool  `json:"overageInUse"`
}

// claudeSubRateLimitVerdict classifies a rate_limit_event's rate_limit_info.
// overage reports that paid extra usage started; it fails closed (true) when
// the event cannot be decoded or its shape is unrecognised. limit carries the
// usage window when the CLI rejected the turn for a plain (non-overage) usage
// limit.
func claudeSubRateLimitVerdict(raw json.RawMessage) (overage bool, limit *UsageLimitError, err error) {
	var info claudeSubRateLimitInfo
	if err := json.Unmarshal(raw, &info); err != nil {
		return true, nil, fmt.Errorf("decode claude rate limit event: %w", err)
	}
	if claudeSubRateLimitIsOverage(info) {
		return true, nil, nil
	}
	if info.Status == "rejected" {
		limit = &UsageLimitError{
			Kind:     UsageLimitKindUsage,
			Provider: "claude_subscription",
			Code:     info.RateLimitType,
			Message:  "Claude subscription usage limit reached",
		}
		if info.ResetsAt > 0 {
			limit.ResetsAt = time.Unix(info.ResetsAt, 0)
		}
		return false, limit, nil
	}
	if info.Status != "allowed" {
		return true, nil, fmt.Errorf("could not classify claude rate limit event: %s", truncateRunes(string(raw), usageLimitBodyRunes))
	}
	return false, nil, nil
}

func claudeSubRateLimitIsOverage(info claudeSubRateLimitInfo) bool {
	if info.IsUsingOverage != nil && *info.IsUsingOverage {
		return true
	}
	if info.OverageInUse != nil && *info.OverageInUse {
		return true
	}
	if info.RateLimitType == "overage" {
		return true
	}
	return info.Status == "rejected" && info.OverageStatus != "" && info.OverageStatus != "rejected"
}

// claudeSubOverageNotificationPrefixes are the CLI notification prefixes that
// mean paid extra usage started (D5).
var claudeSubOverageNotificationPrefixes = []string{
	"you're now using usage credits",
	"now using extra usage",
	"you're now using extra usage",
}

// claudeSubNotificationIsOverage reports whether a system notification text
// starts with a known overage notice (case-insensitive).
func claudeSubNotificationIsOverage(text string) bool {
	t := strings.ToLower(strings.TrimSpace(text))
	for _, prefix := range claudeSubOverageNotificationPrefixes {
		if strings.HasPrefix(t, prefix) {
			return true
		}
	}
	return false
}

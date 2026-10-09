package provider

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func claudeSubFixture(t *testing.T, name string) json.RawMessage {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "claudesub", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return json.RawMessage(b)
}

const (
	claudeSubCreditsEnabledWant = "usage credits (extra usage) are enabled on this Claude account, so claude_subscription is refused to avoid paid usage: turn them off at claude.ai → Settings → Usage, or use the anthropic provider with an API key"
	claudeSubFailClosedWant     = "could not confirm that usage credits are off for this Claude account, so claude_subscription is refused (fail closed)"
)

func TestClaudeSubUsageGate(t *testing.T) {
	tests := []struct {
		name    string
		raw     json.RawMessage
		wantErr string
	}{
		{"ok fixture", claudeSubFixture(t, "get_usage_ok.json"), ""},
		{"credits on fixture", claudeSubFixture(t, "get_usage_credits_on.json"), claudeSubCreditsEnabledWant},
		{"rate_limits_available false", json.RawMessage(`{"rate_limits_available":false,"rate_limits":{"extra_usage":{"is_enabled":false}}}`), claudeSubFailClosedWant},
		{"rate_limits_available absent", json.RawMessage(`{"rate_limits":{"extra_usage":{"is_enabled":false}}}`), claudeSubFailClosedWant},
		{"rate_limits_available null", json.RawMessage(`{"rate_limits_available":null,"rate_limits":{"extra_usage":{"is_enabled":false}}}`), claudeSubFailClosedWant},
		{"rate_limits absent", json.RawMessage(`{"rate_limits_available":true}`), claudeSubFailClosedWant},
		{"extra_usage absent", json.RawMessage(`{"rate_limits_available":true,"rate_limits":{}}`), claudeSubFailClosedWant},
		{"is_enabled null", json.RawMessage(`{"rate_limits_available":true,"rate_limits":{"extra_usage":{"is_enabled":null}}}`), claudeSubFailClosedWant},
		{"is_enabled absent", json.RawMessage(`{"rate_limits_available":true,"rate_limits":{"extra_usage":{"disabled_reason":"org_level_disabled_until"}}}`), claudeSubFailClosedWant},
		{"undecodable", json.RawMessage(`not json`), claudeSubFailClosedWant},
		{"empty", json.RawMessage(``), claudeSubFailClosedWant},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := claudeSubUsageGate(tc.raw)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("claudeSubUsageGate() error = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("claudeSubUsageGate() error = nil, want %q", tc.wantErr)
			}
			if err.Error() != tc.wantErr {
				t.Errorf("claudeSubUsageGate() error = %q, want %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestClaudeSubRateLimitVerdict(t *testing.T) {
	const wantMessage = "Claude subscription usage limit reached"
	tests := []struct {
		name         string
		raw          string
		wantOverage  bool
		wantErr      bool
		wantLimit    bool
		wantCode     string
		wantResetsAt int64
	}{
		{"allowed", `{"status":"allowed","rateLimitType":"five_hour","resetsAt":1791477000,"isUsingOverage":false,"overageInUse":false}`, false, false, false, "", 0},
		{"recorded allowed shape", `{"status":"allowed","resetsAt":1791477000,"rateLimitType":"five_hour","overageStatus":"rejected","overageDisabledReason":"org_level_disabled_until","isUsingOverage":false,"unifiedWindows":{"five_hour":{"utilization":0.06,"resetsAt":1791477000},"seven_day":{"utilization":0.01,"resetsAt":1791928800}}}`, false, false, false, "", 0},
		{"using overage flag", `{"status":"allowed","isUsingOverage":true}`, true, false, false, "", 0},
		{"overage in use flag", `{"status":"allowed","overageInUse":true}`, true, false, false, "", 0},
		{"rate limit type overage", `{"status":"allowed","rateLimitType":"overage"}`, true, false, false, "", 0},
		{"rejected with overage status", `{"status":"rejected","rateLimitType":"five_hour","overageStatus":"allowed"}`, true, false, false, "", 0},
		{"rejected plain with reset", `{"status":"rejected","rateLimitType":"seven_day","overageStatus":"rejected","resetsAt":1791477000}`, false, false, true, "seven_day", 1791477000},
		{"rejected no overage status", `{"status":"rejected","rateLimitType":"five_hour","resetsAt":1791928800}`, false, false, true, "five_hour", 1791928800},
		{"rejected without reset", `{"status":"rejected","rateLimitType":"five_hour"}`, false, false, true, "five_hour", 0},
		{"undecodable", `not json`, true, true, false, "", 0},
		{"empty", ``, true, true, false, "", 0},
		{"empty object", `{}`, true, true, false, "", 0},
		{"json null", `null`, true, true, false, "", 0},
		{"unknown status", `{"status":"unknown"}`, true, true, false, "", 0},
		{"unknown status with overage fields", `{"status":"surprise","rateLimitType":"five_hour","overageStatus":"rejected","isUsingOverage":false}`, true, true, false, "", 0},
		{"allowed without overage fields", `{"status":"allowed"}`, false, false, false, "", 0},
		{"allowed warning with overage fields", `{"status":"allowed_warning","rateLimitType":"five_hour","overageStatus":"rejected","isUsingOverage":false}`, false, false, false, "", 0},
		{"allowed warning overage available", `{"status":"allowed_warning","rateLimitType":"five_hour","overageStatus":"allowed","isUsingOverage":false,"overageInUse":false}`, false, false, false, "", 0},
		{"allowed warning using overage", `{"status":"allowed_warning","rateLimitType":"five_hour","overageStatus":"allowed_warning","isUsingOverage":true}`, true, false, false, "", 0},
		{"allowed warning overage window", `{"status":"allowed_warning","rateLimitType":"overage","overageStatus":"rejected","isUsingOverage":false}`, true, false, false, "", 0},
		{"allowed warning without overage fields", `{"status":"allowed_warning","rateLimitType":"five_hour"}`, true, true, false, "", 0},
		{"allowed warning without is using overage", `{"status":"allowed_warning","rateLimitType":"five_hour","overageStatus":"rejected"}`, true, true, false, "", 0},
		{"allowed warning without overage status", `{"status":"allowed_warning","rateLimitType":"five_hour","isUsingOverage":false}`, true, true, false, "", 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			overage, limit, err := claudeSubRateLimitVerdict(json.RawMessage(tc.raw))
			if tc.wantErr {
				if err == nil {
					t.Errorf("claudeSubRateLimitVerdict() error = nil, want non-nil")
				}
			} else if err != nil {
				t.Errorf("claudeSubRateLimitVerdict() error = %v, want nil", err)
			}
			if overage != tc.wantOverage {
				t.Errorf("overage = %v, want %v", overage, tc.wantOverage)
			}
			if !tc.wantLimit {
				if limit != nil {
					t.Errorf("limit = %+v, want nil", limit)
				}
				return
			}
			if limit == nil {
				t.Fatalf("limit = nil, want a *UsageLimitError")
			}
			if limit.Kind != UsageLimitKindUsage {
				t.Errorf("limit.Kind = %q, want %q", limit.Kind, UsageLimitKindUsage)
			}
			if limit.Provider != "claude_subscription" {
				t.Errorf("limit.Provider = %q, want %q", limit.Provider, "claude_subscription")
			}
			if limit.Code != tc.wantCode {
				t.Errorf("limit.Code = %q, want %q", limit.Code, tc.wantCode)
			}
			if limit.Message != wantMessage {
				t.Errorf("limit.Message = %q, want %q", limit.Message, wantMessage)
			}
			wantReset := time.Time{}
			if tc.wantResetsAt > 0 {
				wantReset = time.Unix(tc.wantResetsAt, 0)
			}
			if !limit.ResetsAt.Equal(wantReset) {
				t.Errorf("limit.ResetsAt = %v, want %v", limit.ResetsAt, wantReset)
			}
		})
	}
}

func TestClaudeSubRateLimitVerdictUnrecognisedStatus(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{"unknown status", `{"status":"unknown"}`, `unrecognised rate-limit status "unknown"`},
		{"unknown status with overage fields", `{"status":"surprise","isUsingOverage":false,"overageStatus":"rejected","rateLimitType":"five_hour"}`, `unrecognised rate-limit status "surprise"`},
		{"allowed warning without overage fields", `{"status":"allowed_warning","rateLimitType":"five_hour"}`, `unrecognised rate-limit status "allowed_warning" without explicit overage fields`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := claudeSubRateLimitVerdict(json.RawMessage(tc.raw))
			if err == nil || err.Error() != tc.want {
				t.Fatalf("claudeSubRateLimitVerdict() error = %v, want %q", err, tc.want)
			}
			if !errors.Is(err, errClaudeSubUnrecognisedStatus) {
				t.Errorf("error %v does not wrap errClaudeSubUnrecognisedStatus", err)
			}
		})
	}
}

func TestClaudeSubNotificationIsOverage(t *testing.T) {
	tests := []struct {
		text string
		want bool
	}{
		{"You're now using usage credits", true},
		{"you're now using usage credits", true},
		{"  You're now using usage credits to continue", true},
		{"now using extra usage", true},
		{"Now using extra usage for this request", true},
		{"you're now using extra usage", true},
		{"You're now using extra usage credits", true},
		{"usage credits are enabled", false},
		{"", false},
		{"you're now using extra tokens", false},
	}
	for _, tc := range tests {
		t.Run(fmt.Sprintf("%q", tc.text), func(t *testing.T) {
			if got := claudeSubNotificationIsOverage(tc.text); got != tc.want {
				t.Errorf("claudeSubNotificationIsOverage(%q) = %v, want %v", tc.text, got, tc.want)
			}
		})
	}
}

package provider

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func usageFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "usage_limit", name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return string(b)
}

func setUsageLimitClock(t *testing.T, now time.Time, loc *time.Location) {
	t.Helper()
	oldNow, oldLoc := usageLimitNow, usageLimitLocation
	usageLimitNow = func() time.Time { return now }
	usageLimitLocation = func() *time.Location { return loc }
	t.Cleanup(func() { usageLimitNow, usageLimitLocation = oldNow, oldLoc })
}

func TestClassifyUsageLimit(t *testing.T) {
	recv := time.Unix(1789671913, 0)
	tests := []struct {
		name     string
		provider string
		status   int
		body     string
		want     bool
		kind     UsageLimitKind
		code     string
		plan     string
		message  string
	}{
		{"codex http", "codex", 429, usageFixture(t, "codex_http_usage_limit.json"), true, UsageLimitKindUsage, "usage_limit_reached", "plus", "The usage limit has been reached"},
		{"codex ws frame", "codex", 429, usageFixture(t, "codex_ws_usage_limit.json"), true, UsageLimitKindUsage, "usage_limit_reached", "pro", "The usage limit has been reached"},
		{"openai quota", "openai", 429, usageFixture(t, "openai_insufficient_quota.json"), true, UsageLimitKindQuota, "insufficient_quota", "", "You exceeded your current quota, please check your plan and billing details."},
		{"litellm budget", "litellm", 429, usageFixture(t, "litellm_budget_exceeded.json"), true, UsageLimitKindBudget, "400", "", "Budget has been exceeded! Current cost: 12.4, Max budget: 10.0"},
		{"litellm budget wrong provider", "openai", 429, usageFixture(t, "litellm_budget_exceeded.json"), false, "", "", "", ""},
		{"rate_limit_error type", "anthropic", 429, `{"error":{"type":"rate_limit_error"}}`, false, "", "", "", ""},
		{"rate_limit_exceeded code", "openai", 429, `{"error":{"code":"rate_limit_exceeded"}}`, false, "", "", "", ""},
		{"non-429", "codex", 503, usageFixture(t, "codex_http_usage_limit.json"), false, "", "", "", ""},
		{"non-JSON body", "codex", 429, "too many requests", false, "", "", "", ""},
		{"usage_not_included type", "codex", 429, `{"error":{"type":"usage_not_included","message":"m"}}`, true, UsageLimitKindQuota, "usage_not_included", "", "m"},
		{"code as number", "openai", 429, `{"error":{"code":429,"message":"m"}}`, false, "", "", "", ""},
		{"code null", "openai", 429, `{"error":{"code":null}}`, false, "", "", "", ""},
		{"message fallback to body", "openai", 429, `{"error":{"code":"insufficient_quota"}}`, true, UsageLimitKindQuota, "insufficient_quota", "", `{"error":{"code":"insufficient_quota"}}`},
	}
	for _, code := range []string{"credit_balance_exhausted", "organization_spend_limit_exceeded", "project_spend_limit_exceeded", "organization_usage_limit_exceeded"} {
		tests = append(tests, struct {
			name     string
			provider string
			status   int
			body     string
			want     bool
			kind     UsageLimitKind
			code     string
			plan     string
			message  string
		}{"billing code " + code, "openai", 429, fmt.Sprintf(`{"error":{"code":%q,"message":"m"}}`, code), true, UsageLimitKindQuota, code, "", "m"})
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyUsageLimit(tt.provider, &HTTPError{StatusCode: tt.status, Body: tt.body}, recv)
			if !tt.want {
				if got != nil {
					t.Fatalf("expected nil, got %+v", got)
				}
				return
			}
			if got == nil {
				t.Fatal("expected classification, got nil")
			}
			if got.Kind != tt.kind || got.Code != tt.code || got.PlanType != tt.plan || got.Message != tt.message || got.Provider != tt.provider {
				t.Errorf("got %+v", got)
			}
		})
	}
	if classifyUsageLimit("codex", nil, recv) != nil {
		t.Error("nil HTTPError must classify as nil")
	}
}

func TestClassifyUsageLimitResetTime(t *testing.T) {
	recv := time.Unix(1_700_000_000, 0)
	hdr := http.Header{"X-Codex-Primary-Reset-At": {" 1700003000 "}}
	tests := []struct {
		name   string
		body   string
		header http.Header
		want   time.Time
	}{
		{"resets_at wins", `{"error":{"type":"usage_limit_reached","resets_at":1700001000,"resets_in_seconds":50}}`, hdr, time.Unix(1700001000, 0)},
		{"resets_in_seconds relative", `{"error":{"type":"usage_limit_reached","resets_in_seconds":50}}`, hdr, recv.Add(50 * time.Second)},
		{"header fallback", `{"error":{"type":"usage_limit_reached"}}`, hdr, time.Unix(1700003000, 0)},
		{"millisecond resets_at skipped", `{"error":{"type":"usage_limit_reached","resets_at":1700001000000,"resets_in_seconds":50}}`, nil, recv.Add(50 * time.Second)},
		{"zero and negative skipped", `{"error":{"type":"usage_limit_reached","resets_at":0,"resets_in_seconds":-5}}`, hdr, time.Unix(1700003000, 0)},
		{"unparsable header", `{"error":{"type":"usage_limit_reached"}}`, http.Header{"X-Codex-Primary-Reset-At": {"soon"}}, time.Time{}},
		{"all missing", `{"error":{"type":"usage_limit_reached"}}`, nil, time.Time{}},
		{"past kept", `{"error":{"type":"usage_limit_reached","resets_at":1600000000}}`, nil, time.Unix(1600000000, 0)},
		{"quota never has reset", `{"error":{"type":"insufficient_quota","resets_at":1700001000}}`, nil, time.Time{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyUsageLimit("codex", &HTTPError{StatusCode: 429, Body: tt.body, Header: tt.header}, recv)
			if got == nil {
				t.Fatal("expected classification")
			}
			if !got.ResetsAt.Equal(tt.want) {
				t.Errorf("ResetsAt = %v, want %v", got.ResetsAt, tt.want)
			}
			if got.HasReset() != !tt.want.IsZero() {
				t.Errorf("HasReset = %v", got.HasReset())
			}
		})
	}
}

func TestUsageLimitErrorString(t *testing.T) {
	loc := time.FixedZone("TST", 3600)
	now := time.Date(2026, 9, 21, 14, 25, 18, 0, time.UTC) // 15:25:18 TST
	setUsageLimitClock(t, now, loc)
	at := func(y int, m time.Month, d, h, mi int) time.Time { return time.Date(y, m, d, h, mi, 0, 0, loc) }
	tests := []struct {
		name string
		err  *UsageLimitError
		want string
	}{
		{"same day", &UsageLimitError{Kind: UsageLimitKindUsage, Provider: "codex", Model: "codex/luna/high", PlanType: "plus", Message: "The usage limit has been reached", ResetsAt: at(2026, 9, 21, 15, 34)},
			"provider usage limit reached (codex/luna/high, plan: plus): The usage limit has been reached — resets at 15:34 local (in 8m42s)"},
		{"other day", &UsageLimitError{Kind: UsageLimitKindUsage, Provider: "codex", Model: "codex/luna/high", Message: "msg", ResetsAt: at(2026, 9, 22, 9, 10)},
			"provider usage limit reached (codex/luna/high): msg — resets Tue 22 Sep 09:10 local (in 17h44m42s)"},
		{"past", &UsageLimitError{Kind: UsageLimitKindUsage, Provider: "codex", Message: "msg", ResetsAt: at(2026, 9, 21, 9, 0)},
			"provider usage limit reached (codex): msg — resets now"},
		{"quota no reset", &UsageLimitError{Kind: UsageLimitKindQuota, Provider: "openai", Model: "openai/gpt", Code: "insufficient_quota", Message: "You exceeded"},
			"provider quota exhausted (openai/gpt, insufficient_quota): You exceeded — no reset time reported; check your plan and billing"},
		{"quota no code", &UsageLimitError{Kind: UsageLimitKindQuota, Provider: "openai", Message: "m"},
			"provider quota exhausted (openai): m — no reset time reported; check your plan and billing"},
		{"budget", &UsageLimitError{Kind: UsageLimitKindBudget, Provider: "litellm", Message: "Budget has been exceeded!"},
			"provider budget exceeded (litellm): Budget has been exceeded! — no reset time reported"},
		{"usage no reset no message", &UsageLimitError{Kind: UsageLimitKindUsage, Provider: "codex"},
			"provider usage limit reached (codex) — no reset time reported; check your plan and billing"},
		{"unknown provider", &UsageLimitError{Kind: UsageLimitKindBudget},
			"provider budget exceeded (unknown provider) — no reset time reported"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.want {
				t.Errorf("Error() =\n%q\nwant\n%q", got, tt.want)
			}
		})
	}
}

func TestUsageLimitErrorUnwrap(t *testing.T) {
	h := &HTTPError{StatusCode: 429}
	ule := &UsageLimitError{Kind: UsageLimitKindQuota, HTTP: h}
	var got *HTTPError
	if !errors.As(ule, &got) || got != h {
		t.Errorf("errors.As HTTPError failed: %v", got)
	}
	if (&UsageLimitError{}).Unwrap() != nil {
		t.Error("Unwrap with nil HTTP must be nil")
	}
	if a, ok := AsUsageLimit(fmt.Errorf("x: %w", ule)); !ok || a != ule {
		t.Error("AsUsageLimit must find wrapped error")
	}
	if a, ok := AsUsageLimit(nil); ok || a != nil {
		t.Error("AsUsageLimit(nil) must be (nil,false)")
	}
	if _, ok := AsUsageLimit(errors.New("plain")); ok {
		t.Error("AsUsageLimit must reject plain errors")
	}
}

func TestWrapUsageLimit(t *testing.T) {
	recv := time.Unix(1_700_000_000, 0)
	match := &HTTPError{StatusCode: 429, Body: usageFixture(t, "openai_insufficient_quota.json")}
	other := &HTTPError{StatusCode: 429, Body: `{"error":{"type":"rate_limit_error"}}`}
	plain := errors.New("boom")

	if wrapUsageLimit("openai", nil, recv) != nil {
		t.Error("nil must stay nil")
	}
	if got := wrapUsageLimit("openai", plain, recv); got != plain {
		t.Error("non-HTTP error must be unchanged")
	}
	if got := wrapUsageLimit("openai", other, recv); got != error(other) {
		t.Error("non-matching HTTPError must be returned unchanged")
	}
	got := wrapUsageLimit("openai", fmt.Errorf("request: %w", match), recv)
	ule, ok := got.(*UsageLimitError)
	if !ok {
		t.Fatalf("got %T, want *UsageLimitError", got)
	}
	if ule.HTTP != match || ule.Kind != UsageLimitKindQuota {
		t.Errorf("unexpected %+v", ule)
	}
	if again := wrapUsageLimit("openai", fmt.Errorf("outer: %w", ule), recv); again == nil || again.Error() != fmt.Sprintf("outer: %v", ule) {
		t.Error("already-wrapped error must pass through unchanged")
	}
	if again := wrapUsageLimit("openai", ule, recv); again != error(ule) {
		t.Error("idempotent wrap must return identical error")
	}
}

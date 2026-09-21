package provider

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
)

// UsageLimitKind classifies why a provider refused a request for usage reasons.
type UsageLimitKind string

const (
	// UsageLimitKindUsage is a usage window that resets over time (Codex usage_limit_reached).
	UsageLimitKindUsage UsageLimitKind = "usage"
	// UsageLimitKindQuota is a billing or plan limit that needs user action.
	UsageLimitKindQuota UsageLimitKind = "quota"
	// UsageLimitKindBudget is an exhausted litellm budget.
	UsageLimitKindBudget UsageLimitKind = "budget"
)

// UsageLimitError is a provider 429 that retrying cannot fix. It unwraps to the
// underlying *HTTPError so existing status-based handling keeps working.
type UsageLimitError struct {
	Kind     UsageLimitKind
	Provider string    // provider type string, e.g. "codex"
	Model    string    // display identifier; may be empty
	PlanType string    // Codex error.plan_type; may be empty
	Code     string    // error.code if set, else error.type
	Message  string    // provider error.message, else a bounded raw body
	ResetsAt time.Time // zero when unknown
	HTTP     *HTTPError
}

// Unwrap returns the underlying HTTP error, or nil when there is none.
func (e *UsageLimitError) Unwrap() error {
	if e == nil || e.HTTP == nil {
		return nil
	}
	return e.HTTP
}

// HasReset reports whether the provider disclosed a reset time.
func (e *UsageLimitError) HasReset() bool { return !e.ResetsAt.IsZero() }

// AsUsageLimit finds a *UsageLimitError in err's chain.
func AsUsageLimit(err error) (*UsageLimitError, bool) {
	if err == nil {
		return nil, false
	}
	var ule *UsageLimitError
	if errors.As(err, &ule) {
		return ule, true
	}
	return nil, false
}

// Test seams for the clock and zone used when formatting reset times.
var (
	usageLimitNow      = time.Now
	usageLimitLocation = func() *time.Location { return time.Local }
)

const (
	maxResetHorizon      = 30 * 24 * time.Hour
	usageLimitBodyRunes  = 200
	codexResetAtHeader   = "x-codex-primary-reset-at"
	usageLimitReachedTyp = "usage_limit_reached"
)

// quotaTypes and quotaCodes are the billing/plan match sets (each defined once).
var (
	quotaTypes = map[string]struct{}{
		"usage_not_included": {},
		"insufficient_quota": {},
	}
	quotaCodes = map[string]struct{}{
		"insufficient_quota":                {},
		"credit_balance_exhausted":          {},
		"organization_spend_limit_exceeded": {},
		"project_spend_limit_exceeded":      {},
		"organization_usage_limit_exceeded": {},
	}
)

type usageErrorBody struct {
	Type            string          `json:"type"`
	Code            json.RawMessage `json:"code"`
	Message         string          `json:"message"`
	PlanType        string          `json:"plan_type"`
	ResetsAt        *json.Number    `json:"resets_at"`
	ResetsInSeconds *json.Number    `json:"resets_in_seconds"`
}

type usageEnvelope struct {
	Error *usageErrorBody `json:"error"`
}

func (b *usageErrorBody) code() string {
	s := strings.TrimSpace(string(b.Code))
	if s == "null" {
		return ""
	}
	return strings.Trim(s, `"`)
}

// classifyUsageLimit returns a *UsageLimitError when httpErr is a 429 that
// signals an exhausted usage limit, quota or budget; otherwise nil.
func classifyUsageLimit(providerType string, httpErr *HTTPError, receivedAt time.Time) *UsageLimitError {
	if httpErr == nil || httpErr.StatusCode != 429 {
		return nil
	}
	var env usageEnvelope
	// Parse failures are not errors: fall through to the litellm text rule.
	_ = json.Unmarshal([]byte(httpErr.Body), &env)
	body := env.Error
	if body == nil {
		body = &usageErrorBody{}
	}
	code := body.code()

	var kind UsageLimitKind
	switch {
	case body.Type == usageLimitReachedTyp:
		kind = UsageLimitKindUsage
	case inSet(quotaTypes, body.Type) || inSet(quotaCodes, code):
		kind = UsageLimitKindQuota
	case providerType == "litellm" && isLiteLLMBudgetExceeded(httpErr.Body):
		kind = UsageLimitKindBudget
	default:
		return nil
	}

	ule := &UsageLimitError{
		Kind:     kind,
		Provider: providerType,
		PlanType: body.PlanType,
		Code:     code,
		Message:  body.Message,
		HTTP:     httpErr,
	}
	if ule.Code == "" {
		ule.Code = body.Type
	}
	if ule.Message == "" {
		ule.Message = truncateRunes(httpErr.Body, usageLimitBodyRunes)
	}
	if kind == UsageLimitKindUsage {
		ule.ResetsAt = usageResetTime(body, httpErr, receivedAt)
	}
	return ule
}

func inSet(set map[string]struct{}, key string) bool {
	_, ok := set[key]
	return key != "" && ok
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// usageResetTime applies the D3 precedence: body resets_at, body
// resets_in_seconds, then the x-codex-primary-reset-at header.
func usageResetTime(body *usageErrorBody, httpErr *HTTPError, receivedAt time.Time) time.Time {
	limit := receivedAt.Add(maxResetHorizon)
	valid := func(t time.Time, n int64) bool { return n > 0 && !t.After(limit) }

	if body.ResetsAt != nil {
		if n, err := strconv.ParseInt(body.ResetsAt.String(), 10, 64); err == nil {
			if t := time.Unix(n, 0); valid(t, n) {
				return t
			}
		}
	}
	if body.ResetsInSeconds != nil {
		if n, err := strconv.ParseInt(body.ResetsInSeconds.String(), 10, 64); err == nil && n > 0 && n <= int64(maxResetHorizon/time.Second) {
			if t := receivedAt.Add(time.Duration(n) * time.Second); valid(t, n) {
				return t
			}
		}
	}
	if httpErr.Header != nil {
		raw := strings.TrimSpace(httpErr.Header.Get(codexResetAtHeader))
		if n, err := strconv.ParseInt(raw, 10, 64); err == nil {
			if t := time.Unix(n, 0); valid(t, n) {
				return t
			}
		}
	}
	return time.Time{}
}

// wrapUsageLimit replaces err with a *UsageLimitError when it carries a
// matching 429. It is idempotent and returns other errors unchanged.
func wrapUsageLimit(providerType string, err error, receivedAt time.Time) error {
	if err == nil {
		return nil
	}
	if _, ok := AsUsageLimit(err); ok {
		return err
	}
	httpErr := asHTTPError(err)
	if httpErr == nil {
		return err
	}
	ule := classifyUsageLimit(providerType, httpErr, receivedAt)
	if ule == nil {
		return err
	}
	return ule
}

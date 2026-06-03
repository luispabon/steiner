## Request

When the upstream API throttles via a litellm proxy, steiner errors out to the user after 3 quick retries instead of waiting the recommended cooldown and retrying. The retry infrastructure exists but is ineffective for litellm-relayed 429s because litellm does not forward the upstream `Retry-After` HTTP header — it embeds the retry delay only in the JSON error body text.

## Overview

Add litellm-specific retry-after parsing to extract the cooldown delay from 429 error bodies when the `Retry-After` HTTP header is absent. This is scoped purely to litellm provider behavior and must be clearly marked as such in the code.

### What changes

1. **New litellm-specific body parser** (`internal/provider/litellm_retry.go`) — parses "Try again in N seconds" and "retry_after" fields from litellm 429 error bodies. Clearly documented as litellm-specific behavior. Best-effort: if parsing fails, falls through to existing exponential backoff.

2. **Integrate into `classifyRetryError`** — when an `HTTPError` is 429 and the `Retry-After` header is absent, attempt litellm body parsing as a fallback before falling back to exponential backoff.

3. **Budget-exceeded detection** — litellm returns 429 for budget exhaustion ("Budget has been exceeded!") which is not retryable. Detect and skip retry for this case.

4. **Wire provider type into `OpenAICompatConfig`** — pass the `config.ProviderType` through so litellm-specific logic only activates for `ProviderTypeLiteLLM`. Non-litellm providers remain unaffected.

### What does NOT change

- Retry loop mechanics (`withRetry`, backoff, jitter) — unchanged.
- Default retry config values (3 attempts, 250ms initial, 5s max, 30s retry-after cap) — unchanged.
- Non-litellm provider behavior — completely unaffected.
- Config schema — no new config fields.

### Scope boundary

This is purely a litellm provider-type concern. All new code paths must be gated on `ProviderTypeLiteLLM` and clearly commented as litellm-specific behavior. If the provider type is not litellm, the existing behavior is preserved exactly.

## Verification Strategy

- **Formatter**: `gofmt -w <files>`
- **Imports**: `goimports -w <files>` (cheap)
- **Lint**: `golangci-lint run ./...` (medium)
- **Vet**: `go vet ./...` (cheap)
- **Tests**: `go test ./internal/provider/... -run TestLiteLLM` for targeted; `go test ./...` for full suite (medium)
- **Build**: `go build ./...` (cheap)
- **Full check**: `make check` (medium — runs all of the above)

## Decision Log

| Decision | Rationale |
|----------|-----------|
| Scope to litellm only | Standard OpenAI API and other providers send `Retry-After` headers properly; only litellm swallows the upstream header |
| Gate on provider type, not heuristic body matching | Avoids false positives on non-litellm providers that happen to have similar error text |
| Best-effort body parsing | litellm message format may change; fallback to exponential backoff is safe |
| No config changes | Existing retry config (max_attempts, backoff, retry_after_max) is sufficient — the gap is header absence, not config |
| Budget-exceeded = non-retryable | litellm 429 for budget exhaustion is permanent, not transient |

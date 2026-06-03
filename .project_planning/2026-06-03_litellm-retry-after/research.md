## Question

When litellm returns a 429 to clients, does it send a `Retry-After` HTTP header? What is the error response shape? Are there different 429 subtypes that require different handling?

## Findings

### `Retry-After` header behavior

- **Proxy-side rate limits** (TPM/RPM/max_parallel_requests configured in litellm): litellm sends a `Retry-After` header with seconds until the next minute boundary (sliding window reset). This is raised via `parallel_request_limiter.py` as an `HTTPException` with `headers={"retry-after": str(self.time_to_next_minute())}`.

- **Upstream provider 429s** (e.g. OpenAI token-per-minute exceeded): litellm does NOT forward the upstream provider's `Retry-After` header to the client. It consumes it internally for its own retry/backoff logic (`_calculate_retry_after()`), retries up to its configured limit, then returns the final error to the client without the header. The "Try again in N seconds" information is embedded only in the JSON error body message text.

### JSON error response format

All 429s use a stable format:
```json
{"error": {"message": "...", "type": "...", "param": null, "code": "429"}}
```

The `message` field varies by 429 subtype:
- Upstream token limit: raw provider error embedded verbatim, e.g. `"litellm.RateLimitError: RateLimitError: OpenAIException - Token limit is exceeded. Try again in 11 seconds.. Received Model Group=pilot-gpt-52\nAvailable Model Group Fallbacks=None"`
- Proxy RPM/TPM/parallel: `"LiteLLM Rate Limit Handler for rate limit type = key/model_per_key/user/customer/team. Crossed TPM / RPM / Max Parallel Request Limit. current rpm: X, rpm limit: Y, current tpm: Z, tpm limit: W..."`
- Budget exceeded: `"Budget has been exceeded! Current cost: X, Max budget: Y"` (uses `BudgetExceededError`, also 429)

### Model group fallbacks

`"Available Model Group Fallbacks=None"` in the error message means no fallback models were configured. When fallbacks exist, litellm retries transparently server-side and the client never sees the 429. When fallbacks are absent, the 429 is terminal from litellm's perspective — it already exhausted its internal retries.

### Rate limit headers on success

On successful responses, litellm forwards upstream `x-ratelimit-remaining-tokens`, `x-ratelimit-remaining-requests`, etc. These are NOT present on 429 error responses.

## Implications

1. For upstream provider 429s relayed through litellm, our existing `retryAfterDelay()` header parsing finds nothing — the delay must be extracted from the JSON body text as a fallback.
2. Budget-exceeded 429s should not be retried — they are permanent limits, not transient rate limits.
3. Since litellm already retried internally, client-side retries need to respect the upstream's cooldown (often 10-60s), not use sub-second exponential backoff.
4. This is purely a litellm-specific behavior — standard OpenAI API and other providers typically send `Retry-After` headers. Code handling this must be clearly scoped to litellm provider type.

## Risks and Uncertainties

- The "Try again in N seconds" text format comes from upstream providers (e.g. OpenAI, Azure). If providers change their error message wording, body-text parsing could break. Parsing should be best-effort with fallback to generous exponential backoff.
- litellm's internal retry behavior may change across versions, affecting what errors reach clients.

## Sources

- litellm source: `parallel_request_limiter.py`, `router.py`, `proxy_server.py`
- litellm source: `_calculate_retry_after()` in retry logic
- User-observed error from litellm proxy (screenshot)

## Open Questions

None — sufficient for planning.

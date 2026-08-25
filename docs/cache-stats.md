# Cache Hit Rate Tracking

Steiner records prompt-cache token usage on every usage-bearing model response and surfaces a token-weighted cache hit rate. The feature is always-on with no configuration required and stores no prompt or completion content — only token counts and model identity. Advisor calls are included in this recording and now flow into the cache-hit-rate reporting where they were previously invisible.

## Codex cache improvements

Codex Responses requests send three affinity headers to route requests to the same cache shard:
- `session-id`: steiner's per-conversation session ID
- `thread-id`: steiner's per-conversation session ID (also set to the same value)
- `originator`: set to `codex_cli_rs` to identify steiner clients

These headers, derived from a stable session key (`prompt_cache_key`), enable server-side session affinity: traffic from the same conversation stays on one cache shard instead of being load-balanced across different cache servers. This allows later requests to reuse prior prompt prefixes within a session. **Measured improvement: ~68% → ~89% hit rate on gpt-5.4-mini.** This improvement is still valid — see [Superseded claims](#superseded-claims-that-did-not-reproduce) below for distinction from later claims that did not reproduce.

The `prompt_cache_key` field alone (without the affinity headers) was not sufficient to achieve the improvement; the headers are required for the Codex backend to route correctly. The headers are set in `buildResponsesHTTPRequest` (`internal/provider/codex_responses.go`), keyed from the same stable per-conversation ID carried on `ChatRequest.PromptCacheKey`.

An earlier draft also sent `prompt_cache_retention: "24h"`, intending to extend cache lifetime. That parameter is valid on OpenAI's Platform API (`api.openai.com/v1`) but is **unsupported by the Codex/ChatGPT backend** that steiner's OAuth path talks to — a live request confirmed `400 Bad Request: {"detail":"Unsupported parameter: prompt_cache_retention"}`. It has been removed from the Codex request payload and must not be reintroduced on that path. However, the native-OpenAI chat-completions path (openaiWire) does send `prompt_cache_retention: "24h"` for models that support extended cache retention, because OpenAI documents that prompt cache pricing is identical for `in_memory` and `24h` retention, so extended retention is free — which materially helps sparsely-spaced requests where the default 5–10 minute idle timeout would otherwise evict the cache.

### Known upstream quirk: Codex backend still rejects `prompt_cache_retention` on its own (2026-08)

Live as of 2026-08, a subset of Codex/ChatGPT OAuth backend replicas reject requests with `400 {"error":{"param":"prompt_cache_retention","code":"invalid_parameter",...}}` — the OpenAI Platform error shape, not the `{"detail":...}` shape documented above — even though steiner's `responsesWire` never sends this field (confirmed by source read and a live capture of the exact outbound body). This is an upstream defect, not a steiner bug: the same error hits the official Codex app and several unrelated third-party clients, and OpenAI has not acknowledged it (tracked at `openai/codex#39392`). It appears tied to a mid-2026-08 backend migration of `gpt-5.x` models from `prompt_cache_retention` to `prompt_cache_options.ttl`.

The failure is intermittent and per-replica, not per-request-shape: a fresh attempt often succeeds where an identical retry against the same session affinity does not. `responsesWire.RefineRetry` (`internal/provider/wire_responses.go`) detects this exact error shape (`isCodexPromptCacheRetentionRejection`, `internal/provider/codex_cache_retention_retry.go`) and marks it retryable within the client's normal `RetryConfig`; `Client.ChatCompletion`/`Client.streamWithRetry` additionally drop the cache-affinity headers (`session-id`/`thread-id`/`originator`) on the retried attempt so it isn't pinned back to the same bad replica. Remove this workaround once OpenAI fixes the upstream issue.

### Request pacing (min-request-interval)

The `codex.min_request_interval` field allows optional rate limiting between consecutive Codex requests and defaults to `0` (disabled). When set to a positive duration (e.g. `4s`), it enforces a minimum gap between requests and serialises them — on a 60-turn run, a 4s interval adds roughly 4 minutes of wall-clock time. It only affects bursts; it is a no-op for interactive use where think-time between turns already far exceeds any sensible interval.

Earlier documentation claimed that pacing reduced cold-shard overflow, improving hit rate from ~0.78 → ~0.89. This was disproven on 2026-08-25 (see [Superseded claims](#superseded-claims-that-did-not-reproduce) below). Set this field based on your own preferences, not on cache-hit assumptions.

### Transport selection

Codex supports two transports: HTTP (default, `codex.transport: http`) and WebSocket (`codex.transport: websocket`). HTTP is now the default.

The WebSocket transport was originally designed to provide cache-shard stickiness by pinning a connection for its lifetime, but measured testing on 2026-08-25 disproved this benefit (see [Superseded claims](#superseded-claims-that-did-not-reproduce) below). WebSocket remains available as an explicit opt-in for users who prefer it for other reasons, but it has no measurable cache advantage over HTTP with affinity headers. It has no HTTP fallback: because the transport is chosen explicitly, a WebSocket failure surfaces as an error rather than silently degrading to a transport the user did not ask for. Whether to remove it entirely is tracked for v3.0.0 in [issue #567](https://github.com/luispabon/steiner/issues/567), which carries the full measurements.

`/cache-stats` remains the tool to verify actual hit rate per session regardless of which transport is in effect.

### Superseded: claims that did not reproduce (2026-08-25)

Two claims about cache improvement mechanisms were re-measured on 2026-08-25 and did not hold:

**A. Request pacing does not improve cache hits.** Earlier documentation claimed that a 4-second minimum interval prevented cold-shard overflow and improved hit rate from ~0.78 → ~0.89. Re-testing with sustained request rates (each arm given its own `prompt_cache_key`) found:
- Fast arm (1.5s gap): 35 requests sustained over 1m01s at 34.2 req/min, 85.4% hit rate, zero cold requests.
- Paced arm (4s gap): 35 requests sustained over 2m23s at 14.7 req/min, 83.0% hit rate, one cold request (#5).

The burst arm exceeded the claimed 15 req/min overflow threshold for a full minute with zero cold requests. The single cold request in the entire sweep occurred in the paced arm. The earlier ~0.78 → ~0.89 measurement was most likely an aggregate-session artifact: aggregate hit rate is dominated by cold-turn *count*, not per-turn behaviour, and pacing changes a run's wall-clock duration and where compaction lands — this cannot be re-checked without the original harness.

**B. WebSocket transport gives no cache benefit.** Earlier documentation claimed a held-open WebSocket connection provides deterministic shard stickiness targeting ~0.95 hit rate versus HTTP's ~0.89 ceiling. Re-testing found:
- Codex's prompt cache expires on idle at ~5 minutes on **both** transports (survives 4 min, gone by 5). A held-open connection cannot keep a prefix alive, so the stickiness premise is void.
- Warm-turn round-trip: WebSocket median 1515ms vs HTTP median 1402ms (n=8 per arm) — no detectable difference, well within noise.
- TCP+TLS handshake cost: median 14ms (max 35ms) per request; pooled connection costs ~0ms, so the ~14ms gain only applies in the 5-minute window between HTTP's 90s pool expiry and the cache TTL, a narrow and rarely-hit window.
- OpenAI labels its own Codex WebSocket transport experimental.

**C. Warm turn cache read rate is ~95.2%, which is the ceiling.** A session aggregating ~87% hit rate is a weighted mix of ~95% warm turns (where cache is available) and ~0% cold turns (where it is not). The only lever on aggregate hit rate is the *number of cold turns*, not per-turn behaviour. This refocuses what cache-hit-rate metrics can and cannot be improved by.

**Affinity headers are still endorsed:** The ~68% → ~89% improvement from session-affinity headers is unchanged and stands — it came from a prior test with real before/after measurement and has not been re-tested. It is preserved here for clarity that the refutations above do not invalidate it.

### Non-streaming fallback fix

Some Codex models (e.g. `gpt-5.4-mini`) reject non-streaming Responses requests with `400 "Stream must be set to true"`. Steiner previously tried a non-stream call first (when streaming was not preferred), silently discarded that 400, and fell back to streaming — wasting one failed request every turn and suppressing the error, which also depressed the observed cache rate. The agent now detects that specific 400 (`isStreamRequiredError` in `internal/agent/model_call.go`), emits a one-time diagnostic, and latches a per-run flag so subsequent turns skip the doomed non-stream attempt and stream directly. Unrelated 400s are unaffected.

The cache hit rate tracking in this repo now reflects the affinity-header improvement with stable session routing, so the sidebar and `/cache-stats` view measure traffic that benefits from reduced cache misses due to shard affinity.

## Known upstream limitations

Two upstream issues remain visible in cache behavior:

- Trailing content longer than 500 tokens can still miss cache reuse because of a provider-side bug.
- `gpt-5.4-nano` has been observed at 0% cache rates, even with stable routing.

These are provider limitations, not Steiner accounting bugs.

## The cache hit rate metric

**Cache hit rate** is calculated as:

```
hit_rate = CacheReadInputTokens / total_input_tokens
```

Where:

- `CacheReadInputTokens` is the count of input tokens served from cache (read from `cache_read_input_tokens` in the model response).
- `total_input_tokens` is the sum of non-cached input tokens, cache-read tokens, and cache-creation tokens: `non_cached_input + cache_read + cache_creation`. `non_cached_input` is derived by subtracting the cache-read and cache-creation counts from the provider's `prompt_tokens` field, which is always a raw total including any cached portion — true for Anthropic, OpenAI-compatible, and Codex adapters alike. Any consumer of `prompt_tokens` (a future provider adapter included) must subtract cache components before treating it as "uncached" input, or the hit-rate denominator double-counts cache reads and deflates the reported rate.

**Undefined case**: When a time window contains no cache-capable model calls or zero total input tokens, the metric renders as `—` (em-dash), never NaN or an error.

**Zero cache reads**: A call with input tokens but zero cache reads is recorded normally and shows 0.0% in that window's aggregate.

**Provider-specific cache-creation token reporting**: Codex and OpenAI-compatible providers report cache-read tokens only, never cache-creation tokens. This is by design at those providers, not a bug or gap in Steiner's integration. Anthropic reports both cache-read and cache-creation tokens.

## Fixed time windows

Cache hit rates are aggregated into three fixed, non-configurable windows:

| Window | Retention |
|--------|-----------|
| Last hour (1h) | 1 hour |
| Last 24 hours (24h) | 24 hours |
| Last 7 days (7d) | 7 days |

All windows are based on wall-clock time, bucketed hourly. Older data is pruned on load and write to enforce 8-day retention.

## Storage and schema

Observations are stored in a single global JSON file shared across all concurrent steiner processes:

**Location**: `$XDG_STATE_HOME/steiner/cache-stats.json`, falling back to `~/.local/state/steiner/cache-stats.json` if `XDG_STATE_HOME` is not set.

**Placement**: Under XDG *state* (durable analytics), not cache. This ensures the data persists across sessions and is not swept by cache cleanup.

**Lock file**: A zero-byte sibling `cache-stats.json.lock` (mode `0600`) sits next to the data file and carries the write lock. It is created on first write and persists; it holds no data and is safe to delete when no steiner process is running.

**On-disk schema**:

```json
{
  "schema_version": 1,
  "entries": [
    {
      "hour": "2026-06-21T14:00:00Z",
      "provider_alias": "local",
      "provider_type": "openai_compat",
      "model_id": "qwen2.5-coder:14b",
      "non_cached_input_tokens": 500,
      "cache_read_input_tokens": 150,
      "cache_creation_input_tokens": 50
    },
    {
      "hour": "2026-06-21T14:00:00Z",
      "provider_alias": "anthropic",
      "provider_type": "anthropic",
      "model_id": "claude-3.5-sonnet",
      "non_cached_input_tokens": 1000,
      "cache_read_input_tokens": 800,
      "cache_creation_input_tokens": 100
    }
  ]
}
```

Each entry represents one hourly bucket for a given provider-alias, provider-type, and model-id combination. Entries accumulate (sum) all observations in that hour. The `schema_version` field allows for future migrations.

**Retention**: Fixed 8 days (pruned on load and after each write). Observations older than 8 days are dropped.

## Resilience and error handling

- **Missing file**: Starts empty silently on first run (no error).
- **Corrupt file**: Treated as empty; logs a warning but does not block startup.
- **Unknown schema version**: Treated as empty; logs a warning and continues.
- **Never blocks agent execution**: A lock-acquisition failure or write error drops only that single observation from persistence (it still counts in the in-session percentage); it never blocks a model turn or interrupts the agent loop.

## Concurrency and write safety

The global file is shared across concurrent steiner processes. Write safety is enforced through:

1. **OS advisory lock (flock)**: On Unix, an exclusive lock (`LOCK_EX`) is acquired before reading and released after writing. Ensures atomic read-modify-write. The lock is taken on the stable sibling `cache-stats.json.lock`, never on the data file: `flock` binds to the open file description (the inode open at lock time), and each write replaces the data file's inode via temp+rename, so locking the data file would let concurrent writers hold locks on different inodes and lose each other's updates.
2. **Additive delta merging**: Each observation is calculated as a delta (one call's token counts). The on-disk state is re-read before write, and the new observation is added to the existing entries. Concurrent writers do not lose each other's updates.
3. **Non-unix fallback**: On Windows and other platforms without flock, in-process concurrency only is guaranteed (same-process threads are serialized). Cross-process safety is not available.
4. **Graceful degradation**: If a lock cannot be acquired within a short timeout, the observation is dropped from persistence only; the in-session counts are unaffected.

## Surfaces

Cache statistics are surfaced in three ways.

### In-session sidebar field

The `PERFORMANCE` sidebar card includes a `cache hit` field, alongside `duration`, `ttft`, and `tps`, displaying the **current session** token-weighted cache hit rate:

- **Format**: e.g., `78.2%` (session-lifetime percentage) or `—` (before the first cache-capable call).
- **Scope**: Process-lifetime counters for the top-level orchestrator run only (`usagestats.SourceParent`), independent of the global windowed store. Sub-agent and advisor calls are recorded separately and do not feed this figure, so the sidebar shows `—` until the orchestrator itself has made a cache-capable call, even if a sub-agent or advisor call completed first. The `/cache-stats` overlay's windowed rows remain blended across all sources; the source distinction is session-scoped only and does not affect the persisted schema.
- **Updates**: Refreshed after each model response.

### Cache hit rate in sub-agent and advisor tool boxes

Sub-agent delegation tool boxes report the child agent's cumulative cache hit rate (across auto-extension re-runs and `follow_up` calls); advisor tool boxes report each one-shot consultation's per-call rate. Both are computed with the same `usagestats.HitRate` formula as the session and overlay figures, rather than the session-wide aggregate:

- **Collapsed meta line**: Completed headers show metadata in the order `status · model/effort · cache NN.N% · elapsed`. The `model/effort` segment is included only when the runtime model is known, using the existing formatter (e.g. `gpt-5.4-mini/high`), and the cache segment is included only when a rate is available. For example: `✓ complete · gpt-5.4-mini/high · cache 95.2% · 12.4s`.
- **Expanded stats block**: shown as `Tokens: X in / Y out` (X is cache-read + non-cached input + cache-create tokens, Y is completion tokens) and `Cache: NN.N%`.
- **Follow-ups**: Cache figures are cumulative for the child agent: they are accumulated across auto-extension re-runs and carried across `follow_up` calls (via `Spec.PriorTokenUsage`), so a follow-up box reports the child's overall rate rather than that one call's rate. Turn/tool counts remain per call, but token counts are now cumulative (whole-life) across extension re-runs and `follow_up` calls, so a follow-up box's `cache NN.N%` and its token counts describe the same cumulative scope while its turn/tool counts describe that one call.
- **Compaction/escalation caveat**: Compaction and context-escalation model calls within a run do not feed these per-run counters; they report to the session-wide recorder through a separate path. A run that included heavy compaction will not have that portion reflected in its own reported cache rate.
- **Undefined case**: `—` is shown, never `0.0%`, when the run had no cache-bearing usage.

### Compaction banners

Finished compaction banners show the per-request summarizer cache rate in the order `✓ cache NN.N% <elapsed> #count`. The rate comes from the summarizer response usage for that banner's one summarizer request and uses the same `usagestats.HitRate` formula: `cache_read / (input + cache_read + cache_create)`, where `input` is non-cached prompt tokens. It is omitted when the summarizer response carries no usage at all (zero input, cache-read, and cache-create tokens, so the hit-rate total is zero); a response with non-zero input but zero cache reads still renders `cache 0.0%`. In-progress banners never show it.

### /cache-stats overlay

The `/cache-stats` slash command (in interactive mode) opens a read-only overlay displaying aggregated cache statistics across the three fixed windows (last hour, 24h, 7d):

- **Layout**: One table per window, with columns: Provider, Model, Hit rate, Cached / Total.
  - Provider: provider alias and type (e.g., "local (openai_compat)").
  - Model: backend model id (e.g., "qwen2.5-coder:14b").
  - Hit rate: percentage or `—` if no data.
  - Cached / Total: formatted as "X / Y" where X is cache-read tokens and Y is total input tokens, or omitted if no data.
- **No-data state**: When no observations exist for a window, the table shows a message like "No cache data in the last hour".
- **Controls**: Scroll with ↑↓, close with esc (reuses standard report-overlay controls).

### Per-turn usage telemetry (headless/batch runs)

The TUI `/cache-stats` overlay is interactive only and unavailable in headless (`--exec`) runs. To enable per-turn usage reporting for headless runs and batch automation, set the `STEINER_USAGE_TELEMETRY` environment variable to a file path. Steiner will append one JSON line per usage-bearing model response and per WebSocket connection event.

**Recording is off unless `STEINER_USAGE_TELEMETRY` is set.** If the path cannot be opened, telemetry is silently disabled rather than failing the run, since it is diagnostic only. Steiner never reads the file back.

**Model response lines** (`kind: "usage"`):

```json
{"kind":"usage","ts":"2026-08-25T14:23:45.123456789Z","run_id":"batch-job","seq":12,"source":"parent","provider_alias":"codex","provider_type":"codex","backend_model_id":"gpt-5.6-luna","prompt_tokens":5695,"cache_read_tokens":4864,"cache_create_tokens":0,"completion_tokens":120}
```

Fields:
- `kind` — `"usage"` for model responses.
- `ts` — RFC 3339 UTC timestamp with nanosecond precision.
- `run_id` — value of `STEINER_USAGE_TELEMETRY_RUN` environment variable; omitted when unset.
- `seq` — monotonic per-process sequence number, so lines order correctly even when timestamps tie.
- `source` — one of `"parent"`, `"sub_agent"`, `"advisor"`, `"unknown"`: which agent made the call.
- `provider_alias`, `provider_type`, `backend_model_id` — provider and model identity.
- `prompt_tokens` — the provider's raw prompt-token count, **which INCLUDES any cached portion**. To calculate uncached input for a hit-rate denominator, subtract `cache_read_tokens` and `cache_create_tokens` (see [The cache hit rate metric](#the-cache-hit-rate-metric) for details on why this matters).
- `cache_read_tokens`, `cache_create_tokens`, `completion_tokens` — token counts from the response.

**WebSocket connection events** (`kind: "ws"`, Codex only):

When the Codex WebSocket transport is in use, connection-lifecycle events are written to the same file:

```json
{"kind":"ws","ts":"2026-08-25T14:24:15.987654321Z","run_id":"batch-job","event":"reconnect","reason":"request: read response: EOF","cache_key":"key-abc123"}
```

Fields:
- `kind` — `"ws"` for WebSocket events.
- `ts` — RFC 3339 UTC timestamp with nanosecond precision.
- `run_id` — value of `STEINER_USAGE_TELEMETRY_RUN`; omitted when unset.
- `event` — `"dial"` (new connection) or `"reconnect"` (connection re-established after loss).
- `reason` — optional; when present, describes why the reconnect occurred (e.g. error message).
- `cache_key` — optional; the Codex cache key in use, if available.

These events make otherwise-silent reconnects observable and allow aligning connection lifecycle against cache-read collapse on the following turn.

**Usage example:**

```bash
export STEINER_USAGE_TELEMETRY=~/.local/state/steiner/telemetry.jsonl
export STEINER_USAGE_TELEMETRY_RUN="batch-job-2026-08-25"
steiner --exec < task.txt
```

Each line is self-contained and appended atomically. The telemetry file is never parsed, aggregated, or modified by steiner; it exists solely for external analysis or integration with monitoring tools.

## Privacy and data security

Only the following data is stored:

- **Token counts**: non-cached input, cache-read input, cache-creation input (integers only).
- **Model identity**: provider alias, provider type, and backend model id.
- **Timestamp**: hourly bucket (UTC ISO 8601 format).

**Never stored**: prompt text, completion text, user input, file contents, or any other session data. Cache statistics are purely quantitative aggregates keyed by model and provider identity.

# Cache Hit Rate Tracking

Steiner records prompt-cache token usage on every usage-bearing model response and surfaces a token-weighted cache hit rate. The feature is always-on with no configuration required and stores no prompt or completion content — only token counts and model identity. Advisor calls are included in this recording and now flow into the cache-hit-rate reporting where they were previously invisible.

## Codex cache improvements

Codex Responses requests send three affinity headers to route requests to the same cache shard:
- `session-id`: steiner's per-conversation session ID
- `thread-id`: steiner's per-conversation session ID (also set to the same value)
- `originator`: set to `codex_cli_rs` to identify steiner clients

These headers, derived from a stable session key (`prompt_cache_key`), enable server-side session affinity: traffic from the same conversation stays on one cache shard instead of being load-balanced across different cache servers. This allows later requests to reuse prior prompt prefixes within a session. **Measured improvement: ~68% → ~89% hit rate on gpt-5.4-mini.**

The `prompt_cache_key` field alone (without the affinity headers) was not sufficient to achieve the improvement; the headers are required for the Codex backend to route correctly. The headers are set in `buildResponsesHTTPRequest` (`internal/provider/codex_responses.go`), keyed from the same stable per-conversation ID carried on `ChatRequest.PromptCacheKey`.

An earlier draft also sent `prompt_cache_retention: "24h"`, intending to extend cache lifetime. That parameter is valid on OpenAI's Platform API (`api.openai.com/v1`) but is **unsupported by the Codex/ChatGPT backend** that steiner's OAuth path talks to — a live request confirmed `400 Bad Request: {"detail":"Unsupported parameter: prompt_cache_retention"}`. It has been removed from the Codex request payload and must not be reintroduced on that path. However, the native-OpenAI chat-completions path (openaiWire) does send `prompt_cache_retention: "24h"` for models that support extended cache retention, because OpenAI documents that prompt cache pricing is identical for `in_memory` and `24h` retention, so extended retention is free — which materially helps sparsely-spaced requests where the default 5–10 minute idle timeout would otherwise evict the cache.

### Request pacing (min-request-interval)

Cache-shard affinity is best-effort: OpenAI still load-balances a key away from its warm shard when a single key bursts past roughly 15 requests/minute. During rapid agentic work (e.g. `--exec` runs with many quick turns) steiner naturally sends turns only ~1.5s apart, which is enough to trip that overflow and scatter later turns onto cold shards. To keep a session on one shard, the Codex provider enforces a minimum gap between consecutive requests (`codex.min_request_interval`, default `4s`, `0` to disable). It is a no-op for interactive use — think-time between turns already exceeds the interval — and only paces bursts. Adding the throttle on top of the affinity headers moved the measured hit rate from ~0.78 to ~0.89. See `rateLimit` in `internal/provider/codex_responses.go` and the config field in [configuration.md](configuration.md).

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

- **Collapsed meta line**: shown as `cache NN.N%` alongside turn/tool/token counts, e.g. `✓ complete · 4 turns · 12 tools · 8123 tokens · 12.4s · cache 95.2%`.
- **Expanded stats block**: shown as `Tokens: X in / Y out` (X is cache-read + non-cached input + cache-create tokens, Y is completion tokens) and `Cache: NN.N%`.
- **Follow-ups**: Cache figures are cumulative for the child agent: they are accumulated across auto-extension re-runs and carried across `follow_up` calls (via `Spec.PriorTokenUsage`), so a follow-up box reports the child's overall rate rather than that one call's rate. Turn/tool counts remain per call, but token counts are now cumulative (whole-life) across extension re-runs and `follow_up` calls, so a follow-up box's `cache NN.N%` and its token counts describe the same cumulative scope while its turn/tool counts describe that one call.
- **Compaction/escalation caveat**: Compaction and context-escalation model calls within a run do not feed these per-run counters; they report to the session-wide recorder through a separate path. A run that included heavy compaction will not have that portion reflected in its own reported cache rate.
- **Undefined case**: `—` is shown, never `0.0%`, when the run had no cache-bearing usage.

### /cache-stats overlay

The `/cache-stats` slash command (in interactive mode) opens a read-only overlay displaying aggregated cache statistics across the three fixed windows (last hour, 24h, 7d):

- **Layout**: One table per window, with columns: Provider, Model, Hit rate, Cached / Total.
  - Provider: provider alias and type (e.g., "local (openai_compat)").
  - Model: backend model id (e.g., "qwen2.5-coder:14b").
  - Hit rate: percentage or `—` if no data.
  - Cached / Total: formatted as "X / Y" where X is cache-read tokens and Y is total input tokens, or omitted if no data.
- **No-data state**: When no observations exist for a window, the table shows a message like "No cache data in the last hour".
- **Controls**: Scroll with ↑↓, close with esc (reuses standard report-overlay controls).

## Privacy and data security

Only the following data is stored:

- **Token counts**: non-cached input, cache-read input, cache-creation input (integers only).
- **Model identity**: provider alias, provider type, and backend model id.
- **Timestamp**: hourly bucket (UTC ISO 8601 format).

**Never stored**: prompt text, completion text, user input, file contents, or any other session data. Cache statistics are purely quantitative aggregates keyed by model and provider identity.

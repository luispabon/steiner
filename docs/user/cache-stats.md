# Cache Hit Rate Tracking

Steiner records prompt-cache token usage on every usage-bearing model response and surfaces a token-weighted cache hit rate. The feature is always-on and stores no prompt or completion content, only token counts and model identity. Advisor calls are included in recording and reporting.

## The cache hit rate metric

**Cache hit rate** is calculated as:

```
hit_rate = CacheReadInputTokens / total_input_tokens
```

`CacheReadInputTokens` is input tokens served from cache. `total_input_tokens` is non-cached input plus cache-read and cache-creation tokens. A provider's `prompt_tokens` is a raw total including cached input, so cached and cache-created counts are subtracted before calculating the non-cached portion.

When a window contains no cache-capable calls or zero input tokens, the metric renders as `—`. A call with input tokens but zero cache reads is recorded normally and contributes 0.0%. Codex and OpenAI-compatible providers report cache-read tokens but no cache-creation tokens; Anthropic reports both.

## Fixed time windows

Cache hit rates are aggregated into three fixed, non-configurable windows:

| Window | Retention |
|--------|-----------|
| Last hour (1h) | 1 hour |
| Last 24 hours (24h) | 24 hours |
| Last 7 days (7d) | 7 days |

Windows use wall-clock time and hourly buckets. Older data is pruned after 8 days.

## Surfaces

### In-session sidebar field

The `PERFORMANCE` sidebar card includes `cache hit`, alongside `duration`, `ttft`, and `tps`. It shows the current session's token-weighted rate, for example `78.2%`, or `—` before the first cache-capable parent call. It updates after each model response. The sidebar covers the top-level orchestrator; sub-agent and advisor calls do not feed this field.

### Sub-agent and advisor tool boxes

Completed sub-agent and advisor boxes show cache metadata when available, for example `✓ complete · gpt-5.4-mini/high · cache 95.2% · 12.4s`. Expanded stats show token counts and `Cache: NN.N%`. Child rates are cumulative across extension reruns and follow-ups; advisor rates are per consultation. `—` is shown when there was no cache-bearing usage. Compaction and context-escalation calls are not included in these per-run counters.

### Compaction banners

Finished compaction banners show the one summarizer request's cache rate, for example `✓ cache NN.N% <elapsed> #count`. The field is omitted when that response has no usage.

### `/cache-stats` overlay

The `/cache-stats` slash command opens a read-only overlay with one table for each fixed window. Columns are Provider, Model, Hit rate, Cached / Total, Uncached/req, and Cached/req. The last two averages explain whether a rate changed because uncached tokens grew or cached tokens shrank. `—` means no data. Scroll with ↑↓ and close with esc.

### Per-turn telemetry

For headless runs, set `STEINER_USAGE_TELEMETRY` to a file path. Steiner appends one JSON line per usage-bearing response and per Codex WebSocket connection event. Recording is off unless the variable is set. An unwritable path silently disables this diagnostic output, and Steiner never reads the file back.

```bash
export STEINER_USAGE_TELEMETRY=~/.local/state/steiner/telemetry.jsonl
export STEINER_USAGE_TELEMETRY_RUN="batch-job-2026-08-25"
steiner --exec < task.txt
```

Usage records include timestamp, optional run id, source, provider and model identity, raw prompt tokens, cache-read and cache-create tokens, and completion tokens. `prompt_tokens` includes cached input. WebSocket records identify `dial` or `reconnect`, an optional reason, and an optional cache key. Each line is appended atomically and is intended for external analysis.

Structured cache diagnostics are a separate opt-in stream: with `diagnostics.enabled: true` and `diagnostics.streams.cache: true`, one JSONL record per usage-bearing response is written to `<diagnostics.dir>/cache.jsonl`, covering the parent and delegated agents. Records contain model identity, token counts, source and run metadata, plus hashes and prefix comparison counts, never message content or the cache key itself. See the configuration reference for diagnostics settings.

## Codex transport and pacing

Codex uses HTTP by default (`codex.transport: http`). WebSocket is an explicit opt-in (`codex.transport: websocket`) and does not fall back to HTTP when it fails. `/cache-stats` measures the actual result for either transport.

`codex.min_request_interval` optionally enforces a minimum gap between consecutive Codex requests. It defaults to `0` (disabled), serializes bursts when positive, and can add substantial wall-clock time to batch runs. It does not guarantee a higher cache rate.

## Known limitations

- Trailing content longer than 500 tokens can miss cache reuse because of a provider-side bug.
- `gpt-5.4-nano` has been observed at 0% cache rates, even with stable routing.
- Models that do not report cache reads cannot produce a meaningful hit rate.

These are provider limitations, not accounting errors.

## Resilience and persistence failures

A missing file starts empty. A corrupt or unknown-version file starts empty with a warning. A lock-acquisition or write failure drops that observation from persistence only; it never blocks a model turn, and in-session counts remain unaffected.

## Privacy and data security

Only token counts, provider/model identity, and an hourly timestamp are stored. Prompt text, completion text, user input, file contents, and other session data are never stored by cache statistics.

For provider transports, storage schema, diagnostics machinery, and measurement history, see [Cache statistics internals](../internals/cache-stats.md).

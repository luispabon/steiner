# Prompt-cache hit rate audit — 2026-09-27

**Status:** Fixed by this branch: D1–D15 (lossless history, append-only checkpoints, cache reporting, provider parsing/routing, and diagnostics).

Question: steiner's cache hit rates look low next to Pi, DeepSeek harnesses and OpenCode. Do they calculate differently, is our maths broken, or are we genuinely worse?

**Short answer**

1. **Our maths is correct.** Every harness examined uses the same formula (`cache_read / total_prompt`), and our provider parsing matches what the wire actually returns.
2. **Most of the perceived gap is presentation.** Pi's footer shows the *last request's* rate (~96–98% on our own data); steiner shows a *cumulative session* rate that includes the cold first turn (median ~89%, p25 ~80%). Same data, 7–10 points apart. `/cache-stats` additionally blends the advisor (≈0%) into model rows (gpt-6-sol: 95.4% parent → shown as 72%).
3. **But steiner is genuinely leaking cache in a few places, and one of them is a correctness bug.** Sub-agent delegate *extensions* and *follow-ups* re-ingest the whole child history. Every historic `read` result whose file is unchanged on disk (same range) is replaced with a `[file unchanged since turn N]` stub — **including a file's only read**, whose stub then points at itself. That rewrites the prompt prefix (cache miss on everything after the first such read) **and silently deletes those file contents from the child's context**. A turn-budget notice is also superseded in place. Strictly attributed, ≈8% of all uncached input (~+0.8 points hit rate); reproduced end to end.
4. The remaining avoidable misses are provider-side (OpenCode Go gateway switching upstreams mid-conversation; Codex backend partial misses at the same rate Codex CLI sees).

Data: `~/.local/state/steiner/diagnostics/cache.jsonl`, 2026-09-09 → 2026-09-27: 21,662 usage records, 1.20B prompt tokens, 118.2M uncached, token-weighted hit rate **90.2%**. Plus the 8-day `cache-stats.json` store, session logs, OpenCode's local SQLite (Mar–Aug 2026, 14.6k messages), Codex CLI sessions (Apr–Jul 2026, 893 sessions), captured provider bodies, and live experiments.

---

## 1. How each harness calculates and displays it

| Harness | Formula | What the headline number is |
|---|---|---|
| **steiner** | `read / (uncached + read + write)` (`internal/usagestats/report.go` `HitRate`) | Sidebar: **cumulative, parent-only, whole session incl. cold first turn**. `/cache-stats`: token-weighted per provider/model over 1h/24h/7d, **all sources blended** (parent + sub-agents + advisor). |
| **Pi** (`badlogic/pi-mono`) | `cacheRead / (input + cacheRead + cacheWrite)` | Footer `CH x%` = **latest request only** (`coding-agent/src/modes/interactive/components/footer.ts:89-100`). `/session` shows the cumulative figure with the same formula as ours. |
| **DeepSeek-TUI** (`Hmbown/DeepSeek-TUI`) | `hit / (hit + miss)`; scorecard adds writes to denominator | Footer `cache N%` = cumulative session (`tui/session_metrics.rs:445`). Same as ours. |
| **Whale** (`usewhale/DeepSeek-Code-Whale`) | `hit / (hit + miss)` | "~98%" is a README badge, not a measured display. |
| **OpenCode** (`anomalyco/opencode`) | TUI shows **no cache %**. The public stats site uses `cacheRead / (input + cacheRead)` — drops cache *writes* from the denominator (inflates Anthropic-family numbers; identical for OpenAI-compatible). |

Our accounting also checks out on the wire:

* opencode-go returns only `usage.prompt_tokens_details.cached_tokens` (verified live) — exactly what `openai_wire.go` parses. Pi additionally falls back to DeepSeek's `prompt_cache_hit_tokens` and Kimi's top-level `cached_tokens`; worth adding for direct DeepSeek/Moonshot endpoints, irrelevant for our current providers.
* Anthropic adapter adds `cache_creation + cache_read` back into `PromptTokens`; Codex `input_tokens` already includes cached. The `nonCached = prompt − read − create` derivation is consistent everywhere (recorder, delegation boxes, compaction banner, output events).

### Same data, different metric

Computed over our own `cache.jsonl`, conversations with ≥3 requests:

| | Pi-style (last request), median | steiner-style (cumulative), median | cumulative, p25 |
|---|---|---|---|
| parent (75 convs) | **96.3%** | 88.8% | 79.7% |
| sub-agents (802 convs) | **98.0%** | 89.1% | 83.4% |

That is most of the "Pi looks better" gap. Also note hit % is dominated by context size: a 150k-token context with 2k of new content per turn is ~98% whatever the harness; steiner delegates aggressively, so many of its conversations are short sub-agents where the cold first turn weighs more.

### `/cache-stats` blending

The overlay groups by provider+model across sources. From the 8-day store: gpt-6-sol parent 95.4% (67 req) + advisor 0.0% (36 req) → overlay row ~72%. gpt-5.6-sol shows 72.7% for the same reason. The advisor is near-0% by construction (see §4), so any model used as advisor looks broken in the overlay.

---

## 2. Where steiner's uncached tokens actually go

Per conversation (run × source × agent), each request *i* is decomposed:
`growth = max(0, P_i − P_{i−1})` (new content — unavoidable), `lost = min(P_{i−1}, P_i) − R_i` (previously-sent prefix that was not served from cache). Losses ≤2048 tokens are provider block granularity. Losses >2048 are a **rewrite** if the prompt shrank >3% or `shared_prefix_messages` did not grow, else **routing/eviction** (pure append that still missed).

Fleet (all sources, 18 days), share of the 118.2M uncached tokens:

| Cause | Share of uncached | Notes |
|---|---|---|
| Cold first turn per conversation | 3.8% | unavoidable |
| Growth (new tool output / replies) | 49.2% | unavoidable |
| Block granularity | 5.5% | unavoidable |
| **Harness rewrites** | **20.6%** (518 events) | see §3 — mostly fixable |
| Routing / eviction on pure appends | 20.8% (899 events) | provider-side, see §4 |

If rewrites and routing losses were zero the fleet rate would be ~94.2% instead of 90.2%.

Harness-vs-harness on the same gateway: OpenCode's own sessions on `opencode-go/deepseek-v4-flash` lost **32 tokens/turn (1.8% of uncached)**; steiner's `deepseek-v4.1-flash` sub-agents lost **1,669 tokens/turn (44.5% of uncached)**. (Different months and model point-release, so indicative; the causes below are confirmed independently.)

---

## 3. Harness-side cache busting (fixable)

### 3a. Delegate extension re-ingestion — cache bug **and correctness bug**

`internal/delegation/task.go:291` (`runChildToCompletion`) rebuilds the child request with
`req.Prompt.Conversation = agent.ToProviderMessages(state.Conversation)` and calls `runner.Run` again with the **same** `ContextStateManager`. `provider.Message` has no `Ingested` field, so the flag is lost; `initializeRunState` → `PostIngestion` → `normalizeIngestedMessages` then re-runs `observeToolResult` on every historic tool message. For `read` results, `FileTracker.ObserveRead` compares against its existing tracker entry and the **current on-disk hash**, so historic reads — including the *first* full read of a file — are replaced by `[file unchanged since turn N: …]` stubs.

Reproduced deterministically: `internal/agent/reingest_regression_test.go` (worktree, intentionally failing):

```
message 2 rewritten:
  before: {"path":"<a.go>",…,"output":"package a\n\nfunc A() {}\n"}
  after:  {"path":"<a.go>",…,"output":"[file unchanged since turn 4: lines 1-3 of 3 in <a.go>]"}
message 4 rewritten:
  before: {"path":"<a.go>",…,"output":"package a\n\nfunc B() {}\n"}
  after:  {"path":"<a.go>",…,"output":"[file unchanged since turn 2: lines 1-3 of 3 in <a.go>]"}
```

Both reads now point at each other. Scope: `decorateReadObservation` only stubs when the line range matches the tracker entry and the file's generation/hash still match the current disk content, so reads with a different offset/limit, or of files modified since, keep their content. Everything else is stubbed.

**End-to-end reproduction** (real steiner run, private `XDG_STATE_HOME`, `sub_agent.max_turns: 15`, code child on deepseek-v4.1-flash reads `util.go` once then runs 18 one-per-turn bash calls). At the extension (child turn 16) `shared_prefix_messages` fell 30 → 4 and cache read fell to 4,480. Request-body diff, message 4 (the only read of `util.go`):

```
BEFORE: {"path":".../util.go","start_line":1,"end_line":9,"total_lines":9,"file_hash":"951124E6","output":"package main\n\nimport \"strings\"…
AFTER:  {"path":".../util.go","start_line":1,"end_line":9,"total_lines":9,"output":"[file unchanged since turn 1: lines 1-9 of 9 in .../util.go]"}
```

The stub references its own turn; the child no longer has `util.go`'s content anywhere (and `file_hash` is dropped by the re-marshal).

In production this shows as a spike at sub-agent **turn 31** (default `max_turns: 30`): average prompt *shrink* 22.6k tokens (severe case, luna child: 142,790 → 74,923 tokens, cache read 14,848). Strictly attributed (prompt shrank >3% and gap <300 s, the stub fingerprint): **121 events, 6.09M lost tokens = 5.1% of all uncached.**

### 3b. Follow-up re-ingestion — same bug

`internal/delegation/follow_up.go:41` `buildContinuationRequest` uses `agent.ToReplaySafeProviderMessages(conversation)` → same round trip, same re-ingestion (inferred from code; not separately reproduced end to end). 119 lossy first-requests-after-`follow_up` exist, but most follow idle gaps (p75 259 s, p90 716 s) so many are plain TTL expiry; strictly attributed (shrink >3%, gap <300 s): **32 events, 1.84M = 1.6% of uncached**. Other strictly-attributed sub-agent rewrites: 34 events, 0.7%.

### 3c. Turn-budget notice superseded in place

`internal/agent/turn_budget_notice.go` replaces the previous run's `[turn-budget-checkpoint]` message *in place* after an extension (`supersedeOrAppendByContentPrefix`). With max_turns 30→60 the notice fires at turn 42, rewriting history from the old notice (~turn 21) onward: spike at sub-agent turn 43: **40 events with gap <300 s, all with an explicit `shared_prefix_messages` drop, 1.51M lost = 1.3% of uncached** (no prompt shrink — the replacement notice is similar length).

### Fixes

* Extension: pass the real conversation via `req.SourceConversation = state.Conversation` (already supported by `initializeRunState`, preserves `Ingested`/`Retention`/`Source`) instead of the provider round trip; or make `restoreIngestedToolState` the default for any tool message that already has a `Turn` from a prior run. Add a test asserting byte-identical historic messages across an extension and across a follow-up.
* Follow-up: same — keep agent-level messages; apply replay-safety without dropping `Ingested`.
* Budget notice: append a new notice rather than superseding the old one (or leave the old text untouched and append).
* Expected gain (strict attribution): ≈8% of uncached input (5.1 + 1.6 + 1.3), ~9.4M tokens over 18 days, ~+0.8 points fleet hit rate (90.2% → ~91.0%) — and, more importantly, children stop losing file contents at extension/follow-up boundaries.
* Note on the broader "rewrite" bucket in §2 (20.6%): it uses a looser heuristic (no prefix growth *or* shrink) and on pre-#793 builds counts every new-run turn, so it includes idle-TTL misses; the strict figures above are the defensible ones.

### Checked and fine

* Compaction replays the identical prefix and appends the instruction (`compaction_escalation.go:159-176`) — cache-friendly.
* Read annotations at ingestion time don't rewrite history (only the re-ingestion path above does).
* `ReplaySafeConversation`, reasoning echo-back, prompt-suffix handling are append-stable for the configured models.
* Parent prefix: 192/216 historic rewrites stayed warm on the static prefix (previous analysis) — the parent path is healthy apart from compaction.

---

## 4. Provider-side losses

### OpenCode Go gateway upstream switching (DeepSeek)

* The Go gateway now **rejects** requests without `x-opencode-session` (`400 MissingSessionID: "cannot be routed efficiently"`). Steiner sends it (`cmd/steiner/runtime_build_provider.go:50`) but uses the **parent's** session ID for the parent and every sub-agent (`internal/delegation/registry.go:350-352`). OpenCode itself sends a per-(sub)session ID plus `x-parent-session-id`.
* Gateway source (`packages/console/app/src/routes/zen/util/handler.ts`): the session ID keys a sticky-provider record per model and seeds the provider hash; upstream selection can still change on TPS/TPM/budget conditions.
* Response header `x-opencode-endpoint-id` exposes the upstream. Observed live: `orcarouter`, `thesean`, `luminal`, `valarai` all serving `deepseek-v4.1-flash`. Every observed switch caused a miss:
  * A/B round 1 (4 convs/arm, 14 turns): one request in the shared-session arm went `orcarouter → thesean` → R=0 at 88k, then back → partial. Unique-session arm: 0 losses (one conv pinned to `luminal` throughout).
  * A/B round 2 (6 convs/arm, 18 turns): 0 switches, 0 losses in both arms.
  * Real steiner traffic captured today (202 joined requests): one sub-agent switched `orcarouter → valarai` mid-conversation (lossy); same-endpoint losses 2/188.
* Verdict: upstream switching is real, intermittent and load-dependent. Whether per-agent session IDs reduce it is **plausible from the gateway code but not proven** by this sample. Low-risk change: send `X-Opencode-Session = <parent>-<agentID>` for children plus `x-parent-session-id`, mirroring OpenCode.

### Codex

* Codex warm pure-append turns lose cache ~5% of the time (partial misses, 19.6M tokens over 18 days, ~17% of uncached). **Codex CLI shows the same or worse on the same backend**: 3.0% (gpt-5.4-mini), 7.9% (gpt-5.4), 10.5% (gpt-5.5) lossy warm turns. Not a steiner problem.
* Hypothesis refuted: children share one `prompt_cache_key` per agent *type* (`internal/delegation/cache_keys.go`), so parallel `code` children share a Codex routing key — I suspected OpenAI's ~15 RPM per key overflow. Loss rate does **not** rise with requests/min on the key (5.1% at 1–5 rpm, 9.1% at 11–15, 1.5% at 26+).
* Idle TTL expiry (gap ≥5 min) is minor: 54 events, 1.2M tokens.

### Advisor

* 113 calls, ~1.7% hit. 54 of 64 repeat calls were >5 min apart (TTL expired); the first call per run can't hit because the advisor uses its own system prompt, flattened history and usually a different model from the parent. Live test with luna advisor, calls 15 s apart: second call hit its whole prior prefix (R=24,320 of 48,751) — mechanics work. Cost ≈3% of uncached; mainly a display problem (§1).

---

## 5. Recommendations (priority order)

1. **Fix extension/follow-up re-ingestion** (§3a/3b) — correctness first, cache second. Add byte-identity tests across both boundaries.
2. **Stop superseding the turn-budget notice in place** (§3c).
3. **Display**: show both a per-request (last / warm-turn) and session figure, e.g. sidebar `cache 97% · 89% session`; in `/cache-stats`, split or exclude `advisor` source rows (the store already has `source` since schema v2).
4. **opencode-go**: per-agent `X-Opencode-Session` + `x-parent-session-id` (cheap, mirrors OpenCode; benefit unproven).
5. Optional parser hardening: fall back to `prompt_cache_hit_tokens` (DeepSeek direct) and top-level `cached_tokens` (Kimi) like Pi does.
6. Diagnostics: record `x-opencode-endpoint-id` (and a count of messages) in `cache.jsonl` so upstream switches and rewrites are attributable without heuristics.

## Artifacts

* `docs/research/prompt-cache-audit/decomp.mjs` — per-request growth/lost decomposition (steiner `cache.jsonl` or OpenCode SQLite).
* `…/classify.mjs` — fleet classification into first-turn/growth/granularity/rewrite/routing.
* `…/lossturns.mjs` — loss-turn characterisation.
* `…/routing_ab.py`, `ab1.jsonl`, `ab2.jsonl` — gateway session-ID A/B harness and results.
* `internal/agent/reingest_regression_test.go` — passing regression for lossless carried history across the provider round trip; `internal/delegation/follow_up_native_test.go` covers lossless native follow-up history.

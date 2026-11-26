# Live compaction bench

## Purpose

`scripts/compaction-bench.mjs` compares continuity and cache token costs with immutable summary segments off and on (#1042). It uses Node's standard library and makes live model calls.

The entry file holds `main` and the direct-run guard; the work is split across sibling `compaction-bench-*.mjs` modules for flags, fixtures, parsing, scoring, reporting and run execution. The entry re-exports their names, so `scripts/compaction-bench_test.mjs` imports a single path. `scripts/compaction-bench-compare.mjs` compares the `raw.json` files of several invocations.

## Safety and cost

The bench refuses to run without `STEINER_LIVE_BENCH=1`. Provider calls spend real tokens, including two extra probe calls per Anthropic alias. Runs use fresh scratch git repositories and never pass `--unsafe`. `--trust-project-config` applies to the run; it does not persist trust or disable the sandbox. The report shows the sandbox setting from `steiner config`, or `not verified` if it could not be read.

Run artifacts can contain prompts and model output. Keep the output directory private and review artifacts before sharing them. The bench report includes only the experiment environment values, not the full environment; child logs and output can still contain sensitive data. A run that exceeds `--run-timeout-minutes` is killed with `SIGKILL` together with its whole process group, so no descendant keeps running. The same group kill runs when a run exits on its own, and `SIGINT` or `SIGTERM` kills every live run's process group and removes the scratch directories before exiting with 130 or 143.

## Prerequisites

- Node.js, git, and a `steiner` binary available on `PATH` or supplied with `--steiner`.
- Working model aliases and provider authentication in the global user config. The bench config overrides the project config layer, not the global config, and merges model definitions by alias.
- To set experiment limits, copy `.env.dist` to `.env` and load it with direnv's `dotenv_if_exists`, or export the variables directly. Steiner does not read `.env` itself.

The bench sets `STEINER_EXPERIMENT_SEGMENTED_COMPACTION` to `0` or `1` for each mode and passes through these optional limits:

| Environment variable | Default |
| --- | --- |
| `STEINER_EXPERIMENT_SEGMENT_MAX_COUNT` | 8 |
| `STEINER_EXPERIMENT_SEGMENT_MAX_CHAIN_PERCENT` | 12.5 |
| `STEINER_EXPERIMENT_SEGMENT_MAX_POST_PERCENT` | 50 |

Percentages use the compaction budget, the smaller of the context window and effective compaction threshold.

## Usage

```sh
STEINER_LIVE_BENCH=1 node scripts/compaction-bench.mjs \
  --steiner /tmp/steiner \
  --model hy4-flash-anthropic,hy4-flash \
  --anthropic-alias hy4-flash-anthropic \
  --context-window 1000000 --threshold 560000 --seed 7 \
  --repeat 2 --out /tmp/bench-out
```

`--model` accepts configured aliases and `provider/id` refs such as `opencode-go/beam-free`. A ref is benched under the synthesized alias `bench--<provider>--<id>`: the private config pins `provider` and `id`, and the run, its output directory and its report label all use that alias. A plain alias keeps its global settings through the config merge. The id may contain later slashes or a colon (`p/vendor/model`, `p/name:tag`) as long as it is non-empty and stays on one line; the config writes it quoted, and characters outside `[A-Za-z0-9._-]` become dashes in the alias, so `p/vendor/model` runs as `bench--p--vendor-model`. Two refs whose aliases collide are a usage error, and so is the same ref repeated in `--model`. `--anthropic-alias` and `--informational-alias` take the values exactly as written in `--model`.

| Flag | Meaning | Default |
| --- | --- | --- |
| `--model <a,b>` | Aliases or `provider/id` refs, comma-separated | required |
| `--anthropic-alias <a,b>` | Models with Anthropic cache weights and a cache probe | none |
| `--informational-alias <a,b>` | Models without a pass or fail verdict | none |
| `--modes <a,b>` | Subset of `off,on` | `off,on` |
| `--threshold <n>` | Compaction threshold in tokens for each model | 15000 |
| `--context-window <n>` | `advanced.limits.context_window` for each model | alias setting |
| `--seed <n>` | Fixture seed; repeat `r` uses `seed + r - 1` | the repeat number |
| `--files <n>` | Chain length, 4 to 99 | 24 |
| `--pad-tokens <n>` | Approximate filler tokens per file, 0 to 16000 | 3000 |
| `--max-turns <n>` | Turn limit per run | 80 |
| `--repeat <n>` | Runs per alias and mode | 1 |
| `--tolerance <n>` | Allowed percent change in continuity and cost | 5 |
| `--run-timeout-minutes <n>` | Kill a run after this many minutes | 30 |
| `--steiner <path>` | Steiner binary | `steiner` |
| `--out <dir>` | Output directory | a temporary directory |
| `--help` | Print usage | |

Exit codes are 0 for completion, 1 for the live-call guard or an uncaught error, 2 for usage errors, 3 if a benchmark run failed, and 130 or 143 when `SIGINT` or `SIGTERM` interrupts the bench. A failing verdict or probe alone does not set exit code 3. Values of `--anthropic-alias` and `--informational-alias` must also appear in `--model`; anything else is a usage error. `--threshold` must be lower than `--context-window` when both are set, because steiner rejects a threshold at or above the effective window. `--context-window` writes `context_window` under `advanced.limits`, so steiner's resolver may still report another window if the value is ignored; `raw.json` records what the run actually reported. `--pad-tokens` above 16000 is a usage error: the read tool caps a single output at 65536 runes, so a larger file would be truncated on read.

## How a run works

For each model, repeat, and selected mode, the bench generates a seeded chain in a fresh scratch repository and starts one `steiner --exec` without `--resume`. Scratch repositories are created under `.steiner/tmp` of the working directory the bench starts in. The sandbox rewrites a tool's `/tmp` path to `<project root>/.steiner/tmp/sandbox-tmp/<id>/...`, so a scratch repo in the host tmp dir would be read from a rewritten path that holds no fixtures. If that directory cannot be created, the bench falls back to the host tmp dir and records the fallback in `raw.json`; sandboxed tools then cannot read the fixtures and their runs fail as invalid. Off and on use the same seed for each repeat. With `--seed`, repeat `r` uses `seed + r - 1`, so separate invocations at different thresholds can share fixtures for a paired comparison; without it the repeat number is the seed. Mode order alternates per repeat: repeat 1 runs off then on, repeat 2 runs on then off, and so on, so the second mode of a repeat does not start from a cache entry the first mode just warmed. Every run's task prompt opens with `Run id: <nonce>`, so no two runs send a byte-identical first request. The invocation passes `--model`, `--max-turns`, `--compaction-log-file`, `--log-file`, `--config`, and `--trust-project-config`. Its private config sets the alias's `models.definitions.<alias>.advanced.limits.compaction_threshold_tokens`, optionally `context_window`, and enables the cache and provider diagnostics streams outside the project root.

Files `notes/f01.txt` through `notes/fNN.txt` each contain a code, a `NEXT` pointer, and filler. One file after `f03` in the second half corrects the code for `f03`, so the correction never lands in `f01` to `f03`. The task asks the model to follow the chain one file at a time, never re-read, list, or search the directory, and answer with `fNN: CODE` lines applying corrections.

## Scoring and validity

Continuity counts exact code matches per file. `f03` only counts if the corrected code is present and the old code is absent from the answer. Each lost code is attributed to the first compaction after its file was read, as `fNN@cN`, or marked unattributed. Recall is invalid if any read of a file follows a compaction that came after that file's first read, so context dropped between the reads cannot be recovered from a re-read.

The bench reads file paths, model-call usage, and applied compactions from the session log, and `segment_decision` records from the compaction log. Failed model calls (`success` not true) are skipped, and sub-agent scoped calls are skipped for cache economics and read scoring. A tool-call argument value references the fixtures when its `notes` segment starts the value or follows whitespace, a quote, `=`, `:`, `(` or `./`, so `release-notes/`, `footnotes/` and `docs/notes/` do not count, or when it holds an absolute path to a fixture file, whose `notes` segment is exact. An absolute path that ends at a `notes` directory counts only when it is the current run's own notes directory, which the bench passes to the parser; without that directory such a path is not judged. Any main-agent tool call other than the read tool, and any sub-agent call including one through the read tool, that references the fixtures counts as fixture access outside the main-agent read tool. Rebases exclude `chain-start`; a `plain` decision counts as both a rebase and a plain fallback.

An on run is invalid with fewer than four compactions or no append decision. An off or on run is invalid with no parsed final answer, or when it accessed a fixture outside the main-agent read tool. A failed or invalid run on either side makes the verdict `INVALID`, with reasons prefixed `off: ` or `on: `. An off mode whose median exact count is zero also makes the verdict `INVALID`, since continuity compares the two modes relatively and `0/N` against `0/N` would otherwise pass. The off and on medians are incomparable if their compaction counts differ by more than one. A single-mode run is also incomparable. Repeat metrics are medians of reported numeric values over runs that did not fail; if every run failed the medians stay empty. Loss points are combined across runs. A run stopped by `--max-turns` stays scored: its table row counts it and the verdict lists how many runs stopped that way, without invalidating them.

Cache data is reported for a run only when every model call reports it. Otherwise its cache metrics are unavailable. Cost and cached tokens fold in the summarizer calls of applied compactions alongside the main turns, and per-turn cost divides by the main-turn count. The cost proxy uses `uncached + 1.25*write + 0.1*read` for Anthropic aliases and `uncached + 0.1*read` for other aliases. This is a relative token estimate, not billing.

For each `--anthropic-alias`, two one-turn probe calls with one shared prompt test cache control. The prompt opens with a per-probe nonce, so the first call writes cache even when a recent run or a second alias already warmed identical probe text. The probe passes if the first call reports cache creation and the second reports a cache read. A failed probe reports `probe calls failed: exit <code>` plus, when the stderr text matches, one fixed classification: `auth error (401)`, `forbidden (403)`, `not found (404)`, `bad request (400)`, or `timeout`. Raw stderr text never reaches the report, progress output, or `raw.json`. A failed probe prevents a cache verdict for that alias.

## Output and verdicts

`results.md` is written to the output directory and printed to stdout. Its header records the UTC timestamp, binary path, threshold, context window, seed, settings, on-mode experiment environment, and sandbox status. Per-alias tables show turns, max turns hit, compactions, emergency compactions (applied), compaction failures, emergency failures (recorded stages, lower bound), rebases, plain fallbacks, maximum segments, retained-tail median and max, cached and uncached input tokens, cost proxy per turn, wall clock, summed call duration, TTFT and call-duration p50/p95, provider retries, rate-limited failures (429), provider error outcomes, and continuity, followed by verdict reasons and an Anthropic probe line when applicable. Progress goes to stderr.

Wall clock is measured by the bench around the spawned process. Summed duration and the duration percentiles come from the provider stream's `duration_ms`, and TTFT percentiles from its `ttft_ms`. That stream covers every provider call, so it includes compaction summary calls and failed calls, its duration spans retry waits, and it may include delegated or advisor calls that share the process. Its scope therefore differs from the planned main plus compaction scope: it can count sub-agent work that delegation produces, and no run disables delegation to narrow it. Treat it as a cost and latency signal, not main-conversation turn time. `raw.json` records the choice per run as `timingSource`: `provider`, `session` when the session log's main and compaction calls supplied the numbers because the provider stream reported none, or null when nothing measured a timing. A missing measurement stays null rather than zero.

Provider retries sum `attempts - 1` over the copied provider stream. Rate-limited failures count records whose final `error_class` is `rate_limited`: a call that hit a 429 and then succeeded reports outcome `retried` without an error class, so early 429s are not counted. Error outcomes count `exhausted` and `cancelled` final outcomes. The retained tail is the `retained_tokens` of the last applied stage of each applied compaction, and a stage that omits the field counts as zero; a trailing failed stage never supplies it. Emergency compactions count applied compactions whose stages include the `emergency` stage. A compaction that adopts no state emits a skipped event with `compaction_result: failed`; those are counted as compaction failures and never enter the applied counts. `emergencyFailures` is a lower bound: it counts failed compactions whose recorded stages include the `emergency` stage, and production omits that stage for some emergency failures, so the true count can be higher.

Cached and uncached input tokens and the cost proxy fold in the summarizer usage of applied compactions only. A failed compaction's event carries no usage fields, so every summary call of that failed compaction is missing from the token and cost totals rather than estimated; cost is therefore a lower bound in any run with a failed compaction.

`raw.json` contains run records and settings, plus `scratchBase`, which is `worktree` when scratch repos were created under the work tree's `.steiner/tmp` and `tmp` when the bench fell back to the host tmp dir. Each alias entry carries its `alias` (the report label), the original `ref`, and the synthesized `provider` and `id` when the ref was a `provider/id` ref. Each run record carries the metrics above, plus two limit pairs: `configuredContextWindow` and `configuredThresholdTokens` are what the bench asked for, and `observedContextWindow` and `observedThresholdTokens` are what the run's compaction diagnostics reported, or null when the run reported none. The compare script groups by observed when present, otherwise configured, and flags any run whose observed limit differs from the configured one, so a value steiner ignored stays visible instead of being relabelled as observed. Run records also carry `compactionFailures` and `emergencyFailures` separately from the applied counts, and `timingSource` (`provider`, `session`, or null) naming which stream produced the timing metrics. Per-run stdout, stderr, session log, compaction log, provider stream, and cache stream copies are under `<out>/<alias>/<mode>-r<N>.*` when available. Session logs and diagnostics streams keep three rotated generations, and every generation is copied with its `.1`, `.2`, `.3` suffix; the parser reads them oldest first, so early events survive rotation. The report includes the on-mode flag and only limit variables set in the environment.

Verdict precedence is `INVALID`, `INCOMPARABLE`, `INFORMATIONAL`, then `FAIL`, `CONTINUITY_ONLY` or `PASS`. A failed or invalid run, or invalid recall, on either side yields `INVALID`, as does an off side that recalled nothing. After validity and comparability checks, an informational alias gets no pass or fail. Otherwise `FAIL` means on continuity is below off by more than the tolerance, or, when cache is checked, on cost per turn exceeds off by more than the tolerance. Missing cache reporting or a failed Anthropic probe gives `no cache verdict`; cost is not judged, continuity still is, and a continuity result within tolerance is labelled `CONTINUITY_ONLY`. `PASS` requires both continuity and the cost proxy within tolerance.

## Comparing invocations

`scripts/compaction-bench-compare.mjs` reads the `raw.json` files of several bench invocations and prints one table per context window and model, ordered by threshold, with per-run values rather than mode medians. Each row also carries the run's effective seed and the `raw.json` it came from: with `--seed` the seed is the bench seed plus the repeat minus one, and without it the seed is the repeat number, so the repeat index that a phase script encodes only in `--seed` stays readable in one table. A `limits` column names any run whose observed window or threshold differs from the configured value, marks a run `cfg` when it lacks an observed window and/or threshold and so falls back to the configured values, and a footnote counts both. A `raw.json` path cell escapes `|` and `\` and folds CR/LF to a space so a path cannot break the table; the continuity cell escapes a failed or invalid run's reason the same way. Arguments are bench `--out` directories, `raw.json` files, or a directory to scan recursively.

```sh
node scripts/compaction-bench-compare.mjs .steiner/bench/1043/phase0
node scripts/compaction-bench-compare.mjs run-a run-b run-c/raw.json --json
```

`--json` prints `{ sources, rows }`, where every row is one run with its model, ref, source, effective fixture seed, chain `files`, `padTokens`, `maxTurns`, resolved window and threshold (plus the configured and observed pairs and the `windowMismatch` and `thresholdMismatch` flags), mode, repeat, continuity fields, max-turn hit, applied compaction and emergency counts, compaction and emergency failure counts, `timingSource`, retained-tail median and max, wall clock, summed duration, provider retries, final 429 and error outcomes, cached and uncached input tokens, and cost proxy. The Markdown footnote states that seed recovers the fixture seed the run used, that the timing columns cover all provider calls, retry waits included, that `timingSource` names the stream used, that 429 counts final failures only, that `emergency fail` is a lower bound over recorded stages, that a `cfg` limits cell means the run lacks an observed window and/or threshold, and that failed summary tokens are absent. Exit codes are 0 for success, 2 for a usage error, a missing path, an unparsable `raw.json`, or no `raw.json` found, and `--help` prints usage and exits 0. This script makes no provider calls and needs no `STEINER_LIVE_BENCH`.

## Provider notes

Mark aliases using Anthropic cache accounting with `--anthropic-alias`; all other aliases use the other cost formula. The probe tests the actual route rather than assuming cache support. Use `--informational-alias` for `claude_subscription`: the Claude CLI manages the session and its caching, so its results should not be judged as a pass or fail.

Provider setup guidance (confirm details with your provider):

- Anthropic route: define a provider with `type: anthropic`, `base_url: https://opencode.ai/inference/go/anthropic/v1`, the OpenCode Go key via `api_key_env`, and an `X-Opencode-Session` header. Add an `Authorization: Bearer` header only if the route rejects `x-api-key`. Pass the alias with `--anthropic-alias`.
- OpenCode Zen pay-as-you-go is the fallback Anthropic route if the Go route does not honour `cache_control` (the probe reports this).
- Free OpenCode models are chat-completions only and may return 403 outside the OpenCode client. They still give continuity and compaction stats, but no Anthropic cache verdict.
- `claude_subscription` sends no `cache_control` (the Claude CLI owns caching), so it cannot test the segment cache breakpoint.

## Limitations

- A fast run understates the effects of cache expiry during idle gaps in real sessions. Anthropic prompt cache entries expire after 5 minutes without use.
- Small or verbose models may not finish the chain within `--max-turns`.
- Loss attribution identifies the first compaction after a read, not proof that compaction caused the loss.
- The cost proxy is not a provider bill.

## Tests

```sh
node --test scripts/compaction-bench_test.mjs scripts/compaction-bench-compare_test.mjs
# Or run the script test suite:
make test-scripts
```

The bench and compare tests use fake runners, a fake `--steiner` script, local `node` subprocesses, and temporary `raw.json` fixtures; they do not make live provider calls.

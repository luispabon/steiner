# Live compaction bench

## Purpose

`scripts/compaction-bench.mjs` compares continuity and cache token costs with immutable summary segments off and on (#1042). It uses Node's standard library and makes live model calls.

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
  --repeat 2 --out /tmp/bench-out
```

Replace the example aliases with configured aliases. `--model` is required and accepts a comma-separated list. Other flags:

| Flag | Meaning | Default |
| --- | --- | --- |
| `--anthropic-alias <a,b>` | Aliases with Anthropic cache weights and a cache probe | none |
| `--informational-alias <a,b>` | Aliases without a pass or fail verdict | none |
| `--modes <a,b>` | Subset of `off,on` | `off,on` |
| `--threshold <n>` | Compaction threshold in tokens for each alias | 15000 |
| `--files <n>` | Chain length, 4 to 99 | 24 |
| `--pad-tokens <n>` | Approximate filler tokens per file, 0 to 16000 | 3000 |
| `--max-turns <n>` | Turn limit per run | 80 |
| `--repeat <n>` | Runs per alias and mode | 1 |
| `--tolerance <n>` | Allowed percent change in continuity and cost | 5 |
| `--run-timeout-minutes <n>` | Kill a run after this many minutes | 30 |
| `--steiner <path>` | Steiner binary | `steiner` |
| `--out <dir>` | Output directory | a temporary directory |
| `--help` | Print usage | |

Exit codes are 0 for completion, 1 for the live-call guard or an uncaught error, 2 for usage errors, and 3 if a benchmark run failed. A failing verdict or probe alone does not set exit code 3. Values of `--anthropic-alias` and `--informational-alias` must also appear in `--model`; anything else is a usage error. `--pad-tokens` above 16000 is a usage error: the read tool caps a single output at 65536 runes, so a larger file would be truncated on read.

## How a run works

For each alias, repeat, and selected mode, the bench generates a seeded chain in a fresh scratch repository and starts one `steiner --exec` without `--resume`. Off and on use the same seed for each repeat. Mode order alternates per repeat: repeat 1 runs off then on, repeat 2 runs on then off, and so on, so the second mode of a repeat does not start from a cache entry the first mode just warmed. Every run's task prompt opens with `Run id: <nonce>`, so no two runs send a byte-identical first request. The invocation passes `--model`, `--max-turns`, `--compaction-log-file`, `--log-file`, `--config`, and `--trust-project-config`. Its private config sets the alias's `models.definitions.<alias>.advanced.limits.compaction_threshold_tokens` and enables the cache diagnostics stream outside the project root.

Files `notes/f01.txt` through `notes/fNN.txt` each contain a code, a `NEXT` pointer, and filler. One file after `f03` in the second half corrects the code for `f03`, so the correction never lands in `f01` to `f03`. The task asks the model to follow the chain one file at a time, never re-read, list, or search the directory, and answer with `fNN: CODE` lines applying corrections.

## Scoring and validity

Continuity counts exact code matches per file. `f03` only counts if the corrected code is present and the old code is absent from the answer. Each lost code is attributed to the first compaction after its file was read, as `fNN@cN`, or marked unattributed. Recall is invalid if any read of a file follows a compaction that came after that file's first read, so context dropped between the reads cannot be recovered from a re-read.

The bench reads file paths, model-call usage, and applied compactions from the session log, and `segment_decision` records from the compaction log. Failed model calls (`success` not true) and sub-agent scoped calls and reads are skipped, since they are not part of the main conversation's cache economics. Any other tool call whose arguments name the `notes/` directory counts as fixture access outside the read tool. Rebases exclude `chain-start`; a `plain` decision counts as both a rebase and a plain fallback.

An on run is invalid with fewer than four compactions or no append decision. An off or on run is invalid with no parsed final answer, or when it accessed a fixture outside the read tool. A failed or invalid run on either side makes the verdict `INVALID`, with reasons prefixed `off: ` or `on: `. An off mode whose median exact count is zero also makes the verdict `INVALID`, since continuity compares the two modes relatively and `0/N` against `0/N` would otherwise pass. The off and on medians are incomparable if their compaction counts differ by more than one. A single-mode run is also incomparable. Repeat metrics are medians of reported numeric values over runs that did not fail; if every run failed the medians stay empty. Loss points are combined across runs. A run stopped by `--max-turns` stays scored: its table row counts it and the verdict lists how many runs stopped that way, without invalidating them.

Cache data is reported for a run only when every model call reports it. Otherwise its cache metrics are unavailable. Cost and cached tokens fold in the summarizer calls of applied compactions alongside the main turns, and per-turn cost divides by the main-turn count. The cost proxy uses `uncached + 1.25*write + 0.1*read` for Anthropic aliases and `uncached + 0.1*read` for other aliases. This is a relative token estimate, not billing.

For each `--anthropic-alias`, two one-turn probe calls with one shared prompt test cache control. The prompt opens with a per-probe nonce, so the first call writes cache even when a recent run or a second alias already warmed identical probe text. The probe passes if the first call reports cache creation and the second reports a cache read. A failed probe reports `probe calls failed: exit <code>` plus, when the stderr text matches, one fixed classification: `auth error (401)`, `forbidden (403)`, `not found (404)`, `bad request (400)`, or `timeout`. Raw stderr text never reaches the report, progress output, or `raw.json`. A failed probe prevents a cache verdict for that alias.

## Output and verdicts

`results.md` is written to the output directory and printed to stdout. Its header records the UTC timestamp, binary path, settings, on-mode experiment environment, and sandbox status. Per-alias tables show turns, max turns hit, compactions, rebases, plain fallbacks, maximum segments, cached tokens, cost proxy per turn, and continuity, followed by verdict reasons and an Anthropic probe line when applicable. Progress goes to stderr.

`raw.json` contains run records and settings. Per-run stdout, stderr, session log, and compaction log copies are under `<out>/<alias>/` when available. The report includes the on-mode flag and only limit variables set in the environment.

Verdict precedence is `INVALID`, `INCOMPARABLE`, `INFORMATIONAL`, then `FAIL`, `CONTINUITY_ONLY` or `PASS`. A failed or invalid run, or invalid recall, on either side yields `INVALID`, as does an off side that recalled nothing. After validity and comparability checks, an informational alias gets no pass or fail. Otherwise `FAIL` means on continuity is below off by more than the tolerance, or, when cache is checked, on cost per turn exceeds off by more than the tolerance. Missing cache reporting or a failed Anthropic probe gives `no cache verdict`; cost is not judged, continuity still is, and a continuity result within tolerance is labelled `CONTINUITY_ONLY`. `PASS` requires both continuity and the cost proxy within tolerance.

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
node --test scripts/compaction-bench_test.mjs
# Or run the script test suite:
make test-scripts
```

The bench tests use a fake runner plus local `node` subprocesses; they do not make live provider calls.

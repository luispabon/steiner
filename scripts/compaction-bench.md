# Live compaction bench

## Purpose

`scripts/compaction-bench.mjs` compares continuity and cache token costs with immutable summary segments off and on (#1042). It uses Node's standard library and makes live model calls.

## Safety and cost

The bench refuses to run without `STEINER_LIVE_BENCH=1`. Provider calls spend real tokens, including two extra probe calls per Anthropic alias. Runs use fresh scratch git repositories and never pass `--unsafe`. `--trust-project-config` applies to the run; it does not persist trust or disable the sandbox. The report shows the sandbox setting from `steiner config`, or `not verified` if it could not be read.

Run artifacts can contain prompts and model output. Keep the output directory private and review artifacts before sharing them. The bench report includes only the experiment environment values, not the full environment; child logs and output can still contain sensitive data.

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
| `--pad-tokens <n>` | Approximate filler tokens per file | 3000 |
| `--max-turns <n>` | Turn limit per run | 80 |
| `--repeat <n>` | Runs per alias and mode | 1 |
| `--tolerance <n>` | Allowed percent change in continuity and cost | 5 |
| `--run-timeout-minutes <n>` | Kill a run after this many minutes | 30 |
| `--steiner <path>` | Steiner binary | `steiner` |
| `--out <dir>` | Output directory | a temporary directory |
| `--help` | Print usage | |

Exit codes are 0 for completion, 1 for the live-call guard, 2 for usage errors, and 3 if a benchmark run failed. A failing verdict or probe alone does not set exit code 3.

## How a run works

For each alias, repeat, and selected mode, the bench generates a seeded chain in a fresh scratch repository and starts one `steiner --exec` without `--resume`. Off and on use the same seed for each repeat. The invocation passes `--model`, `--max-turns`, `--compaction-log-file`, `--log-file`, `--config`, and `--trust-project-config`. Its private config sets the alias's `models.definitions.<alias>.advanced.limits.compaction_threshold_tokens` and enables the cache diagnostics stream outside the project root.

Files `notes/f01.txt` through `notes/fNN.txt` each contain a code, a `NEXT` pointer, and filler. One file in the second half corrects the code for `f03`. The task asks the model to follow the chain one file at a time, never re-read, list, or search the directory, and answer with `fNN: CODE` lines applying corrections.

## Scoring and validity

Continuity counts exact code matches per file. `f03` only counts if the corrected code is present and the old code is absent from the answer. Each lost code is attributed to the first compaction after its file was read, as `fNN@cN`, or marked unattributed. Recall is invalid if a file is re-read after a compaction between its first and second reads.

The bench reads file paths, model-call usage, and applied compactions from the session log, and `segment_decision` records from the compaction log. Rebases exclude `chain-start`; a `plain` decision counts as both a rebase and a plain fallback.

An on run is invalid with fewer than four compactions or no append decision. The off and on medians are incomparable if their compaction counts differ by more than one. A single-mode run is also incomparable. Repeat metrics are medians of reported numeric values; loss points are combined across runs.

Cache data is reported for a run only when every model call reports it. Otherwise its cache metrics are unavailable. The per-turn cost proxy uses `uncached + 1.25*write + 0.1*read` for Anthropic aliases and `uncached + 0.1*read` for other aliases. This is a relative token estimate, not billing.

For each `--anthropic-alias`, two identical one-turn probe calls test cache control. The probe passes if the first reports cache creation and the second reports a cache read. A failed probe reports a reason and prevents a cache verdict for that alias. Failure evidence can include an exit status and a stderr line that contains no full environment value.

## Output and verdicts

`results.md` is written to the output directory and printed to stdout. Its header records the UTC timestamp, binary path, settings, on-mode experiment environment, and sandbox status. Per-alias tables show turns, compactions, rebases, plain fallbacks, maximum segments, cached tokens, cost proxy per turn, and continuity, followed by verdict reasons and an Anthropic probe line when applicable. Progress goes to stderr.

`raw.json` contains run records and settings. Per-run stdout, stderr, session log, and compaction log copies are under `<out>/<alias>/` when available. The report includes the on-mode flag and only limit variables set in the environment.

Verdict precedence is `INVALID`, `INCOMPARABLE`, `INFORMATIONAL`, then `FAIL` or `PASS`. An invalid on run or invalid recall yields `INVALID`. After validity and comparability checks, an informational alias gets no pass or fail. Otherwise `FAIL` means on continuity is below off by more than the tolerance, or, when cache is checked, on cost per turn exceeds off by more than the tolerance. Missing cache reporting or a failed Anthropic probe gives `no cache verdict`; cost is not judged.

## Provider notes

Mark aliases using Anthropic cache accounting with `--anthropic-alias`; all other aliases use the other cost formula. The probe tests the actual route rather than assuming cache support. Use `--informational-alias` for `claude_subscription`: the Claude CLI manages the session and its caching, so its results should not be judged as a pass or fail.

## Limitations

- A fast run understates the effects of cache expiry during idle gaps in real sessions. Codex's measured idle cache TTL is about 5 minutes.
- Small or verbose models may not finish the chain within `--max-turns`.
- Loss attribution identifies the first compaction after a read, not proof that compaction caused the loss.
- The cost proxy is not a provider bill.

## Tests

```sh
node --test scripts/compaction-bench_test.mjs
# Or run the script test suite:
make test-scripts
```

The bench tests use a fake runner and do not make live provider calls.

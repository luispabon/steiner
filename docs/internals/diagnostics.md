# Diagnostics internals

User reference: [diagnostics block](../user/configuration.md#diagnostics-block) and [logging block](../user/configuration.md#logging-block). This page covers routing, file layout, and the analysis tooling.

## Logging handler

`output.ConfigureLogger` sets a text `slog` handler as the process default. Unknown levels fall back to `info`. While the TUI runs, records go to a `*.slog` file beside the session log, and are discarded when no log file is set. Nothing reaches stderr during a TUI session. The log file is `--log-file`, else `logging.file` when `logging.enabled` is set.

The session log is JSONL. Each process starts it with a `log_started` record, and it rotates by size. With `diagnostics.capture_bodies`, `api_request` records are unbounded. Both logs can hold prompts and tool output.

## Writer and streams

`buildRuntimeDiagnostics` returns nil when diagnostics are disabled, so no directory is created. `diagnostics.New` returns nil when no stream is enabled, and a nil `*Writer` is safe to call. Each enabled stream is one `<kind>.jsonl` file for `cache`, `provider`, `tool`, `tui`, or `internal`. Streams are enabled independently.

- Default directory: `$XDG_STATE_HOME/steiner/diagnostics`, else `~/.local/state/steiner/diagnostics`. Configured paths expand `~`.
- Modes: directory `0o700`, stream files `0o600`, opened with `O_APPEND`. Records append across runs.
- The delegation trace log (`tool`) and stream error log (`provider`) write to the writer only when that stream is enabled. Otherwise they keep their own files.
- The `internal` stream is written by the TUI devlog. Messages are capped at 8 KiB and control characters are removed.

## Envelope

Records carry `ts`, `kind`, and `seq`. The rest are `omitempty`: `run_id`, `session_id`, `build_sha`, `dirty`, `source`, `agent_id`, `agent_type`, `turn`, and `payload`. `seq` is a process-wide counter.

- `run_id` is always set on writer records, because `New` generates one when `Options.RunID` is empty.
- `build_sha` comes from `Options.BuildSHA` when the record has none. The Makefile sets `main.commit` from `git rev-parse HEAD`, or `none`.
- `dirty` is written only when true. The Makefile sets `main.dirty` to `true` when `git diff --quiet HEAD` fails. Readers must treat an absent `dirty` as false.
- `source` is `parent`, `sub_agent`, or `advisor`. Tool records take `turn` and `payload.model` from the call context when it is present.

## Rotation and retention

Before a write that would exceed the stream cap, `rotate` closes the file, shifts `<file>.N` to `<file>.N+1`, drops the oldest generation, renames the active file to `<file>.1`, and reopens it. The cap and generation count are constants in `writer.go`. If reopening fails, later records for that run are dropped without a log line. A failed write logs `diagnostics writer degraded` once per writer.

Retention runs once when a `Writer` opens and `retention_days` is positive. For each active and rotated file, `pruneStale` reads only the first line. It rewrites the file through a temp file and rename, only when that line is older than the cutoff. A first line without a parseable timestamp counts as stale, and unparseable lines are dropped during the rewrite.

## TUI frame stats

`internal/tui/frame_stats.go` times each `Update` and the `View` after it. Messages are classified by bounded names: `key`, `tick`, `sync_debounce`, `resize_reflow`, `runtime_event:<type>`, `window_size`, `composer_blink`, `session_tick`, `drag_autoscroll`, `jump_flash`, `paste`, `mouse_wheel`, `mouse_raw`, and `other:<Go type>`.

Each 10-second window keeps count, sum, max, and a 15-bucket histogram per name. Bucket bounds run from 50 µs to 512 ms, and the last bucket is open. Finished windows go to a writer goroutine through a queue of 8. A full queue drops the window and counts it in `dropped_windows`. The sink is `tui.FrameStatsSink`, implemented by `*diagnostics.Writer`.

## Analyzing diagnostics

`scripts/diagnostics.mjs <mode> [flags]` is stdlib-only Node with no build step. Its header lists every flag. Modes:

- `cache`, `provider`, `tools`: hit rates, retries, latency percentiles, and failure reasons.
- `coldturns`: joins `cache` and `tool` records to classify turns that read nothing from cache. A group is adequate with at least `--min-n` long-delegation turns (default 10). Interpretation is in [prompt caching](prompt-caching.md).
- `prefix <logfile>`: per turn, `APPEND-ONLY`, or `BREAK-AT-N` when the common prefix with the preceding turn is N messages.
- `mutate` (`diagnostics_mutate.mjs`): groups `tool` records into assistant messages. A new message starts when `turn` changes, and a repeated turn after `/clear` starts a new message. Runs without `turn` use the pre-attribution rule and are labelled approximate.
- `tui` (`diagnostics_tui.mjs`): per-name p50 and p95 from bucket upper bounds, max, CPU time, and share of wall time. `TUI_BUCKET_BOUNDS_MS` mirrors the Go bounds, so change both together.

`--compare <shaA> <shaB>` uses `build_sha` and is rejected with `prefix` and `coldturns`.

Shared fixtures live in `testdata/diagnostics/`: a flat set covering every stream, plus `coldturns/`, `coldturns_seq/` (where `ts` order differs from `seq` order), and `mutate/`. `TestSharedFixturesDecode` checks each record's kind and non-nil payload. `scripts/diagnostics_test.mjs` checks row counts and hand-computed aggregates. `make test-scripts` runs the Node tests.

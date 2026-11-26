# Diagnostics internals

User guide: [Configuration](../user/configuration.md#diagnostics-block).

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

## Tool payload

When the `tool` stream is enabled, every `Executor.Execute` call writes one `kind:"tool"` record, whatever the outcome. Fields:

- `tool`, `model`, `outcome` (`ok`, `denied`, or `error`), and `duration_ms`.
- `ops_total`, the number of operations the input carried (1 when it carries no `operations` array), and `ops_failed`, the number of failed operations.
- `failures[]`: `op`, `reason`, `path_ext`, and, for failed mutate replaces under `capture_bodies`, the bounded `match` and `sample` evidence.
- `ops_requested`: requested operation types and their counts, keyed by the tool's fixed labels.

`ops_requested` is opt-in per tool through `ToolDef.OpsRequested` (currently only mutate supplies one). The extractor reads the call's raw input after execution, so counts cover policy-denied and failed calls, and it runs only while the `tool` stream is enabled. Absent means the tool has no extractor or the input did not describe operations, such as mutate input whose `operations` value is not an array, including a double-encoded JSON string. A present `{}` means the input described zero operations. Counts never carry paths, arguments, file content, or other model-supplied text: mutate derives its labels from `allowedFields`, maps a missing, non-string, or unknown `type` to `invalid`, and trims the type as the planner does. The executor also bounds any extractor result to 16 labels of at most 24 lowercase identifier characters and drops labels outside that shape, so the field cannot hold arbitrary strings. An extractor panic is swallowed and omits the field. `ops_total` is computed separately and does not change.

## Rotation, retention and concurrent writers

Several processes can write the same directory: the parent run and its sub-agents, or two terminals. Each stream has a stable sibling lock file, `<kind>.jsonl.lock`, created once and never deleted or rewritten, so it stays valid while rotation renames the data files. `(*Writer).lockStream` in `internal/diagnostics/locks.go` acquires it through `internal/filelock`, bounded by `streamLockTimeout` (100ms). The lock files are per stream, but one `*Writer` serializes all of its appends under `Writer.mu`, which it holds while `append` acquires the stream lock, so a stream waiting for its lock delays that writer's other streams for up to `streamLockTimeout`.

The lock covers every mutation of one stream. `(*Writer).prepareStream` holds it across startup rotation recovery, retention, tail repair and opening the file. `(*Writer).append` holds it across rotation recovery, the active-file refresh in `stream.reopenIfNeeded`, the tail repair in `repairIncompleteTail`, the rotation decision, `rotateStream` and the record write. The refresh compares the open descriptor with the active path through `os.SameFile` and reopens on a mismatch, which catches replacement by a writer that takes no lock; under the lock no cooperating writer can replace the active file between the refresh and the record write.

When the lock is not free within the timeout, or `filelock` reports the platform or filesystem has no working lock, `lockStream` warns through `Writer.warn` and the caller skips the protected mutation instead of running it unprotected. `append` then drops the record. At startup `prepareStream` also leaves the file unopened in that case: it registers the stream without a descriptor, so tail repair and the open wait for the first `append` that takes the lock, which performs both under it. Retention is not retried that way: when the lock is unavailable at startup, `prepareStream` skips it, and pruning runs only when a writer opens the directory with `retention_days` positive. A rotation recovery failure at startup is warned the same way and registers the same descriptorless stream.

Rotation is a transaction. `rotateStream` writes a versioned marker at `<kind>.jsonl.rotate.json` with phase `staging` and the generation set before any source moves, then closes its own descriptor, because Windows refuses to rename a file another handle has open, and moves the existing generations to stage paths (`<kind>.jsonl.rot` for the active file, `<kind>.jsonl.N.rot` for generation `N`). It rewrites the marker with phase `installing`, shifts each stage to its destination (active to `.1`, `N` to `N+1`), discards the staged oldest generation last so the kept generations stay `.1` to `.3`, and removes the marker only after every step succeeds.

`recoverRotation` runs under the stream lock at startup and before every append, and repeats whichever stage or install step is outstanding: a moved source with its stage present counts as staged, and an absent stage with its destination present counts as installed. Any other combination for a listed generation, or a marker version this build does not know, is an error: the marker stays, the record is dropped, and a later attempt retries. A marker is never removed while a transaction is incomplete. A rotation that fails before moving any generation is deferred: the stream keeps appending for at most one more cap worth of records, then drops records while retrying the rotation, and retries at most once per `stallRetryInterval` (5s).

At startup a pending marker means an unfinished rotation owns the active path, so `prepareStream` registers the stream without opening it; the first `append` that recovers the transaction opens the file under the lock and then writes its record, and a record whose recovery fails is dropped.

Tail repair has two entry points, both under the stream lock. `truncateIncompleteTail` runs from `prepareStream` before the stream opens, and `repairIncompleteTail` runs from `append` before every record: a trailing fragment with no newline, which a process killed mid-record leaves, is truncated to the last newline and complete lines are kept, so no record lands on top of it. The pre-append check opens and stats the active file, reads one byte only when the file is non-empty, and truncates only when that byte is not a newline; after a repair the tracked size comes from the truncated file, so the stream does not rotate early. At startup a repair failure fails writer construction; during an append it goes through `Writer.warn`, which warns once per writer, and the record is dropped.

Retention runs only when a writer opens a directory with `retention_days` positive. It prunes the active file and every rotated generation under the stream lock, so no cooperating process appends to a file while it is rewritten.

The remaining limits: only writers that take the lock exclude each other; on a platform or filesystem where `filelock` reports no working lock the writer warns and skips protected mutations instead of excluding anyone; power-loss durability is not guaranteed; and a third party that deletes or rewrites the transaction files on disk is not recovered from. Rotation renames stream files, so it completes only while no other process holds one open in a way the platform refuses to rename: a peer that keeps the active file open blocks the rename on Windows, and the stream defers the rotation or stalls and retries at `stallRetryInterval` until that peer closes the stream or exits.

## TUI frame stats

`internal/tui/frame_stats.go` times each `Update` and the `View` after it. Messages are classified by bounded names: `key`, `tick`, `sync_debounce`, `resize_reflow`, `runtime_event:<type>`, `window_size`, `composer_blink`, `session_tick`, `drag_autoscroll`, `jump_flash`, `paste`, `mouse_wheel`, `mouse_click`, `mouse_release`, `mouse_motion`, `mouse_raw`, and `other:<Go type>`.

Each 10-second window keeps count, sum, max, and a 15-bucket histogram per name. Bucket bounds run from 50 µs to 512 ms, and the last bucket is open. Finished windows go to a writer goroutine through a queue of 8. A full queue drops the window and counts it in `dropped_windows`. The sink is `tui.FrameStatsSink`, implemented by `*diagnostics.Writer`.

## Analyzing diagnostics

`scripts/diagnostics.mjs <mode> [flags]` is stdlib-only Node with no build step. Its header lists every flag. Modes:

- `cache`, `provider`, `tools`: hit rates, retries, latency percentiles, and failure reasons.
- `coldturns`: joins `cache` and `tool` records to classify turns that read nothing from cache. A joined pair counts as a long-delegation turn when its delegated call lasts at least `LONG_DELEGATION_MS` (240 s). A group is adequate with at least `--min-n` such turns (default 10). Records carrying `resume_baseline_unavailable` are counted in their own `baseline_unavailable_after_resume` section and kept out of every pair-based table (model aggregation, attribution, ladders): the label marks a process-local baseline a resume could not compare against, not a miss cause. Interpretation is in [prompt caching](prompt-caching.md).
- `prefix <logfile>`: per turn, `APPEND-ONLY`, or `BREAK-AT-N` when the common prefix with the preceding turn is N messages.
- `mutate` (`diagnostics_mutate.mjs`): groups `tool` records into assistant messages. A new message starts when `turn` changes, and a repeated turn after `/clear` starts a new message. Runs without `turn` use the pre-attribution rule and are labelled approximate.
- `tui` (`diagnostics_tui.mjs`): per-name p50 and p95 from bucket upper bounds, max, CPU time, and share of wall time. `TUI_BUCKET_BOUNDS_MS` mirrors the Go bounds, so change both together.
- `peak <file...>`: per run and agent, the max `prompt_tokens` of `model_call_finished` events and the call count, plus the number of runs whose main-agent peak reaches 200k, 500k and 1M. Sub-agent peaks never enter those buckets. It reads session logs, which exist only when logging is enabled (off by default; `--log-file`, or `logging.enabled` with `logging.file`), and `STEINER_USAGE_TELEMETRY` files, detected per line, so rotated generations and both formats may be passed together. A telemetry line has no agent id (`source` stands in, and `parent` is the main agent) and no run id unless `STEINER_USAGE_TELEMETRY_RUN` was set; lines without a run id are reported in their own `unattributed` table and JSON field and are never counted as runs. Telemetry also records compaction calls, and its lines carry no call kind (`usagestats.Observation.CallKind` is not persisted), so a telemetry compaction call looks like a parent call; session-log `model_call_finished` events cover turn calls only. The two sources therefore measure different peaks and call counts for the same run. `peak` accepts `--json` and `--top` (a non-negative integer) only. It rejects a missing, non-file or unreadable input, a non-numeric `--top`, and any other flag with an error, and its summary prints the excluded unattributed observation count beside the run counts.

`--compare <shaA> <shaB>` uses `build_sha` and is rejected with `prefix`, `coldturns`, `mutate`, and `peak`.

Shared fixtures live in `testdata/diagnostics/`: a flat set covering the `cache`, `provider`, `tool`, and `tui` streams, plus `coldturns/`, `coldturns_seq/` (where `ts` order differs from `seq` order), `mutate/`, and `peak/`. `peak/` holds a rotated session-log pair and a telemetry file rather than envelope streams, so `TestSharedFixturesDecode`, which walks stream kinds, does not cover it. No fixture covers the `internal` stream. `TestSharedFixturesDecode` checks each record's kind and non-nil payload. `scripts/diagnostics_test.mjs` checks row counts and hand-computed aggregates. `make test-scripts` runs the Node tests.

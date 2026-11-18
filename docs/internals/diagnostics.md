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

Several processes can write the same directory: the parent run and its sub-agents, or two terminals. Each stream has a stable sibling lock file, `<kind>.jsonl.lock`, created once and never deleted or rewritten, so it stays valid while rotation renames the data files. `(*Writer).lockStream` in `internal/diagnostics/locks.go` acquires it through `internal/filelock`, bounded by `streamLockTimeout` (100ms). Streams lock independently, so contention on one stream does not block another.

The lock covers every mutation of one stream. `(*Writer).prepareStream` holds it across startup rotation recovery, retention, tail repair and opening the file. `(*Writer).append` holds it across rotation recovery, the active-file refresh in `stream.reopenIfNeeded`, the rotation decision, `rotateStream` and the record write. The refresh still compares the open descriptor with the active path through `os.SameFile`, which catches replacement by a writer that takes no lock; among cooperating writers the replacement can no longer land between the check and the append.

When the lock is not free within the timeout, or `filelock` reports the platform or filesystem has no working lock, `lockStream` warns through `Writer.warn` and the caller skips the protected mutation instead of running it unprotected. `append` then drops the record. At startup, retention and tail repair are skipped, and the stream still opens unless a rotation marker is pending.

Rotation is a transaction. `rotateStream` writes a versioned marker at `<kind>.jsonl.rotate.json` with phase `staging` and the generation set before any source moves, then moves the existing generations to stage paths (`<kind>.jsonl.rot` for the active file, `<kind>.jsonl.N.rot` for generation `N`). It rewrites the marker with phase `installing`, shifts each stage to its destination (active to `.1`, `N` to `N+1`), discards the staged oldest generation last so the kept generations stay `.1` to `.3`, and removes the marker only after every step succeeds.

`recoverRotation` runs under the stream lock at startup and before every append, and repeats whichever stage or install step is outstanding: a moved source with its stage present counts as staged, and an absent stage with its destination present counts as installed. Any other combination for a listed generation, or a marker version this build does not know, is an error: the marker stays, the record is dropped, and a later attempt retries. A marker is never removed while a transaction is incomplete. A rotation that fails before moving any generation is deferred: the stream keeps appending for at most one more cap worth of records, then drops records while retrying the rotation, and retries at most once per `stallRetryInterval` (5s).

At startup a pending marker means an unfinished rotation owns the active path, so `prepareStream` registers the stream without opening it and its records are dropped until an append recovers the transaction and creates the file. `truncateIncompleteTail` runs under the lock before the stream opens: an unterminated trailing fragment left by a crash is truncated to the last newline, complete lines are kept, and a repair failure fails writer construction rather than letting the next record merge with the fragment.

Retention runs only when a writer opens a directory with `retention_days` positive, and holds the stream lock, so it no longer rewrites a stream another cooperating process is appending to.

The remaining limits: only writers that take the lock exclude each other; on a platform or filesystem where `filelock` reports no working lock the writer warns and skips protected mutations instead of excluding anyone; power-loss durability is not guaranteed; and a third party that deletes or rewrites the transaction files on disk is not recovered from. Tail repair runs only while a writer opens a stream, so a fragment left by another process that dies mid-record after this writer opened is not repaired until the next writer opens that stream.

## TUI frame stats

`internal/tui/frame_stats.go` times each `Update` and the `View` after it. Messages are classified by bounded names: `key`, `tick`, `sync_debounce`, `resize_reflow`, `runtime_event:<type>`, `window_size`, `composer_blink`, `session_tick`, `drag_autoscroll`, `jump_flash`, `paste`, `mouse_wheel`, `mouse_click`, `mouse_release`, `mouse_motion`, `mouse_raw`, and `other:<Go type>`.

Each 10-second window keeps count, sum, max, and a 15-bucket histogram per name. Bucket bounds run from 50 µs to 512 ms, and the last bucket is open. Finished windows go to a writer goroutine through a queue of 8. A full queue drops the window and counts it in `dropped_windows`. The sink is `tui.FrameStatsSink`, implemented by `*diagnostics.Writer`.

## Analyzing diagnostics

`scripts/diagnostics.mjs <mode> [flags]` is stdlib-only Node with no build step. Its header lists every flag. Modes:

- `cache`, `provider`, `tools`: hit rates, retries, latency percentiles, and failure reasons.
- `coldturns`: joins `cache` and `tool` records to classify turns that read nothing from cache. A joined pair counts as a long-delegation turn when its delegated call lasts at least `LONG_DELEGATION_MS` (240 s). A group is adequate with at least `--min-n` such turns (default 10). Interpretation is in [prompt caching](prompt-caching.md).
- `prefix <logfile>`: per turn, `APPEND-ONLY`, or `BREAK-AT-N` when the common prefix with the preceding turn is N messages.
- `mutate` (`diagnostics_mutate.mjs`): groups `tool` records into assistant messages. A new message starts when `turn` changes, and a repeated turn after `/clear` starts a new message. Runs without `turn` use the pre-attribution rule and are labelled approximate.
- `tui` (`diagnostics_tui.mjs`): per-name p50 and p95 from bucket upper bounds, max, CPU time, and share of wall time. `TUI_BUCKET_BOUNDS_MS` mirrors the Go bounds, so change both together.

`--compare <shaA> <shaB>` uses `build_sha` and is rejected with `prefix`, `coldturns`, and `mutate`.

Shared fixtures live in `testdata/diagnostics/`: a flat set covering the `cache`, `provider`, `tool`, and `tui` streams, plus `coldturns/`, `coldturns_seq/` (where `ts` order differs from `seq` order), and `mutate/`. No fixture covers the `internal` stream. `TestSharedFixturesDecode` checks each record's kind and non-nil payload. `scripts/diagnostics_test.mjs` checks row counts and hand-computed aggregates. `make test-scripts` runs the Node tests.

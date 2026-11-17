# Diagnostics internals

User guide: [Configuration](../user/configuration.md#diagnostics-block).

## Logging handler

`output.ConfigureLogger` sets a text `slog` handler as the process default. Unknown levels fall back to `info`. While the TUI runs, records go to a `*.slog` file beside the session log, and are discarded when no log file is set. Nothing reaches stderr during a TUI session. The log file is `--log-file`, else `logging.file` when `logging.enabled` is set.

The session log is JSONL. Each process starts it with a `log_started` record, and it rotates by size. With `diagnostics.capture_bodies`, `api_request` records are unbounded. Both logs can hold prompts and tool output.

## Writer and streams

`buildRuntimeDiagnostics` returns nil when diagnostics are disabled, so no directory is created. `diagnostics.New` returns nil when no stream is enabled, and a nil `*Writer` is safe to call. Each enabled stream is one `<kind>.jsonl` file for `cache`, `provider`, `tool`, `tui`, or `internal`. Streams are enabled independently.

- Default directory: `$XDG_STATE_HOME/steiner/diagnostics`, else `~/.local/state/steiner/diagnostics`. Configured paths expand `~`.
- Modes: directory `0o700`, stream files `0o600`, opened with `O_APPEND`. Records append across runs.
- Each stream keeps a stable sibling lock file, `<kind>.jsonl.lock`, created `0o600` by `internal/filelock` and never deleted. It serializes retention, rotation and appends across processes.
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

Several processes can write the same directory: the parent run and its sub-agents, or two terminals. Each stream serializes retention, rotation and appends through `internal/filelock` on its stable sibling lock file, and no lock wait is long enough to delay startup or charge the caller per queued record. `Writer.openStream` in `internal/diagnostics/writer.go` waits at most `openLockTimeout` (250ms) and skips retention when the lock stays busy, so a contended directory delays startup by a bounded amount and never fails it. `Writer.appendRecord` makes `appendLockAttempts` nonblocking attempts separated by `appendLockPause` (250µs), which rides out a concurrent append holding the lock for one write syscall; a lock held longer drops the record. No backoff window is involved, so the first append after the lock is released takes it. A record is never appended without the lock: when the lock cannot be taken the record is dropped, and the first lock problem is logged once per writer as `diagnostics writer degraded` with `action="lock diagnostics stream"`. When `internal/filelock` reports locking as unsupported for the platform or filesystem, appends proceed without cross-process exclusion under that same one-time warning. `internal/filelock` never writes warnings itself.

Every append marshals the record before taking the writer mutex, then the stream lock. Under the lock `stream.revalidate` compares the active path with `stream.lastFile`, the inode recorded by the last open, through `os.SameFile`: a descriptor left on a replaced inode by another process's retention or rotation is closed and reopened, a missing descriptor is recreated, and `written` is refreshed from the open file so the rotation decision counts bytes other writers appended. Comparing against the recorded inode rather than the live descriptor is what lets a rotation staged by this writer survive its own failed attempt while being discarded once another process replaces the file. Records therefore never land in an unlinked inode, and a file removed underneath the writer is recreated.

Before a write that would exceed the stream cap, `rotateStream` closes the descriptor, drops the oldest generation, shifts `<file>.N` to `<file>.N+1`, renames the active file to `<file>.1`, and reopens it. The cap and generation count are constants in `writer.go`. Closing before the rename is what keeps appends aimed at the active file out of the rotated generation. A generation move is only attempted onto a free generation name, and the active rename only once generation 1 is free, so a partially completed shift never overwrites a generation: it is retried on the next attempt instead. The oldest generation is dropped at most once per rotation episode (`stream.droppedOldest`), so retries cannot discard the generation that shifted into its place, and `stream.revalidate` clears that state when another process replaced the active file. A failed rotation leaves the stream closed; the next append revalidates, reopens the active path and retries, so records resume instead of being dropped for the rest of the run. A failed rotation or write logs `diagnostics writer degraded` once per writer; lock problems are reported once as described above.

Retention runs once when a `Writer` opens and `retention_days` is positive, holding the stream lock for the whole prune. For each active and rotated file, `pruneStale` reads only the first line. It rewrites the file through a temp file and rename, only when that line is older than the cutoff. A first line without a parseable timestamp counts as stale, and unparseable lines are dropped during the rewrite. The rewrite replaces the inode, so writers that already hold the stream open detect the change on their next append and reopen.

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

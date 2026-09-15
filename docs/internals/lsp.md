# LSP-backed code intelligence internals

User-facing documentation: [LSP-backed code intelligence](../user/lsp.md).

## Lifecycle and protocol mechanics

A server is selected by file extension and workspace root markers. Sessions are keyed by server and workspace root. They are spawned lazily, initialized over stdio, and retained until idle. The readiness gate observes `$/progress`: it waits for the first workspace-load completion cycle, while `ready_grace_period` lets servers that emit no progress send an initial event. Requests proceed with `incomplete=true` after `ready_timeout`; each request has its own timeout. Idle reaping marks a session stopped without treating it as failed.

`lsp_symbols` has two routes. With `file`, the manager selects a server from the file extension and calls `textDocument/documentSymbol`, then filters flattened results by case-insensitive substring when `query` is present. With only `query`, all enabled configured servers are queried concurrently through `workspace/symbol`, reusing an existing session for a server rather than spawning another at a different root. Results merge best-effort.

The five position tools normalize explicit line/column and identifier-boundary symbol lookup to one position before issuing the same request and cache lookup. Diagnostics use push publications after `didOpen`, not pull diagnostics. Post-mutate injection only uses already-ready sessions and checks a fixed bounded subset of touched files.

Subprocess stderr is written to a derived `<session-log>-lsp<ext>` file when logging is configured; otherwise it is discarded in interactive mode and sent to process stderr in non-interactive mode. Cache directories use a 16-character workspace-root hash below the configured or XDG cache root and are never removed automatically.

## Package map

| Area | Responsibility |
|------|----------------|
| `internal/lsp` | Server manager, JSON-RPC sessions, routing, readiness, diagnostics, and tools |
| `internal/config` | LSP fields and defaults |
| `internal/tool` | Tool registration and post-mutate integration |
| `internal/tui` | Sidebar and `/lsp` overlay |
| `cmd/steiner` | Runtime wiring and server process setup |

## Timeout calibration

The `lsp.*` defaults in `internal/config/defaults.go` come from measurements against a real gopls. `internal/lsp/calibrate_manual_test.go` is behind the `lspcalib` build tag and does not run in CI:

```bash
go test -tags lspcalib ./internal/lsp/ -run TestCalibrate -v -timeout 40m
```

It resolves gopls from `$STEINER_LSP_CALIB_GOPLS`, then `PATH`, `$GOBIN`, and `$(go env GOPATH)/bin`, skipping when none is found.

### Measurement environment

| | |
|---|---|
| Language server | `golang.org/x/tools/gopls v0.23.0` |
| OS | Linux 7.0.0-30-generic (x86_64) |
| CPU | AMD Ryzen 9 7940HS, 16 threads |
| Workspace | this repository, commit `f23a0c5f0ce9b927768d8b8894a3a58957763fe5` |
| Samples | 5 per category |

The harness mirrors `Manager.spawnServer`: `HOME`, `XDG_CACHE_HOME`, and `GOCACHE` point at a per-workspace cache directory. Cold runs use fresh gopls and GOCACHE but a warm GOMODCACHE. Module-download time on a genuinely empty module cache is not measured and is part of what `ready_timeout` protects.

### Observed measurements

Workspace load from spawn to readiness events:

| Run | Cold: first begin | Cold: begin→end | Warm: first begin | Warm: begin→end |
|---|---|---|---|---|
| 1 | 65 ms | 1.026 s | 72 ms | 444 ms |
| 2 | 74 ms | 1.049 s | 72 ms | 380 ms |
| 3 | 62 ms | 1.109 s | 120 ms | 429 ms |
| 4 | 106 ms | 1.262 s | 76 ms | 401 ms |
| 5 | 61 ms | 1.139 s | 74 ms | 411 ms |
| **max** | **106 ms** | **1.262 s** | **120 ms** | **444 ms** |

An earlier run saw a cold begin-to-end maximum of 1.764s, so approximately 1.8s is the observed cold ceiling for this repository size.

Diagnostics watched every `publishDiagnostics` for 10s on a warm ready server:

| Run | Clean file | Overlay with an error |
|---|---|---|
| 1 | 1 publication: +615 ms, 0 items, version 1 | 1 publication: +46 ms, 1 item, version 1 |
| 2 | 1 publication: +20 ms, 0 items, version 1 | 1 publication: +20 ms, 1 item, version 1 |
| 3 | 1 publication: +22 ms, 0 items, version 1 | 1 publication: +22 ms, 1 item, version 1 |
| 4 | 1 publication: +19 ms, 0 items, version 1 | 1 publication: +17 ms, 1 item, version 1 |
| 5 | 1 publication: +22 ms, 0 items, version 1 | 1 publication: +19 ms, 1 item, version 1 |

Across 10 runs, gopls published exactly once per file. The earlier claim of a slower second workspace diagnostics pass was refuted.

Definition requests on a warm ready server returned non-empty cross-file results. Full cycles were 529ms, 12.9ms, 13.9ms, 10.2ms, and 11.4ms; the first was the first query against a freshly-ready server, with steady state 10 to 14ms.

For a no-progress server, the harness initializes gopls without `window.workDoneProgress`, asserts zero progress events, and issues a definition immediately after `initialized` on a cold cache:

| Run | First correct answer | Progress events |
|---|---|---|
| 1 | 1.382 s | 0 |
| 2 | 1.455 s | 0 |
| 3 | 1.704 s | 0 |
| 4 | 1.705 s | 0 |
| 5 | 1.790 s | 0 |

Requests queued by a still-loading server returned correct answers. Early request release costs latency, not accuracy.

### Defaults derived

| Field | Value | Derivation |
|---|---|---|
| `idle_timeout` | 5m | Policy; measurements do not argue for a change. |
| `request_timeout` | 10s | Hung-server ceiling. Warm definitions peaked at 529ms; no-progress first answer at 1.79s. References were not measured. |
| `ready_timeout` | 30s | Deliberately generous; expiry marks incomplete and proceeds. |
| `ready_grace_period` | 2s | About 17 times the observed 61 to 120ms first-progress range; also safe for no-progress servers. |
| `diagnostics_window` | 2s | Lowered from provisional 3s. The worst publication was 615ms and no second pass exists. |

Only `diagnostics_window` changed in that calibration.

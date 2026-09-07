# LSP-backed code intelligence

Steiner can connect to language servers to answer navigation and diagnostics
queries. Servers are configured under `lsp.servers` and are off by default
(`lsp.enabled: false`). See docs/configuration.md for the field reference.

## The three tools

Three built-in tools become available when a language server is configured and working:

- **definitions** — Jump to the definition of a symbol at a given location. Returns a list of locations in other files (or the same file) where the symbol is defined. If multiple definitions exist (rare), all are returned, up to `lsp.max_results`.
- **references** — Find all references to a symbol. By default includes the symbol's declaration; pass `include_declaration: false` to exclude it. Results are returned up to `lsp.max_results`.
- **diagnostics** — Get diagnostics (errors, warnings, information, hints) for a file. Returns what the language server has published for that file. Diagnostics reflect the server's state at query time; if the server is still indexing, results may be incomplete or provisional.

All three tools gracefully degrade when no server is configured for a file's extension, when a server is disabled, or when a server fails to start — they return a clear message instead of an error. See [Graceful degradation](#graceful-degradation) below for the messages and what they mean.

## Language server setup

Steiner does not install or manage language servers. You must install servers separately before configuring them in steiner. Each server is a separate executable that you run from `lsp.servers.<name>.command`.

### Copy-paste server examples

#### gopls (Go)

```yaml
lsp:
  servers:
    gopls:
      enabled: true
      command: gopls
      file_extensions: [".go"]
      root_markers: ["go.mod", ".git"]
      env:
        GOCACHE: ${XDG_CACHE_HOME}/go
        GOMODCACHE: ${GOPATH}/pkg/mod
```

Install: `go install github.com/golang/tools/gopls@latest`

#### typescript-language-server (TypeScript / JavaScript)

```yaml
lsp:
  servers:
    tsserver:
      enabled: true
      command: typescript-language-server
      args: ["--stdio"]
      file_extensions: [".ts", ".tsx", ".js", ".jsx"]
      root_markers: ["package.json", "tsconfig.json"]
```

Install: `npm install -g typescript-language-server typescript`

#### pyright (Python)

```yaml
lsp:
  servers:
    pyright:
      enabled: true
      command: pyright-langserver
      args: ["--stdio"]
      file_extensions: [".py"]
      root_markers: ["pyproject.toml", "setup.py", ".git"]
```

Install: `pip install pyright` (or use your package manager)

#### rust-analyzer (Rust)

```yaml
lsp:
  servers:
    rust-analyzer:
      enabled: true
      command: rust-analyzer
      file_extensions: [".rs"]
      root_markers: ["Cargo.toml", ".git"]
```

Install: Per [rust-analyzer installation](https://rust-analyzer.github.io/manual.html#installation)

## Lifecycle

Language servers are started lazily on first use and kept alive until idle for `lsp.idle_timeout` (default 5m). When a request is issued to a server:

1. If the server is not running, it is spawned and initialized.
2. The initialization includes a `lsp.ready_timeout` (default 30s) wait for the server to signal readiness (the first workspace load complete). If readiness is not signaled within the timeout, the request proceeds anyway with an incomplete flag in the response.
3. The request is sent to the server with a per-request timeout of `lsp.request_timeout` (default 10s).
4. If the server becomes idle (no requests for `lsp.idle_timeout`), it is shut down.

The readiness gate is a best-effort optimization: servers that emit progress events are tracked closely, and servers that emit no progress are given `lsp.ready_grace_period` (default 2s) to send an initial event before the readiness gate closes. This allows requests to proceed to a truly ready server as soon as indexing completes, and prevents requests from blocking indefinitely on servers that never report progress.

## Persistent cache directory

By default, each language server's cache (index, compiled code, etc.) is stored under the system user cache directory. Steiner derives a per-workspace cache subdirectory based on a hash of the workspace root, creating the path:

```
<lsp.cache_dir or XDG_CACHE_HOME>/steiner/lsp/<16-char-hash-of-workspace-root>/
```

When `lsp.cache_dir` is set in config, it overrides the user cache directory. The cache is **never cleaned up automatically** — users are responsible for removing old caches manually if needed.

## Graceful degradation

The three LSP tools return human-readable messages instead of errors when a server is unavailable:

- **"No language server is configured for .ext. Configure one under `lsp.servers` to enable this tool."** — The file extension has no server declared in config, or the configured server is disabled.
- **"Language server <name> failed to start: <error>."** — The server executable was not found, did not start, or crashed during initialization.
- **"Language server <name> exited unexpectedly."** — The server was running but crashed or exited during a request.

When a query completes successfully but the server was still indexing, the result may be incomplete. The response includes a note: *"results may be incomplete if the language server's indexing has not finished."* This is expected and not an error.

## Diagnostics collection window

The `diagnostics` tool collects published diagnostics for a file by opening it and waiting for `lsp.diagnostics_window` (default 2s) to collect all incoming diagnostics messages from the server. Unlike pull-based diagnostics (which would request diagnostics on demand), this push-based approach respects the server's optimization: servers batch and rate-limit diagnostics publications, and we honor those choices rather than forcing a full re-check.

The `lsp.diagnostics_window` is unconditional latency: the tool always waits the full window even if the server publishes diagnostics immediately. This ensures completeness without being surprising.

## Known limitations

- Language servers other than gopls are unverified for cache requirements. If you encounter cache-related issues with other servers, open an issue.
- Hover, document symbols (`textDocument/documentSymbol`), rename (`textDocument/rename`), and other LSP features are not yet implemented. The three tools (definitions, references, diagnostics) are the current focus.

## Timeout calibration

The `lsp.*` timeout defaults in `internal/config/defaults.go` are derived from
measurements taken against a real gopls, not from guesswork. The harness that
produced them lives in `internal/lsp/calibrate_manual_test.go` behind the
`lspcalib` build tag, so it never runs in CI:

```bash
go test -tags lspcalib ./internal/lsp/ -run TestCalibrate -v -timeout 40m
```

It resolves gopls from `$STEINER_LSP_CALIB_GOPLS`, then `PATH`, then `$GOBIN`,
then `$(go env GOPATH)/bin`, and skips when none of those hit.

### Measurement environment

| | |
|---|---|
| Language server | `golang.org/x/tools/gopls v0.23.0` |
| OS | Linux 7.0.0-30-generic (x86_64) |
| CPU | AMD Ryzen 9 7940HS, 16 threads |
| Workspace | this repository, commit `f23a0c5f0ce9b927768d8b8894a3a58957763fe5` |
| Samples | 5 per category |

The harness mirrors `Manager.spawnServer`'s environment: `HOME`,
`XDG_CACHE_HOME` and `GOCACHE` all point at a per-workspace cache directory. A
**cold** run is therefore a fresh gopls index *and* a fresh `GOCACHE`, but a
**warm** `GOMODCACHE` — `GOMODCACHE`, `GOPATH` and `PATH` are inherited from the
real environment, as a user filling in `lsp.servers.<name>.env` must supply
them. A genuine first run on a machine with an empty module cache pays module
download time on top of every cold number below; that scenario is not measured
here and is part of what `ready_timeout` protects against.

### Workspace load

Time from process spawn to the events `internal/lsp/readiness.go` actually acts
on: the first `$/progress` `begin`, and the first complete `begin`→`end` cycle.

| Run | Cold: first begin | Cold: begin→end | Warm: first begin | Warm: begin→end |
|---|---|---|---|---|
| 1 | 65 ms | 1.026 s | 72 ms | 444 ms |
| 2 | 74 ms | 1.049 s | 72 ms | 380 ms |
| 3 | 62 ms | 1.109 s | 120 ms | 429 ms |
| 4 | 106 ms | 1.262 s | 76 ms | 401 ms |
| 5 | 61 ms | 1.139 s | 74 ms | 411 ms |
| **max** | **106 ms** | **1.262 s** | **120 ms** | **444 ms** |

An earlier run of the same harness saw a cold `begin`→`end` maximum of 1.764 s,
so treat ~1.8 s as the observed cold ceiling for a repository this size.

### Diagnostics

Every `publishDiagnostics` for the opened file, timed from `didOpen`, watched
for a 10 s ceiling on a warm, ready server. Two cases: the file as it is on
disk, and an overlay with an appended call to an undefined symbol.

| Run | Clean file | Overlay with an error |
|---|---|---|
| 1 | 1 publication: +615 ms, 0 items, version 1 | 1 publication: +46 ms, 1 item, version 1 |
| 2 | 1 publication: +20 ms, 0 items, version 1 | 1 publication: +20 ms, 1 item, version 1 |
| 3 | 1 publication: +22 ms, 0 items, version 1 | 1 publication: +22 ms, 1 item, version 1 |
| 4 | 1 publication: +19 ms, 0 items, version 1 | 1 publication: +17 ms, 1 item, version 1 |
| 5 | 1 publication: +22 ms, 0 items, version 1 | 1 publication: +19 ms, 1 item, version 1 |

**The second-pass claim (D13) is refuted.** Research notes claimed gopls runs a
second, slower workspace diagnostics pass roughly a second after the first
publication. Across 10 runs with a 10 s observation window, gopls published
**exactly once** per file — no repeat, no updated version, not even an identical
re-publication. The overlay case published one real item, which confirms the
single-publication result is gopls finishing the job rather than gopls staying
silent.

### Definition requests

Full `didOpen` → `textDocument/definition` → `didClose` cycle on a warm, ready
server, resolving a cross-file symbol. Every run returned a non-empty result.

| Run | Request | Full cycle |
|---|---|---|
| 1 | 529 ms | 529 ms |
| 2 | 12.8 ms | 12.9 ms |
| 3 | 13.8 ms | 13.9 ms |
| 4 | 10.1 ms | 10.2 ms |
| 5 | 11.3 ms | 11.4 ms |

Run 1 is the first query against a freshly-ready server; steady state is
10–14 ms.

### Servers that emit no progress (D16)

`ready_grace_period` exists for servers that never send `$/progress`, so
gopls with `window.workDoneProgress` present cannot measure it. The harness
initializes gopls *without* that client capability instead: a real server doing
real indexing while staying silent. Each run asserts that zero progress events
arrived, then issues a definition immediately after `initialized` on a cold
cache and times the first **correct** (non-empty) answer.

| Run | First correct answer | Progress events |
|---|---|---|
| 1 | 1.382 s | 0 |
| 2 | 1.455 s | 0 |
| 3 | 1.704 s | 0 |
| 4 | 1.705 s | 0 |
| 5 | 1.790 s | 0 |

The key finding is not the latency but the correctness: a request issued to a
still-loading server is queued server-side and answered correctly. Letting a
request through early is safe; it costs latency, not accuracy.

### Resulting defaults

| Field | Value | Derivation |
|---|---|---|
| `idle_timeout` | 5m | Policy, not a measurement. Nothing above argues for a change. |
| `request_timeout` | 10s | A ceiling for a hung server, not a p95-derived margin. Warm definitions peak at 529 ms and a request to a still-loading no-progress server returned in 1.79 s, both far inside it. Measured against definitions only — `references` is a heavier query and was not measured. |
| `ready_timeout` | 30s | Unchanged, and deliberately generous. On expiry `awaitReady` returns `incomplete=true` and the request proceeds anyway, so an over-long value costs nothing in the common case (the gate closes at ~1.2 s and the timer never fires), while an under-short one silently defeats the readiness gate on any workspace larger than this one — or on a genuine first run that also pays module downloads. |
| `ready_grace_period` | 2s | Must exceed the time a normal server takes to emit its *first* `begin`, or `trackReadiness` wrongly concludes the server is silent. That was 61–120 ms cold and warm, so 2s carries ~17x headroom. D16 also shows that firing this timer early is not harmful, which is why headroom rather than precision is the goal. |
| `diagnostics_window` | 2s | Lowered from a provisional 3s. `collectDiagnostics` has no early exit — it always burns the whole window, so this is unconditional latency on every diagnostics call. The last publication for a file arrived at 615 ms in the worst of 10 runs and no second pass exists to wait for, so 2s keeps ~3.3x headroom over that worst case while returning a second sooner. |

Only `diagnostics_window` changed. The other four are unchanged because the
measurements support them, not because they were left untouched.

# LSP-backed code intelligence internals

User guide: [LSP-backed code intelligence](../user/lsp.md).

## Sessions and readiness

`serverForExtension` selects the server by file extension, and `resolveRoot` picks the root from `RootMarkers`. Sessions are keyed `sessionKey{server, root}`, spawn lazily, and bound the handshake with `ReadyTimeout`. Status values are declared, starting, ready, failed, and stopped (`internal/lsp/state.go`). `reapIdle` closes ready sessions idle past `idle_timeout` and marks them stopped, not failed. `readyEntry` retires a crashed session by marking it stopped and running `closeExitedSession`, then retries spawn. A failed respawn enters failed and applies `spawnFailureBackoff`. Each entry's `cycleMu` serializes open, request, and close in `diagnostics.go` and `navigate.go`.

`awaitReady` gates navigation, hover, and symbol queries; diagnostics do not wait. A completed `$/progress` begin-to-end cycle marks the session ready (`processReadinessProgress`), and a begin stops the grace timer. If `ready_grace_period` expires with no begin, the session is ready. If `ready_timeout` expires first, `awaitReady` returns `incomplete=true` with a nil error and the request proceeds.

## Diagnostics, caching, and tools

Diagnostics come from `publishDiagnostics` pushes after `didOpen`, not pull diagnostics. Post-mutate injection (`ReadyForFile`) uses only ready sessions, bounded by `maxMutateDiagnosticsFiles` and `maxMutateDiagnosticsLines` (`mutate_diagnostics.go`).

`resultCache` is an LRU of `resultCacheMaxEntries` (512). Keys cover the server, root, file, the SHA-256 of the file bytes (`hashFileContent`), and the position for location queries. A read failure gives an empty hash and skips the cache. Location queries, document symbols, and diagnostics use it; hover and workspace symbols do not. `Manager.Close` clears it.

`ToolDefs` (`internal/lsp/tooldef.go`) returns seven definitions in deterministic order. `lsp_symbols` with `file` calls `textDocument/documentSymbol` and filters by `query`. With only `query`, `WorkspaceSymbols` queries enabled servers concurrently and merges results best-effort. An existing session for a server is reused (`existingSessionKeyForServer`) rather than spawning another root.

## Environment and storage

Stderr goes to `<session log minus extension>-lsp<ext>` when logging is configured (`ServerLogPath`, `internal/lsp/server_log.go`); `.log` is used when the session log has no extension. Otherwise `selectServerStderr` (`cmd/steiner/runtime_build_mcp.go`) discards it when `asyncMCP` is set and sends it to `os.Stderr` in other runs.

`buildServerEnv` (`internal/lsp/manager.go`) inherits the steiner environment and applies `srv.Env`. Unless `srv.Env` sets them, `HOME` and `XDG_CACHE_HOME` point at the cache directory and `GOCACHE` at its `go` subdirectory. `cacheDirFor` (`internal/lsp/cache.go`) uses `<cache_dir or os.UserCacheDir()>/steiner/lsp/<16 hex of sha256(root)>`, mode 0o700. Non-test code never removes it.

## Calibration

The four timeout defaults in `internal/config/defaults.go` (`request_timeout`, `ready_timeout`, `ready_grace_period`, `diagnostics_window`) come from the harness `internal/lsp/calibrate_manual_test.go`. It sits behind the `lspcalib` build tag, so `go test ./...` skips it. Run it with `go test -tags lspcalib ./internal/lsp/ -run TestCalibrate -v -timeout 30m`. It needs gopls: `resolveGopls` checks `STEINER_LSP_CALIB_GOPLS`, then `PATH`, `$GOBIN`, and `$GOPATH/bin`. `idle_timeout` is policy, not calibrated.

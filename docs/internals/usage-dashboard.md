# Subscription usage dashboard internals

User reference: [Subscription usage dashboard](../user/usage-dashboard.md).

## Package and wiring

`internal/quota` imports only the standard library, `internal/fsatomic` (store writes), and `golang.org/x/sys/unix` (file locking). Callers pass in fetchers and observations. Provider fetchers are in `internal/provider/quota_*.go`, with `BuildQuotaAccounts` in `quota_registry.go`. The overlay is in `internal/tui/usage_overlay*.go`.

In `cmd/steiner`, `runtime_build.go` builds the service and store, and `primeQuota` registers configured accounts at startup. `MarkUsed` is called from the provider factory closure (`runtime_build_provider.go`) and from `cliRunner` (`runner_run.go`) before cache routing, so cached Codex WebSocket providers are marked too.

## Service

- `Fetcher.Fetch` returns a `Snapshot`. A `TimeoutFetcher` overrides the 10-second default timeout. `FetchOptions.AllowSpawn` is true for `RefreshAll` and false for `RefreshUsed`; only the Claude fetcher reads it.
- `Views` returns one row per registered account, sorted by display alias, which is the alphabetically first alias. Stored snapshots without a registration are never shown, but stay in the store.
- `commit` releases the service lock before persisting. Persist failures are logged, and the in-memory state still updates.

## Passive observations

- `claudeSubQuotaObservedGetUsage` wraps the Claude usage call. `parseCodexRateLimitsFrame` parses Codex rate-limit frames.
- `Observe` stamps a zero `FetchedAt` with the service clock. `mergePassive` overwrites windows with the same label and keeps the others. It keeps the existing provider type, plan, and credits when the observation has none, and replaces budgets when it has any. It takes the observation's time and source.
- A successful fetch replaces the latest snapshot and clears the error. A failed fetch keeps the snapshot and stores the error, truncated to 80 runes. Passive observations do not clear that error, so it can show beside newer data until the next successful fetch.

## Store

- `Append` creates the directory with mode `0o700`, opens `<path>.lock` (mode `0o600`, tightened on each append), and takes a blocking exclusive `flock`.
- Under the lock it reads the file, merges, prunes old snapshots, and writes through `fsatomic.WriteFile`: a synced temp file renamed over the data file.
- `Latest` and the history read in `Views` take no lock. Each sees the old or the new file, never a partial one.
- Locking is unix-only. Elsewhere `lockFile` returns an error, so `Append` writes nothing.
- A file that fails to decode makes `Append` fail, so history is never overwritten. `Views` treats an unreadable history as empty and logs the error.

## Projection

`ProjectWindow` and `ProjectBudget` extrapolate linearly to capacity from the oldest history point for the same account, label, and reset period (reset times within one minute). Times come from the current snapshot's `FetchedAt`, not the wall clock.

## In-memory state

`Service` loads the latest snapshot per account from the store once, on first use. Afterwards the displayed value changes only through this process's fetches and observations. Other processes' writes reach the overlay only through projection history, so a value can stay stale until this process fetches that account.

## TUI wiring

- `openUsageOverlay` shows cached views and starts `RefreshAll` under a 30-second context. Views are read only in commands (`loadUsageViewsCmd`, `usageRunFinishedCmd`). `handleQuotaUpdateMsg` calls `Updates()`, a capacity-one channel whose notifications coalesce, and checks overlay state.
- A row shows a spinner and `refreshing…` while its account or the overlay is refreshing. That takes precedence over age. Settled rows show `updated <age> ago`, then any `unavailable: <err>`.
- `needsTicking` includes `IsOpen() && refreshing()`. `handleTickMsg` advances the spinner only while open and refreshing. Closing keys start no ticker.
- `usageRunFinishedCmd` runs on run-finished events. It returns nil when there is no service or the interval is not positive. Otherwise it calls `RefreshUsed` with that interval as the minimum snapshot age.
- `tui.quota_refresh_interval` (default `5m`) is validated by `validateTUIConfig`, which rejects negative values and accepts zero. `quotaTUIConfig` passes it to the TUI. Opening the dashboard and pressing `r` do not read it.

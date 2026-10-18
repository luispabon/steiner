# Subscription usage dashboard internals

User-facing documentation: [Subscription usage dashboard](../user/usage-dashboard.md).

## Package layout

`internal/quota` is a leaf domain package. It imports only the standard library and `golang.org/x/sys/unix`, and it does not import the provider, agent, TUI, or config packages. Callers supply fetchers and observations.

| File | Responsibility |
| --- | --- |
| `snapshot.go` | `Snapshot`, `Window`, `Budget`, `Source`, `Fetcher`, `TimeoutFetcher`, and `FetchOptions`. |
| `account.go` | `Account`, `APIKeyAccountKey`, and `IsLegacyAPIKeyAccountKey`. |
| `service*.go` | `Service`: registration, observations, fetch and refresh, merge, and views. |
| `store.go`, `lock_unix.go`, `lock_other.go` | `Store`: the locked, atomically replaced JSON history file. |
| `projection.go` | Trend projections for windows and budgets. |
| `labels.go` | `WindowLabel` for standard window lengths and `BudgetLabelSpend` for spend-only budgets. |

Provider fetchers live in `internal/provider/quota_*.go`: `quota_registry.go` (`BuildQuotaAccounts`), `quota_claudesub.go`, `quota_codex.go`, and `quota_apikey.go`. The overlay is in `internal/tui/usage_overlay*.go`. In `cmd/steiner`, `runtime_build.go` builds the service and store, `primeQuota` in `interactive_session.go` registers accounts at startup. `buildRuntimeProviderFactory` (`runtime_build_provider.go`) returns a closure that calls `MarkUsed` on every provider build. `cliRunner.runtimeProvider` (`runner_run.go`) also calls `MarkUsed` on every call, before cache routing. The second call is needed for cached Codex WebSocket routing: a cache hit in `cachedCodexWSProvider` returns the cached provider without calling the factory.

## Fetcher, observer, and Service

- `Fetcher.Fetch(ctx, FetchOptions)` returns a full `Snapshot` marked `SourceFetch`. A fetcher can also implement `TimeoutFetcher`. A positive value overrides `ServiceOptions.FetchTimeout`, which defaults to 10 seconds.
- `FetchOptions.AllowSpawn` is true for `RefreshAll` and false for `RefreshUsed`. Only the Claude fetcher reads it.
- The sink is a plain `func(quota.Snapshot)` callback. The provider layer receives `Service.Observe` through its config and calls it with `SourcePassive` snapshots.
- `Service` holds the registered accounts by key, an alias to key map, the latest snapshot per key, in-flight flags, the last fetch error per key, the aliases marked as used, and the last successful append time per key. Its methods are safe for concurrent use, and it never holds its mutex during store I/O.
- `MarkUsed` may run before `Register`. The alias is resolved in `RefreshUsed`.
- `Updates()` returns a channel with capacity one. Notifications coalesce, and the TUI re-arms one waiter after each update.

## Passive sources

| Source | Where | Notes |
| --- | --- | --- |
| Claude `get_usage` | `claudeSubQuotaObservedGetUsage` wraps the usage-gate call in the pool | Observed only when the pool has a `QuotaObserver` and the reply parses. The gate receives the raw reply unchanged. |
| Codex WebSocket | `parseCodexRateLimitsFrame` on `codex.rate_limits` frames in `codex_responses_stream.go` | Other frame types are ignored. The account key comes from the `ChatGPT-Account-ID` header. |
| Codex HTTP | `codexQuotaObserveResponse` reads `x-codex*` headers | A family is found by its `-primary-used-percent` header. The main family is `x-codex`. Other families are labelled with the family name, for example `codex_other 5h`. |

Passive snapshots have a zero `FetchedAt`, and `Observe` stamps it with the service clock.

## Passive merge and fetch replace

- A fetch calls `commit(snap, replace=true)`. The latest snapshot is replaced as a whole, and a successful fetch clears the account's error.
- A failed fetch keeps the previous snapshot and stores the error, truncated to 80 runes. Passive observations do not clear that error, so an older error can show beside newer passive data until the next successful fetch.
- A passive observation calls `commit(snap, replace=false)`, which runs `mergePassive(prev, obs)`:
  - windows with the same `Label` are overwritten, and other windows are kept;
  - empty `ProviderType`, `Plan`, and `Credits` keep the previous values;
  - a non-empty `Budgets` list replaces the previous budgets rather than merging by label;
  - the result takes the observation's `FetchedAt` and `Source`.
- With no previous latest snapshot, a passive observation is stored as it is.

## Account keys and dedupe

| Provider | Account key |
| --- | --- |
| `codex` | `codex:<ChatGPT account ID>`, or `codex:unknown` when not signed in. |
| `claude_subscription` | `claude_subscription`, shared by every alias of that type. |
| `opencode_go`, `openrouter`, `litellm` | `provider:<configured alias>`, from `APIKeyAccountKey`. The key does not depend on the API key, base URL, project, or provider type. The fetcher copies the same key into its snapshots. |

- Several aliases can map to one key. `Views` returns one `AccountView` per key. `DisplayAlias` is the alphabetically first alias, or the key when there is none.
- `Views` also returns keys that have a stored snapshot but no registration, sorted by `DisplayAlias` and then `Key`.
- Legacy rows: `Views` skips an unregistered key when `IsLegacyAPIKeyAccountKey` matches it. That is the old `<provider type>:<16 lowercase hex>` fingerprint of an `opencode_go`, `openrouter`, or `litellm` account. Such snapshots are not migrated or deleted, and age out under the normal 35-day retention. Other unregistered keys, such as `codex:*` and `claude_subscription`, still appear.
- Persistence dedupe: `commit` skips the store when the new state equals the latest state (ignoring `FetchedAt` and `Source`) and the account's last successful append was less than an hour ago. `Store.Append` makes the same check against the newest stored snapshot, so separate processes also avoid duplicates.

## Store

- The file format is `{"version": 1, "snapshots": [...]}`. The path is described in the [user page](../user/usage-dashboard.md#history-and-privacy).
- `Append` creates the directory with mode `0o700` and opens `<path>.lock` with mode `0o600`, tightening it on each append. It takes a blocking exclusive `flock`, reads the current file, merges, prunes, and writes. On non-Unix builds, `lockFile` returns an error, so `Append` fails and nothing is written without a lock.
- `write` writes a temp file in the same directory and renames it over the data file. `Latest`, and the history read in `Views` take no lock. The rename keeps them consistent.
- Each `Append` drops snapshots fetched more than 35 days before its clock time.
- A file that fails to decode makes `Append` fail, so history is never overwritten. The failure is logged, and the in-memory state still updates.
- `Views` reads the history on every call. A read error counts as no history.

## Projection

`ProjectWindow` and `ProjectBudget` use the oldest history point for the same account, label, and reset period, where reset times are within one minute. `estimateEmpty` extrapolates linearly to capacity: 100 for windows, or the limit for budgets. It returns no projection when the span is under 15 minutes or the rise is under 2 percentage points for windows, or 2% of the limit for budgets. Times come from `current.FetchedAt`, not the wall clock. `BeforeReset` is true when `EmptyAt` is before the current reset time.

## In-memory state and other processes

`Service` loads `latest` from the store once, on first use (`ensureLoaded`). After that, the displayed snapshot changes only through this process's fetches and observations. Snapshots that another steiner process writes reach the overlay only through the history read used for projections. A displayed value can therefore stay stale until this process fetches or observes that account. A restart reloads the latest state.

## TUI wiring

- `openUsageOverlay` shows the cached views, starts `RefreshAll` under a 30-second context, and loads views in `loadUsageViewsCmd`. `Update` never reads the quota service directly.
- `quotaUpdateMsg` reloads the views when the overlay is open.
- `RunFinishedEvent` runs `usageRunFinishedCmd`, which calls `RefreshUsed(context.Background(), interval)`. It returns nil when `interval` is zero or negative.
- Per-account in-flight flags drive the `refreshing…` text. During a global refresh, a row with no snapshot and no error also shows it. Settled rows keep their age or error.
- `tui.quota_refresh_interval` is validated by `validateTUIConfig` and passed to the TUI by `quotaTUIConfig`. See [TUI quota refresh interval](configuration.md#tui-quota-refresh-interval).

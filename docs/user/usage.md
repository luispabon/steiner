# Usage and cache statistics

Steiner shows two kinds of usage information in the interactive TUI: subscription and spend limits reported by provider accounts (the `/usage` dashboard), and prompt-cache hit rates measured from model responses (the sidebar and `/cache-stats`).

## Usage dashboard

The usage dashboard is an overlay that shows the rate-limit windows and spend budgets each configured provider account reports, with a trend for windows and budgets that have enough history. It exists only in the interactive TUI.

| Key or command | Action |
| --- | --- |
| `ctrl+o` or `/usage` | Open the dashboard. |
| `ctrl+o` or `esc` | Close the dashboard. |
| `r` | Refresh every account now. |
| `↑` and `↓` | Scroll when the content is taller than the terminal. |

Opening the dashboard shows the stored values at once and refreshes every account in the background. The header shows `refreshing…` while the refresh runs.

### Supported providers

| Provider type | Source | Shows |
| --- | --- | --- |
| `codex` | ChatGPT usage endpoint, using your Codex sign-in, plus rate-limit information on normal Codex responses. | Rate-limit windows, the plan, and credits. |
| `claude_subscription` | The `claude` CLI's usage reply, plus the usage check Steiner makes before each turn. | Five-hour and weekly windows, extra weekly windows when reported, and the plan. |
| `opencode_go` | OpenCode's usage endpoint. | Rolling, weekly, and monthly windows. |
| `openrouter` | OpenRouter's key endpoint. | Budgets or the key limit, and daily, weekly, and monthly spend. |
| `litellm` | LiteLLM's key info endpoint. | The max budget, or spend alone when no max budget is set. |

Other provider types never appear on the dashboard, because Steiner has no usage source for them. Check the provider's own console instead.

Only providers in the current config appear. Each API-key provider gets one row per provider key. Two Codex providers that use the same ChatGPT account share one row. A Codex sign-in that uses an OpenAI API key rather than a ChatGPT account shows `API mode: no quota to report`.

### Reading the dashboard

Each account has its own block:

- **Header.** A `●` marks the provider the current session uses. The header shows the plan and how long ago the data was updated, or the error from the last failed fetch. An account that cannot be fetched shows why, for example `not signed in` or `no API key`.
- **Windows.** A bar, the percentage used, and the reset time, such as `14:05 (2h05m)`. The bar changes to warning style from 75% and error style from 90%. Standard window lengths show as `5h`, `daily`, `weekly`, or `monthly`.
- **Budgets.** `$used / $limit`, the period, and the reset time. Spend without a limit appears under `spend`.
- **Credits.** A text row, when the provider reports credits.

A failed fetch keeps the previous values and shows the error in the header.

### Trends

A trend row under a window or budget projects when usage will reach its limit, using a straight line from the oldest stored reading in the same reset period to the latest one:

- `└─ trend: empty by <time> (before reset) ✗` means that at this rate the limit is reached before the reset.
- `└─ trend: lasts past reset ✓` means usage lasts until the reset.
- `└─ trend: limit reached ✗` means the window already shows 100% used, so there is nothing left to forecast. It replaces the other rows even when no projection is available, and it appears whenever the shown percentage rounds up to 100 as well as at or above 100.

A trend appears only when the readings are at least 15 minutes apart, usage rose by at least 2 percentage points (2% of the limit for budgets), and the window or budget has a reset time. The projection uses two points, so one large turn can move it noticeably.

An `empty by` time is not shown once it has passed: a window whose projected limit time is at or before the current time shows no trend row instead.

### When usage refreshes

- **Opening the dashboard or pressing `r`** fetches every account, whatever the age of its data.
- **During normal use**, Steiner records usage that responses already carry: Codex rate-limit information and the Claude usage check before each turn. This makes no extra requests.
- **When a run finishes**, each account the run used is fetched again if its data is older than `tui.quota_refresh_interval` (default `5m`). See the [`tui` block](configuration.md#tui-block).

For Claude, Steiner reads usage through a running `claude` CLI session and never reads Claude credential files. If no session is running, opening the dashboard or pressing `r` starts a background CLI session that stays open until Steiner exits. The end-of-run refresh never starts one; with no running session it shows `no live claude session` and keeps the previous values.

## Cache hit rate

Steiner records prompt-cache token counts from every model response that reports usage, including sub-agent, advisor, and compaction calls. The cache hit rate is token-weighted:

```text
hit rate = cache-read input tokens / total input tokens
```

Total input tokens are uncached input plus cache-read and cache-write tokens. A dash in place of a rate means there is no data. A call that reports input but no cache reads counts as 0%. Models that do not report cache reads cannot produce a meaningful rate.

### Sidebar

The `PERFORMANCE` card in the sidebar shows two values under `cache hit`: the latest request (`latest req`) and the whole session (`session`). These cover the main conversation only, not sub-agent or advisor calls. `/clear` resets both; stored statistics are kept.

Sub-agent boxes show their own cache rate as each model request finishes, for example `Cache: NN.N% latest req · NN.N% session`. Finished compaction banners show the cache rate of the summary request.

### `/cache-stats`

`/cache-stats` opens a read-only overlay with these tables:

- **This session**: one row for all calls in the current session, including sub-agent and advisor calls.
- **Last hour**, **Last 24 hours**, and **Last 7 days**: one row per provider and model, from stored statistics. Advisor calls get separate rows marked `(advisor)`.

| Column | Meaning |
| --- | --- |
| Provider, Model | The provider key and backend model ID. |
| Hit rate | Token-weighted cache hit rate. |
| Cached / Total | Cache-read tokens and total input tokens. |
| Uncached/req | Average uncached input tokens per request. |
| Cached/req | Average cache-read tokens per request. |

The two per-request averages show whether a rate changed because uncached tokens grew or cached tokens shrank. The windows are fixed and use hourly buckets. Scroll with `↑` and `↓`, and close with `esc`.

The overlay includes the current session immediately. Statistics from other Steiner processes appear once those processes have saved them.

### Usage telemetry file

For headless runs, set `STEINER_USAGE_TELEMETRY` to a file path. Steiner appends one JSON line per model response that reports usage, and one per Codex WebSocket connection event. `STEINER_USAGE_TELEMETRY_RUN` adds a run ID to each line. Recording is off unless the variable is set, and Steiner never reads the file back.

```bash
export STEINER_USAGE_TELEMETRY=~/.local/state/steiner/telemetry.jsonl
export STEINER_USAGE_TELEMETRY_RUN=batch-job-1
steiner --exec "summarize this repository"
```

Usage lines contain a timestamp, the optional run ID, the source, provider and model identity, and token counts. They contain no prompt or response text. For per-request cache diagnostics, see the [`diagnostics` block](configuration.md#diagnostics-block).

## Session start and runtime

The `PERFORMANCE` card also shows when the current session started and how long it has worked.

- `started` is the local time of the first prompt sent after launching the TUI or after `/resume`. It reads `09:30` when that prompt was sent today, `09:30 yesterday` when it was sent on the previous local calendar day, and `2026-11-01 09:30` when it was older. It shows `n/a` until you send that prompt, and it moves to the next form at local midnight without a new prompt.
- `runtime` is the time the session spent working since that prompt, as `HH:MM:SS`, and shows a dash until then. It advances while the main conversation is generating a reply, while at least one sub-agent is queued or running, and while the conversation compacts.

Overlapping work counts once: a generating turn with sub-agents running adds a single span. Runtime stops while the session is idle, and while the conversation is parked on finished sub-agent results you have not answered yet. It resumes on the next prompt or sub-agent start.

Both rows reset on `/clear`, `/fork` and `/resume`. The runtime is not saved, so it covers the current TUI process only; the start time uses your machine's local time zone. After `/resume`, the rows stay empty until the next prompt.

## What is stored

Both features store data under `$XDG_STATE_HOME/steiner/`, or `~/.local/state/steiner/` when `XDG_STATE_HOME` is unset. You can delete these files; Steiner starts them empty again.

| File | Contents | Retention |
| --- | --- | --- |
| `quota-history.json` | Usage dashboard readings: account identity, provider type, plan name, window and budget figures, reset times, credit text, fetch times, and whether each reading came from a fetch or from normal traffic. | Readings older than 35 days are removed on the next write. |
| `cache-stats.json` | Cache statistics in hourly buckets: provider key and type, backend model ID, source (parent, sub-agent, or advisor), request count, and input, cache-read, cache-write, and output token counts. | 8 days. |

Neither file contains prompts, responses, file contents, or API keys. API-key accounts on the dashboard are identified by their provider key, not by the key or base URL. A removed provider's dashboard history stays in `quota-history.json` until it ages out, and reappears if you add the provider back.

If `cache-stats.json` is corrupt or was written by a schema version your Steiner build does not support, its stored history shows as empty, new cache statistics are never saved, and the refusal is logged. Steiner does not overwrite a file it cannot read. To resume saving, keep a copy of the file and then either repair it, restore it from a backup, or run a Steiner version that supports its schema version; otherwise move it aside and Steiner starts a new, empty file.

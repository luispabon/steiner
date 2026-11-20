# Usage and cache statistics

Steiner shows two kinds of usage information in the interactive TUI: subscription and spend limits reported by provider accounts (the `/usage` dashboard), and token usage measured from model responses (the sidebar and `/token-stats`).

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

A trend row under a window or budget estimates when usage will reach its limit. One of two rules produces it:

- **Claude subscription five-hour window.** The estimate uses the average usage over the window so far: Steiner assumes the window started empty five hours before the reset, at the start of the current window, and extends the current percentage over the elapsed part of the window. The subscription's weekly windows use the next rule.
- **Every other window and every budget.** The estimate takes the oldest stored reading in the same reset period and extends it linearly to the limit, using the usage and the time between that reading and the latest one.

The row then reads:

- `└─ trend: empty by <time> (before reset) ✗` means that at this rate the limit is reached before the reset.
- `└─ trend: lasts past reset ✓` means usage lasts until the reset.
- `└─ trend: limit reached ✗` means the window already shows 100% used, so there is nothing left to forecast. It replaces the other rows even when no projection is available, and it appears whenever the shown percentage rounds up to 100 as well as at or above 100.

A trend appears only when the readings are at least 15 minutes apart, usage rose by at least 2 percentage points (2% of the limit for budgets), and the window or budget has a reset time. For the five-hour average, the two readings are the inferred window start and the current reading, so the window must be at least 15 minutes old and show at least 2% used; a younger window or a smaller rise falls back to stored readings when they allow an estimate.

An `empty by` time is not shown once it has passed: a window whose projected limit time is at or before the current time shows no trend row instead. Once the five-hour window's reset time has passed or arrived, no forecast appears, because the reading then belongs to the next window.

The window start is inferred from the reset time and is not observed. The five-hour average therefore describes the steady pace of the window so far: a recent spike can leave it understating current usage, and it says nothing about what the next request will cost. The history rule uses two points, so one large turn can move it noticeably.

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

The `PERFORMANCE` card in the sidebar shows two values under `cache hit`: the latest request (`latest req`) and the whole session (`session`). When the newest main-conversation call was a context compaction, the latest value is labelled `compaction req` instead, so a compaction's low hit rate is not mistaken for a normal turn. These cover the main conversation only, not sub-agent or advisor calls. `/clear` resets both; stored statistics are kept.

Sub-agent boxes show their own cache rate as each model request finishes, for example `Cache: NN.N% latest req · NN.N% session`. Finished compaction banners show the cache rate of the summary request.

### `/token-stats`

`/token-stats` opens a read-only, scrollable overlay titled **Token usage**. From top to bottom it shows:

1. A large ASCII headline for total tokens over the last calendar month, with requests, cache hit share, and cached, uncached, and output tokens. On narrow terminals the headline falls back to a bold line.
2. **This week**, Monday to Sunday, with one bar per day.
3. **Previous weeks**, Monday to Sunday, with one line per week. The oldest week may be partial.
4. Tables for **This session**, **Last hour**, **Last 24 hours**, **Last 7 days**, and **Last calendar month**. Each has a summary of requests, the percentage of input from cache, output per request, and counts of models and providers.
5. **By source (last calendar month)**, including each source's share of total tokens.

Each table has Provider, Model, Reqs, Uncached (uncached input), Read (cache read), Write (cache write), Output, Reason (reasoning tokens, part of Output), Total, Hit (hit rate), and Cost columns, plus a Total row. Source rows (`parent`, `sub_agent`, `advisor`) appear nested under a model only when it has more than one source. Advisor calls are included as an `advisor` source, not as separate model rows.

```text
Uncached in = prompt tokens - cache read - cache write
Total = uncached in + cache read + cache write + output
Hit rate = cache read / (uncached in + cache read + cache write)
```

Reason is part of Output and is never added to Total. Uncached input is floored at zero for each response. Numbers use raw values below 1,000 and one-decimal `k`, `M`, or `B` above that; zero is shown as `-`.

Hit rate counts only requests whose provider reported cache data, so a provider that says nothing about caching is not counted as a miss. When no request in a group reported cache data, Hit shows `-`.

The Cost column shows what the group's requests cost, and the Total column follows the same rules for the whole table:

- `-` when no request in the group has a price.
- `<$0.01` when the amount is positive but below one cent.
- `~$3.12` when the amount is estimated from the model's models.dev prices.
- `$3.12` when every priced request reported its own cost, as OpenRouter does. Provider-reported amounts are used as they are.
- A trailing `+` when some requests in the group have no price at all; those requests are not included in the amount.

Estimates use models.dev base rates plus context-length tiers: a request whose prompt exceeds a tier's size is priced entirely at that tier's rates. They do not account for service tiers, flex or priority processing, or audio. Cache writes are priced at the first (5 minute) cache TTL rate, so one-hour cache writes, such as advisor calls, may be underpriced. Subscription providers show API-equivalent cost, not what the subscription charges.

Cost is fixed when the request is recorded and is never repriced. Models that models.dev does not price, such as local models and unmatched model IDs, stay unpriced, and calls recorded while the models.dev cache is unavailable stay unpriced.

The last calendar month runs from the same day and time of the previous month to now in UTC, clamped to the last day when the previous month is shorter. Days and Monday-to-Sunday weeks use UTC. Windows use hourly buckets, so the lower edge can include up to one extra hour. Scroll with `↑` and `↓`, and close with `esc`.

The overlay includes the current session immediately. Statistics from other Steiner processes appear once those processes have saved them. Data removed under the former 8-day retention period cannot be recovered, so monthly figures fill up over time.

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
| `cache-stats.json` | Token statistics in hourly buckets: provider key and type, backend model ID, source (parent, sub-agent, or advisor), request count, input, cache-read, cache-write, output, and reasoning token counts, cache-reported request and input counts, cost in micro-USD, and reported-cost and unpriced request counts. | 33 days. |

Neither file contains prompts, responses, file contents, or API keys. API-key accounts on the dashboard are identified by their provider key, not by the key or base URL. A removed provider's dashboard history stays in `quota-history.json` until it ages out, and reappears if you add the provider back.

If `cache-stats.json` is corrupt or was written by a schema version your Steiner build does not support, its stored history shows as empty, new cache statistics are never saved, and the refusal is logged. Steiner does not overwrite a file it cannot read. To resume saving, keep a copy of the file and then either repair it, restore it from a backup, or run a Steiner version that supports its schema version; otherwise move it aside and Steiner starts a new, empty file. The current file is schema version 3, so older Steiner binaries cannot read it: they treat it as an unsupported version, show no stored history, and stop saving new statistics until their file is moved aside.

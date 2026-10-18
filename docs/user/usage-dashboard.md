# Subscription usage dashboard

The usage dashboard is an overlay in the interactive TUI. It shows the rate-limit windows and spend budgets that each configured provider account reports, with a trend for each window or budget that has enough history.

## Open and close

| Key or command | Action |
| --- | --- |
| `ctrl+o` or `/usage` | Open the dashboard. |
| `ctrl+o` or `esc` | Close the dashboard. |
| `r` | Refresh every account now. |
| `↑` and `↓` | Scroll when the body is taller than the terminal. |

Opening the dashboard shows the stored values at once and refreshes in the background. Refreshing accounts show an animated spinner and `refreshing…` in their header while a fetch runs. The dashboard exists only in the interactive TUI.

## What it shows

Each account has its own block, and a rule of `─` characters separates adjacent blocks:

- **Header.** A `●` marks the provider that the current session uses. The header shows the account's display alias in bold accent. The right side shows the plan in bold accent, then `updated <age> ago`, `<spinner> refreshing…`, or `unavailable: <error>` for the last failed fetch. An account with no usable source shows the reason instead, for example `not signed in`, `no API key`, or `API mode: no quota to report`.
- **Windows.** A bar, the percentage used, and the reset time. Period labels are bold in the normal foreground colour. Reset times use the compact form `<time> (<time left>)`, such as `14:05 (2h05m)`. The bar uses warning style from 75% and error style from 90%.
- **Budgets.** A budget with a limit shows `$used / $limit`, its period, and its reset time. Spend-only values appear under a bold `spend` label.
- **Credits.** A text row, when the provider reports credits.

Reset times show the clock time on the same day, a weekday and time within seven days, and a date beyond that. The time left shows as `4d`, `2h05m`, `15m`, or `<1m`.

Standard window lengths (within 5% of 5 hours, 24 hours, 7 days, or 30 days) show as `5h`, `daily`, `weekly`, or `monthly`. Other lengths show as a short duration, such as `2h` or `3d`.

## Trend

A trend row under a window or budget projects when usage reaches its limit. The projection is a straight line from the oldest stored snapshot for the same window and reset period to the current snapshot. It appears only when:

- the baseline snapshot is at least 15 minutes older than the current snapshot;
- usage rose by at least 2 percentage points (for budgets, at least 2% of the limit);
- the window or budget has a reset time, and a budget has a positive limit.

| Row | Meaning |
| --- | --- |
| `└─ trend: empty by <time> (before reset) ✗` | At this rate, usage reaches the limit before the reset. The trend text uses warning (orange) style and the ✗ uses error (red) style. |
| `└─ trend: lasts past reset ✓` | At this rate, usage lasts past the reset. The ✓ uses success (green) style. |

The projection uses two points, so one large turn can change it noticeably.

## When snapshots refresh

| Trigger | What happens |
| --- | --- |
| Open (`ctrl+o`, `/usage`) or `r` | Fetches every account that has a source, whatever the age of its snapshot. Each fetch is limited to 10 seconds, or 20 seconds for Claude. The whole refresh stops after 30 seconds. Claude may start a probe session (see [Claude subscription](#claude-subscription)). |
| Passive traffic | Steiner records usage that provider responses already carry: Codex rate-limit headers and WebSocket frames, and Claude usage replies read before each turn. No extra request is made. A passive reading updates only the windows it reports. |
| End of a run | When a run finishes, each account whose provider alias the run used is refetched if its latest snapshot is older than `tui.quota_refresh_interval` (default `5m`). This refresh never starts a Claude probe. |

A failed fetch keeps the previous snapshot and shows the error in the account header.

While a refresh is in flight, refreshing accounts show the animated spinner, and the overlay keeps ticking until every refresh has returned, so the spinner advances.

## Supported providers

| Provider type | Source | Shows |
| --- | --- | --- |
| `codex` | ChatGPT usage endpoint, using the stored Codex sign-in. Passive rate-limit headers and frames from Codex requests. | Rate-limit windows for the main limit and any additional limits, the plan, and credits. |
| `claude_subscription` | `get_usage` on a running `claude` CLI session. Passive usage replies read before each turn. | Five-hour and weekly windows, plus weekly opus, sonnet, and apps windows when reported. The plan. |
| `opencode_go` | `/usage` on the configured base URL. | Rolling, weekly, and monthly windows. |
| `openrouter` | `/key` on the configured base URL. | Budgets, or the key limit, and daily, weekly, and monthly spend. |
| `litellm` | `/key/info` on the configured base URL, with a trailing `/v1` removed. | The max budget, or spend alone when no max budget is set. |

Provider types not in this table never appear on the dashboard. These are `openai`, `anthropic`, `gemini`, `openai_compat`, `ollama`, `lmstudio`, and `opencode_zen`. Steiner has no quota source for them, so check the provider's own console for usage.

A Codex sign-in that uses an OpenAI API key, rather than a ChatGPT sign-in, shows `API mode: no quota to report`, and Steiner does not fetch it.

Two Codex aliases for the same ChatGPT account show as one row.

API-key providers have one row per configured alias. Two aliases with the same base URL and key still show as two rows. Changing an alias's API key, base URL, or provider type keeps its row and its history. Rows from earlier Steiner versions, which were keyed by a hash of the base URL and key, do not appear. Their stored history is not migrated and ages out with the normal 35-day retention. The row for a current alias fills in after its next fetch.

## Claude subscription

Steiner reads Claude usage through the `claude` CLI. It never reads Claude credential files.

- With a live `claude_subscription` session, usage is read through that session's control channel. This works while the session runs a turn. Steiner prefers a non-advisor session.
- With no live session, opening the dashboard or pressing `r` starts a probe session. The probe stays in the session pool until Steiner closes the pool, so later refreshes reuse it.
- The end-of-run refresh never starts a probe. With no live session, it records `no live claude session` as the error and keeps the previous snapshot.

## History and privacy

- Snapshots are stored in `quota-history.json` under `$XDG_STATE_HOME/steiner/`, or under `~/.local/state/steiner/` when `XDG_STATE_HOME` is unset. The lock file is `quota-history.json.lock` in the same directory. The directory is created with owner-only permissions.
- Snapshots fetched more than 35 days ago are removed the next time a snapshot is written.
- An unchanged snapshot is not stored again within an hour of the last stored snapshot for that account.
- Stored data is limited to account keys, provider types, plan names, window and budget figures, reset times, credit text, fetch times, and whether each snapshot came from a fetch or a passive observation.
- Raw API keys are never stored. API-key accounts are identified by their configured provider alias, so the API key and base URL are not part of the identity.
- Steiner does not read Claude credentials. The `claude` CLI holds the sign-in.
- An account that is no longer configured still appears with its last stored snapshot. The exception is a legacy API-key row from an earlier version, a fingerprint-keyed `opencode_go`, `openrouter`, or `litellm` account. Those rows stay hidden.

For the internal design, see [Subscription usage dashboard internals](../internals/usage-dashboard.md).

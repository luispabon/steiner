# Context management

Steiner keeps the conversation within the model's context window so long tasks do not fail on a hard limit.

## How context stays small

1. **Delegation.** Work done by a sub-agent never enters the main conversation; only its compact result does. See [Sub-agent delegation](sub-agent-delegation.md).
2. **Bounded sources.** Skills and project context each have a size cap, so no single source dominates.
3. **Bounded tool output.** Tools return a bounded preview. Cut-off `bash` output is saved to a file the agent can page with `read`, and `grep` returns one page at a time. See [Tools](tools.md).
4. **Compaction.** When the conversation gets large, older turns are summarised and replaced with the summary.

## Compaction

Automatic compaction starts when the prompt reaches about 70% of the model's context window. Set a different trigger per model with `models.definitions.<alias>.advanced.limits.compaction_threshold_tokens`; it must be positive and below the context window. See [Configuration](configuration.md#models-block).

Compaction is automatic: the agent pauses briefly, summarises, and carries on. After compaction the model still has:

- the system prompt and tool definitions
- summaries from earlier compactions
- the most recent conversation turns, verbatim
- your latest request from before the summarized part, verbatim, truncated past 16 KB or past 5% of the context window when that is smaller (images in it are not kept; a turn with only an image is skipped, so the request comes from your last turn with text). It holds the text you typed only: mode notices and sub-agent results from the same turn are left out. If keeping it alone would make the compaction fail, it is dropped and the banner says so
- a note of any sub-agents still running
- enabled skills, re-applied after the summary

The number of recent turns kept verbatim shrinks automatically. Steiner keeps the 3 newest turns when they fit the context window together with your latest request and room for the summary, otherwise 2, then 1. The emergency pass keeps 1. When the summary's verbatim copy of your latest request keeps the result above the compaction threshold, Steiner drops that copy and keeps the summary.

If even the newest turn does not fit, for example because of very large tool output, compaction stops with an error like this:

```text
compaction cannot solve this request: the newest unit does not fit with its summary reserve even without its request block: protected_outputs=N retained_tail=N request_block=N summary_reserve=N context_window=N safety_margin=N hard_limit=N
```

The values are token estimates. `protected_outputs` is the tool and sub-agent output of the newest turn that the model has not read yet. `retained_tail` is the rest of the kept turn. Start a new session and carry over what matters, and reduce tool output (narrower `read` ranges, `grep` patterns or command output). Compaction can also fail with `emergency compaction could not reduce context enough` when earlier turns are too large to summarise.

Compaction summaries are delivered as user-role historical context, not as system instructions. The model is told to treat them as a record of earlier conversation.

Everything else is reduced to the summary, so details can be lost. Each compaction loses a little more. If Steiner warns that a session is likely lossy, start a fresh session and carry over what matters. Keep important state in files or git, not only in the conversation.

### Manual compaction

`/compact` compacts now. Add focus text to steer the summary:

```text
/compact focus on the auth refactor
```

The focus text is added to the summarisation prompt alongside any `models.<name>.prompts.compaction` override. Automatic compaction is never steered. `/compact` is refused while the agent is generating. Manual and automatic compaction share the empty-summary retries and failure rules under [Empty summaries](#empty-summaries).

The summary request carries the tool definitions a normal turn would send. After `/resume` there is no in-memory copy of the parent request, so Steiner rebuilds the tool set from the current configuration and waits for MCP servers to finish connecting first. The rebuild follows the live configuration, not the saved session: a tool from a server that failed to connect, or one you changed since the save, can differ from what the parent request used.

### Compaction history archive

Interactive sessions and oneshot phases save the messages each compaction removes to `history.md` in `~/.config/steiner/sessions/<session-id>/`. It holds user and assistant text and short tool-call summaries, not tool output or model reasoning. The summary tells the model where the archive is so it can `grep` and `read` it for a missing detail. The summary's latest-request section is written inside a code block, fenced longer than any backtick run in the request, so markdown in it is not interpreted. The compaction result in the transcript renders it the same way.

Use `/recall` to view the current session's archive. `--exec` runs and sub-agents do not keep an archive. Deleting a session deletes its archive and recall files. A fork copies the parent's archive and recall files, then rewrites the inherited summary note and recall markers to the copies, so the fork's references keep resolving after the parent is deleted or evicted. If that copy fails, Steiner warns and the fork keeps the parent's paths.

### Empty summaries

If the summary model replies with no text (an empty reply, or tool calls only), Steiner asks again up to two more times, so a stage makes at most 3 summary calls. The retries repeat the same request with a short extra instruction: reply with plain text and do not call tools. The last retry also names up to 3 of the tool calls the model tried, with their arguments cut to 120 characters (the ellipsis included), and asks it to start directly with the summary. A reply that has text is used as the summary even when it also carries tool calls. The tool calls are ignored and a note says so. When a retry recovers, the note also lists each failed attempt's finish reason, the completion tokens the provider reported, its tool-call count, whether it had reasoning content, and why it was empty. A reply that stopped at the output limit (`length`) fails at once, because a retry would hit the same limit.

A fallback model gets one call. If it replies with no text, the compaction reruns on the main model as described below.

Each compaction operation makes at most 6 summary calls on the main model: up to 3 for the normal stage and up to 3 for the emergency stage. With a fallback model selected the worst case is 8 calls: up to 2 on the fallback model, then the main-model rerun of up to 6. An empty normal stage fails the operation without running the emergency stage.

If every attempt is empty, the compaction fails with an error and a single failed diagnostic that gives the attempts, the reason (`tool_calls`, `no_text`, `length` or `retry_does_not_fit`) and the tool-call count. It also lists each attempt with the same fields as the recovery note, and omits completion tokens the provider did not report. Completion tokens are shown as reported. Steiner does not use them to decide the outcome, because some providers leave reasoning tokens out of them. The conversation is left unchanged. Steiner does not continue uncompacted and does not build a placeholder summary. The next step depends on the reason:

- `no_text` or `tool_calls`: run `/compact` again, or switch to a model that writes text summaries.
- `length`: the reply used the whole output cap on reasoning or tool calls before writing any text. The summary output cap is the smaller of the model's `max_output_tokens` and a share of its context window. Use a model with a larger output limit, or raise `advanced.limits.max_output_tokens` for that model if the provider allows more. Running `/compact` again will likely hit the same limit.
- `retry_does_not_fit`: the retry request, with its extra instruction, is larger than the model's context budget, so it was not sent. The conversation is unchanged. Run `/compact` again, since the first reply may differ. If it repeats, switch to a model with a larger context window or start a new session.
- Provider errors are not empty summaries and have no reason. Fix the cause the error names, such as authentication, rate limits or the network, then run `/compact` again.

### Compaction fallback

When the main model's prompt cache has probably expired, a compaction can run its summary on a second model instead. This needs both `models.profiles.<profile>.compaction_fallback` and a positive cold-cache threshold on the main model's provider. The threshold is `compaction_fallback_after`; when that key is unset it uses the provider's `prompt_cache_ttl` (defaults: 5 minutes for `anthropic`, 60 for `claude_subscription`, 30 for `openai` and `codex`, off for other types). The fallback stays off when the resulting threshold is `0`; negative values are invalid. See [Configuration](configuration.md#providers-block).

If the fallback fails, the compaction reruns once on the main model. The compaction banner names the fallback model in its header as soon as the fallback is selected, before the summary finishes. A configured model alias is shown instead of `provider/model`. Click the banner to expand the model, the fallback reason, prompt tokens before and after, the compaction threshold, the number of messages summarized, the retained messages and compaction units, how many retained user messages are real user input versus sub-agent results, summary size and budget, and cache components. The summary row ends with `latest request kept verbatim (new)` or `latest request kept verbatim (carried)` when the summary carries your latest request, or `latest request dropped to fit` when it was dropped to keep the compaction within the context window. When the fallback is skipped the header reads `fallback skipped · <model used>`, where the model is the main model that ran the compaction, shown with the same alias rule; the reason stays in the expanded details and in plain and JSON output.

A resumed or forked session keeps the last main request it saved, so the cold-cache decision uses the real idle time rather than assuming no request. A request recorded for another model is ignored when the session resumes on the current model; the stored record is replaced by the next successful main request. A record whose prompt cache key no longer matches the session is discarded.

### Cache expiry indicator

When the main model's provider has a positive effective `prompt_cache_ttl`, the sidebar's `context` card shows a `cache` row counting down to the moment Steiner presumes the main prompt cache is cold. A compaction fallback model is not required. `openai`, `anthropic`, `claude_subscription` and `codex` providers have a default TTL; every other provider type defaults to `0`, so set `prompt_cache_ttl` explicitly to show the estimate. Steiner does not guess upstream cache retention. The countdown restarts after each successful request to the main model, except during an in-TUI oneshot run.

| Row text | Colour | Meaning |
|----------|--------|---------|
| `cache ~4m left` | dim | More than one minute remains, in whole minutes rounded up |
| `cache ~1m left` | muted amber | One minute or less remains |
| `cache expired` | amber | The cache is presumed cold |

Once the cache is presumed cold, the status bar also shows `cache expired` in amber after the context segment. The row is hidden when the effective `prompt_cache_ttl` is `0`, and until the next successful main request after a model switch that takes effect immediately, a successful profile switch, `/clear`, `/fork` or `/resume`. Requests made by an in-TUI oneshot run do not restart the countdown, and the estimate is cleared when the run finishes.

The indicator is an estimate from idle time, not confirmation from the provider, and it depends only on `prompt_cache_ttl`; `compaction_fallback_after` affects the compaction fallback alone. A model switch that takes effect immediately, a successful profile switch, `/clear`, `/fork` or `/resume` clears it. A model switch that applies only to the next run leaves the current estimate in place.

### `claude_subscription` limit

If the first response after a `claude_subscription` compaction still reports prompt usage at or above 90% of the context window, Steiner stops instead of compacting again and asks you to start a new session.

## Skills and context

Enabling or disabling a skill mid-session takes effect with your next message and does not rewrite earlier messages. A disabled skill's text stays in the conversation until the next compaction removes it. See [Skills](skills.md).

## Session date

Steiner records the local date when a session, `--exec` run, or oneshot run starts, for example `Current date: 2026-09-13 (BST, UTC+01:00), recorded when this session started.`, and includes it in every request. It does not change during the session, even past midnight, and compaction keeps the same line. A saved session stores the date and its timezone abbreviation: `/resume`, `--resume` and `/fork` keep the original line, while `/clear` and a new session record a new date. A session saved before Steiner stored the date keeps the date captured on its first resume.

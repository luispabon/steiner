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
- your latest message, verbatim, when it fits
- a note of any sub-agents still running
- enabled skills, re-applied after the summary

Everything else is reduced to the summary, so details can be lost. Each compaction loses a little more. If Steiner warns that a session is likely lossy, start a fresh session and carry over what matters. Keep important state in files or git, not only in the conversation.

### Manual compaction

`/compact` compacts now. Add focus text to steer the summary:

```text
/compact focus on the auth refactor
```

The focus text is added to the summarisation prompt alongside any `models.<name>.prompts.compaction` override. Automatic compaction is never steered. `/compact` is refused while the agent is generating.

### Compaction history archive

Interactive sessions and oneshot phases save the messages each compaction removes to `history.md` in `~/.config/steiner/sessions/<session-id>/`. It holds user and assistant text and short tool-call summaries, not tool output or model reasoning. The summary tells the model where the archive is so it can `grep` and `read` it for a missing detail.

Use `/recall` to view the current session's archive. `--exec` runs and sub-agents do not keep an archive. Deleting a session deletes its archive; a forked session starts its own archive.

### Compaction fallback

When the main model's prompt cache has probably expired, a compaction can run its summary on a second model instead. This needs both `models.profiles.<profile>.compaction_fallback` and a positive `compaction_fallback_after` on the main model's provider (defaults: 5 minutes for `anthropic`, 60 for `claude_subscription`, 30 for `openai` and `codex`, off for other types). See [Configuration](configuration.md#providers-block).

If the fallback fails, the compaction reruns once on the main model. The compaction banner notes `via <provider>/<model> (<reason>)` when the fallback ran, or `fallback skipped: <reason>` when it did not.

### `claude_subscription` limit

If the first response after a `claude_subscription` compaction still reports prompt usage at or above 90% of the context window, Steiner stops instead of compacting again and asks you to start a new session.

## Skills and context

Enabling or disabling a skill mid-session takes effect with your next message and does not rewrite earlier messages. A disabled skill's text stays in the conversation until the next compaction removes it. See [Skills](skills.md).

## Session date

Steiner records the local date when a session, `--exec` run, or oneshot run starts, for example `Current date: 2026-09-13 (BST, UTC+01:00), recorded when this session started.`, and includes it in every request. It does not change during the session, even past midnight. Resuming or forking a session records a new date.

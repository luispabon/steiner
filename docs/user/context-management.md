# Context Management

Steiner automatically manages the model's context window so you never hit a hard limit mid-task.

## How it works

Context management runs on three lines of defense, in order of preference:

1. **Delegation** — work delegated to a sub-agent never enters the parent context at all. Only a compact result envelope (`output`, `status`, and an optional `reason`) returns to the parent. This is the most effective defense; see [Sub-agent delegation](sub-agent-delegation.md) for the orchestrator role that gives the model this rationale directly.
2. **Per-source byte budgets** — when context does accumulate, each source (skills, project context) is capped so no single source dominates.
3. **Compaction** — when the conversation reaches the soft compaction trigger, older turns are automatically summarised and replaced with a compact handoff. The default trigger is approximately 70% of the effective context window.

### Tool output bounds

Tools bound their own output, and Steiner does not strip or rewrite tool text when results enter the conversation. A `bash` result shows a preview within `max_output_bytes`. When output is cut, it is saved to a private artifact file that can be paged with `read`. The artifact may be partial, and `read` cannot recover every artifact (see [Artifacts](tools.md#artifacts)). A `grep` result returns one page at a time with a `next_offset` to continue. See [Bash output](tools.md#bash-output) and [Grep output](tools.md#grep-output). If the artifact cap drops captured output, Steiner shows a nonfatal warning. The warning does not change the command outcome: a nonzero exit stays nonzero, and the tool call remains successful.

### Per-model compaction threshold

Set `models.definitions.<alias>.advanced.limits.compaction_threshold_tokens` to choose an explicit positive prompt-token soft trigger for one model. If it is absent, Steiner uses 70% of that model's effective context window. Zero or negative values are invalid, and a threshold at or above the final resolved context window is rejected. Native providers use estimated prompt tokens for this trigger. `claude_subscription` uses the reported prompt usage with its existing hysteresis and cap safety behavior.

This is a soft compaction trigger, not the hard request-fit limit. Hard fitting still accounts for the completion reserve and estimator pad, so a request can require compaction before or after the soft trigger depending on the space needed for the request. When a request carries no explicit max-tokens value, hard fitting caps the completion reserve at the space left after the prompt (minus the estimator pad), with a 4096-token floor. That cap chiefly matters when a model's advertised max output is close to or larger than its context window. An explicit max-tokens value (the request's max tokens, or `max_tokens`, `max_completion_tokens`, or `max_output_tokens` in params or extra params) lowers the completion reserve to that value, and compaction fitting uses the same lowered reserve.

### Token estimation

Steiner's pre-request estimate uses a local ctok count for three exact Claude model IDs: `claude-opus-5-5`, `claude-sonnet-5-5`, and `claude-haiku-5-5`. Surrounding whitespace is trimmed before matching. Other Claude model IDs, including older, future, prefixed, and dated IDs, keep the existing estimate.

The local count is approximate and low confidence. On recorded live CLI attempts (six per model, Claude CLI 2.1.296), it reduced the measured error by about 40% against the previous estimate. It still undercounts by about 1750 to 1755 tokens on the measured requests, because it does not model CLI and tool framing. It is not an exact provider count, and Steiner does not claim whole-request or billing exactness. Follow-up work on Opus is tracked in [ctok-go issue 1](https://github.com/luispabon/ctok-go/issues/1).

The estimate feeds hard request fitting. For native providers, it also feeds the soft compaction trigger. For `claude_subscription`, the soft trigger still uses the usage the CLI reports.

## What compaction means in practice

Compaction is transparent. The agent pauses briefly, summarises the older portion of the conversation, and continues with the summary in place of the raw history. You may notice a brief pause; no user action is required.

After multiple compactions the session can become progressively lossy. If Steiner warns that a session is "likely lossy", starting a fresh session is the safest option — prior work is already committed to disk or git.

### Manual compaction steering

Use `/compact` to compact the current conversation manually. Add optional focus text after the command, for example `/compact focus on the auth refactor`. Bare `/compact` behaves as before. Steering is added to the compaction summarisation prompt and composes with, rather than replaces, the `models.<name>.prompts.compaction` override.

Manual compaction reuses the tool definitions and cache prefix of your last top-level request, not a sub-agent request. Work delegated to a sub-agent before you run `/compact` therefore does not change the summarisation request's cached prefix, so the provider's prompt cache still applies.

Auto-compaction is never steered; it always uses an empty steering value. Manual compaction is refused while a run is generating. Session replacement is refused while a driver run, queued prompt, compaction, ungrouped busy work, or pending sub-agent result is unsettled. Finish or cancel busy work, then wait for result settlement before replacing or reopening the session.

## What survives compaction

- The system prompt and tool definitions (always in full)
- Summaries from previous compactions (chained forward)
- The most recent 1–3 conversation turns (verbatim)
- Your latest message, verbatim, when it fits the context budget even if it is older than those turns
- A restatement of any sub-agents still pending, added after the summary

With `claude_subscription`, the 70% trigger uses the total input usage the Claude CLI reports for the last response, including cached input. Steiner prefers a summary made in a forked CLI session, which applies only when the stored session state matches. When no fork is possible and a live session exists, the summary is appended in place to that session and reuses its full cache path. Without a live session, the summary is rebuilt from Steiner's transcript, and that rebuild may not reuse the prompt cache. Fork cache behavior is not yet confirmed against the live CLI.

If the first response after a `claude_subscription` compaction reports prompt usage at or above 90% of the configured context window, Steiner stops instead of compacting again, even when the request would still fit. It shows a critical message with restart guidance: start a new session and carry the context over.

### Skills in interactive sessions

Enabling or disabling a skill in an interactive session does not rewrite anything already sent, so the session's cached prefix is preserved; the change is delivered with your next message. An active skill survives compaction (it is re-applied after the summary), while a disabled skill disappears the next time compaction runs. A skill longer than 98304 bytes is truncated with a marker, and Steiner warns with the original size and the cap. The `/context` report shows leftover blocks for skills you have turned off as inactive until compaction removes them.

## Session date

Steiner captures the local calendar date when a session or standalone run begins — for example, `Current date: 2026-09-13 (BST, UTC+01:00), recorded when this session started.` — and includes it in every request. The date is captured only once per session identity (new session, resume, or fork uses a fresh capture), and per standalone exec or oneshot run; within a session it remains fixed even as wall-clock time advances.

The session date does not count against context budgets and is always delivered in full. See [Context Management Internals](../internals/context-management.md#session-date-assembly) for prompt-cache placement mechanics.

## What you can do

Nothing is required — context management is fully automatic. Persist important state to disk or git when it needs to survive the current session.

With `claude_subscription`, `/clear` starts a new empty session. It does not rewind the current conversation.

For the full assembly pipeline, budget tables, and escalation policy, see [Context Management Internals](../internals/context-management.md).

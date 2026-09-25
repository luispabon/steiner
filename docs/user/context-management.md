# Context Management

Steiner automatically manages the model's context window so you never hit a hard limit mid-task.

## How it works

Context management runs on three lines of defense, in order of preference:

1. **Delegation** — work delegated to a sub-agent never enters the parent context at all. Only a compact result envelope (`output`, `status`, and an optional `reason`) returns to the parent. This is the most effective defense; see [Sub-agent delegation](sub-agent-delegation.md) for the orchestrator role that gives the model this rationale directly.
2. **Per-source byte budgets** — when context does accumulate, each source (skills, project context) is capped so no single source dominates.
3. **Compaction** — when the conversation reaches approximately 70% of the context window, older turns are automatically summarised and replaced with a compact handoff.

## What compaction means in practice

Compaction is transparent. The agent pauses briefly, summarises the older portion of the conversation, and continues with the summary in place of the raw history. You may notice a brief pause; no user action is required.

After multiple compactions the session can become progressively lossy. If Steiner warns that a session is "likely lossy", starting a fresh session is the safest option — prior work is already committed to disk or git.

### Manual compaction steering

Use `/compact` to compact the current conversation manually. Add optional focus text after the command, for example `/compact focus on the auth refactor`. Bare `/compact` behaves as before. Steering is added to the compaction summarisation prompt and composes with, rather than replaces, the `models.<name>.prompts.compaction` override.

Auto-compaction is never steered; it always uses an empty steering value.

## What survives compaction

- The system prompt and tool definitions (always in full)
- Summaries from previous compactions (chained forward)
- The most recent 1–3 conversation turns (verbatim)

### Skills in interactive sessions

Enabling or disabling a skill in an interactive session does not rewrite anything already sent, so the session's cached prefix is preserved; the change is delivered with your next message. An active skill survives compaction (it is re-applied after the summary), while a disabled skill disappears the next time compaction runs. A skill longer than 98304 bytes is truncated with a marker, and Steiner warns with the original size and the cap. The `/context` report shows leftover blocks for skills you have turned off as inactive until compaction removes them.

## Session date

Steiner captures the local calendar date when a session or standalone run begins — for example, `Current date: 2026-09-13 (BST, UTC+01:00), recorded when this session started.` — and includes it in every request. The date is captured only once per session identity (new session, resume, or fork uses a fresh capture), and per standalone exec or oneshot run; within a session it remains fixed even as wall-clock time advances.

The session date does not count against context budgets and is always delivered in full. See [Context Management Internals](../internals/context-management.md#session-date-assembly) for prompt-cache placement mechanics.

## What you can do

Nothing is required — context management is fully automatic. Persist important state to disk or git when it needs to survive the current session.

For the full assembly pipeline, budget tables, and escalation policy, see [Context Management Internals](../internals/context-management.md).

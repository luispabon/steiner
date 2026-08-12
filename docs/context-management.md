# Context Management

Steiner automatically manages the model's context window so you never hit a hard limit mid-task.

## How it works

Context management runs on three lines of defense, in order of preference:

1. **Delegation** — work delegated to a sub-agent never enters the parent context at all. Only a bounded summary (≤1000 characters) returns to the parent. This is the most effective defense; see [Sub-agent delegation](sub-agent-delegation.md) for the orchestrator role that gives the model this rationale directly.
2. **Per-source byte budgets** — when context does accumulate, each source (skills, project context, tool summaries) is capped so no single source dominates.
3. **Compaction** — when the conversation reaches approximately 70% of the context window, older turns are automatically summarised and replaced with a compact handoff.

## What compaction means in practice

Compaction is transparent. The agent pauses briefly, summarises the older portion of the conversation, and continues with the summary in place of the raw history. You may notice a brief pause; no user action is required.

After multiple compactions the session can become progressively lossy. If Steiner warns that a session is "likely lossy", starting a fresh session is the safest option — prior work is already committed to disk or git.

## What survives compaction

- The system prompt and tool definitions (always in full)
- Summaries from previous compactions (chained forward)
- The most recent 1–3 conversation turns (verbatim)
- Scratchpad entries written by the agent

## What you can do

Nothing is required — context management is fully automatic. If you want important state to survive compaction reliably, the agent can write a scratchpad entry to preserve it explicitly.

For the full assembly pipeline, budget tables, and escalation policy, see [Context Management Internals](context-management-internals.md).

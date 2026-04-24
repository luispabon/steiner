# Stage 3: Context Discipline and Compaction - Final Summary

## High-Level Objectives

The primary goal of Stage 3 was to transition `steiner` from an append-only prompt assembly model to a bounded, policy-driven system. This allows for much longer sessions by managing context growth through explicit budgeting, compaction, and structured summaries, ensuring that critical information (user constraints, active tasks) survives even as raw conversation history is truncated or summarized.

Key objectives included:
- **Context Source Budgeting:** Implementing explicit limits for different types of context (e.g., conversation, tool results, project files).
- **Rolling Conversation Compaction:** Introducing logic to summarize older conversation turns into compact blocks while retaining recent turns verbatim.
- **Durable Context State:** Creating an agent-owned model for information that must persist across compaction cycles, such as active constraints and unresolved work.
- **Structured Tool Summaries:** Providing a way to truncate large tool outputs into informative summaries without allowing them to become authoritative instructions.
- **User-Visible Diagnostics:** Exposing compaction events and context usage (via `/history` or logs) so users can debug why certain information might be missing from the prompt.

## Implementation Overview

The implementation was carried out in three main stages:

### 1. Primitive Establishment (`internal/agent` & `internal/prompt`)
- Introduced new types in `internal/agent/context_state.go` to track durable intent (constraints, focus, unresolved tasks).
- Developed a policy layer in `internal/prompt` that manages budgets for different context sources and implements retention rules for conversation turns.
- Added mechanisms for generating rolling summaries of compacted history.

### 2. Runtime Integration & Diagnostics (`internal/agent/loop.go`, `internal/output`, `internal/repl`)
- Integrated the compaction logic into the main agent loop, ensuring that prompt assembly is performed according to the new policy every turn.
- Wired durable context updates through the loop so that decisions and constraints are captured and preserved.
- Added diagnostic event emission when budgets are exceeded or truncation occurs.
- Implemented a `/history` command in the REPL to allow users to inspect compacted history and context diagnostics.

### 3. Hardening & Verification
- Addressed critical edge cases identified during review, specifically:
    - **Cumulative Compaction:** Ensuring that multiple compaction passes preserve previously summarized history rather than overwriting it.
    - **Silent Truncation:** Fixing a bug where truncated raw conversation messages were not emitting diagnostics, ensuring all context loss is observable.
- Established comprehensive test coverage, including snapshot testing for assembled prompts and regression tests for long-session compaction behavior.

## Final Results

- **Bounded Context Growth:** Prompt size no longer grows linearly with session length; it stays within defined budget limits.
- **Durable Intent Preservation:** User constraints and active task focus reliably survive conversation compaction cycles.
- **Observability:** Users can use the `/history` command or inspect logs to see exactly what context was retained, summarized, or truncated due to budgets.
- **Stable Architecture:** Maintained strict package boundaries between `internal/agent`, `internal/prompt`, and `internal/output`.
- **High Test Coverage:** Verified via unit tests, integration tests, and golden snapshot testing for prompt assembly.

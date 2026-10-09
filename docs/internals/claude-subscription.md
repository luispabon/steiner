# Claude subscription: Internals

The `claude_subscription` provider is a stateful adapter around the user's signed-in `claude` CLI. It is unofficial and depends on CLI behavior that Anthropic may change. This page records the implementation that is currently tested, not a stable Claude protocol contract.

## Process pool and session identity

`ClaudeSubscriptionPool` owns CLI processes. A normal session is pooled by `TransportSession` alone, with the parent transport session and `default` used only when that value is absent. Model ID and reasoning effort are not pool-key components. The pool starts a process lazily, serializes calls for one key, and reuses the process until teardown.

The provider changes model live with the CLI's `set_model` control request. It changes effort with `apply_flag_settings` and `settings.effortLevel`. These changes update the existing session's state rather than creating a second process. Advisor mode uses a separate `|advisor` session key derived from the parent key. Advisor processes are tool-less and have their own append-only advisor transcript.

The pool locates `claude` once on `PATH`, checks `claude --version`, and checks `claude auth status`. It requires CLI version `2.1.294` or later, `authMethod: claude.ai`, and `apiProvider: firstParty`. It does not read a credential file. Process teardown, temporary-directory cleanup, advisor reaping, and shutdown deadlines are owned by the pool.

## CLI invocation

Normal sessions use the stream-json process shape:

```text
claude -p --input-format stream-json --output-format stream-json --verbose --include-partial-messages --model <model> --tools "" --strict-mcp-config --setting-sources= --no-session-persistence --settings {"autoCompactEnabled":false,"fastMode":false} --permission-mode dontAsk
```

When tools exist, the process also receives a private MCP config and an allowlist for `mcp__steiner__*`. A private system prompt is passed with `--system-prompt-file`. Reasoning `none` maps to `--thinking disabled`; other known effort values use adaptive thinking with summarized display and `--effort`. Steiner sets `CLAUDE_CODE_DISABLE_FAST_MODE=1`, `CLAUDE_CODE_RETRY_WATCHDOG=1`, and a bounded `MAX_MCP_OUTPUT_TOKENS` value. Credential and CLI entrypoint variables are stripped from the child environment.

## Loopback MCP host

Each tool-enabled session has an in-process MCP host on `127.0.0.1` with a random bearer token. The host is exposed to the CLI through the loopback HTTP MCP endpoint and streamable HTTP/SSE handling. Requests without the token are rejected. Tool names are published with the CLI's `mcp__steiner__` prefix and are shortened deterministically when the 64-character limit requires it.

A `tools/call` request is tied to the CLI's `claudecode/toolUseId`. Steiner registers the pending call when the stream announces the tool use and resolves the same opaque call handle when the tool result is ready. The handler keeps the response open with progress heartbeats every 20 seconds while Steiner runs the tool. Cancellation, duplicate IDs, host shutdown, and stale IDs cannot deliver a result to another call.

## Control messages and call boundary

The four conversational control messages are:

- `set_model`, sent when the requested model differs from the live session model.
- `apply_flag_settings`, sent to change the live effort level.
- `get_usage`, sent before a new user turn for the strict extra-usage gate.
- `interrupt`, sent when pending tool work is stale or a call is cancelled.

`initialize` is discovery-only. Model enumeration starts a short-lived CLI, sends `initialize`, parses its model list, and force-closes that process. A conversational session does not use `initialize` as a per-turn control message.

The provider decodes stream-json partial messages, registers tool-use IDs, and emits text and thinking deltas. It waits for the assistant-message boundary before treating a tool call as the completed assistant message. The assistant echo is used to confirm streamed tool calls or recover a fallback call, and the terminal result closes the turn. This boundary prevents duplicate text and ensures the assembled tool-call message is emitted once.

Usage is checked before a new user message is written. The gate passes only when `rate_limits_available` is true and `rate_limits.extra_usage.is_enabled` is explicitly false. Every other shape fails closed. Later rate-limit events also fail closed when they indicate overage or cannot be classified.

## Transcript synchronization

The sync record is append-only. User and tool entries are compared by role and content digest. Assistant entries are compared by tool-call IDs because the CLI may normalize assistant text and reasoning. Images are excluded from the content digest, and sent image fingerprints are tracked separately. This permits the one tolerated retroactive edit: Steiner may remove image data after consumption. Other rewrites, omissions, or reordering return the history-changed error and reference #895.

After an interrupt, only the assistant entry produced by that interrupted call has the narrow cleanup tolerance. A follow-up user message makes that assistant entry immutable. Advisor mode sends the parent snapshot as labelled transcript text, then the advisor question, and records the parent snapshot so later advisor calls append only the new parent delta.

## Model enumeration

Discovery has a 20-second outer budget, with work capped at 12 seconds and an 8-second cleanup reserve. It uses the signed-in CLI's `initialize` response, skips the `default` alias, keeps the first entry for each resolved model ID, and preserves display names, descriptions, and supported effort levels. Discovery is fail-soft: an error or empty usable result returns the static fallback catalog.

The manual verification checklist is intentionally Haiku-only. Run it with one full Haiku model ID from the signed-in CLI and confirm: CLI version and auth validation, model discovery, one text turn, one tool call with heartbeat behavior, usage-gate refusal with extra usage enabled, live model and effort switching, interrupt cleanup, append-only history refusal, and process and temporary-directory teardown. Do not use Opus, Sonnet, or other models for this checklist.

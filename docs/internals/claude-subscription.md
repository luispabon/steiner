# Claude subscription: Internals

The `claude_subscription` provider is a stateful adapter around the user's signed-in `claude` CLI. It is unofficial and depends on CLI behavior that Anthropic may change. This page records the implementation that is currently tested, not a stable Claude protocol contract.

## Process pool and session identity

`ClaudeSubscriptionPool` owns CLI processes. A normal session is pooled by `TransportSession` alone, with the parent transport session and `default` used only when that value is absent. The parent run forwards the current interactive session ID as `TransportSession`, so `/clear` routes the new conversation to a fresh pool key and CLI process. Model ID and reasoning effort are not pool-key components. The pool starts a process lazily, serializes calls for one key, and reuses the process until teardown.

Only parent normal calls persist (`claudeSubPersists`). Advisor, sub-agent, and compaction summary calls are ephemeral and store no backend state. A parent turn returns an opaque `claudeSubBackendState` for its generation. It holds the CLI session ID, transport session, sync record counts and digests, final message UUID, and a prompt/tools fingerprint, never message text. A process that exits is evicted, and its successor reseeds. A resumed process that produces no output is retired and the request reseeded once, except after a decoder stream error. A decoder stream error retires the process in normal and advisor turns without an inline reseed, so the next request starts a fresh process: the parent resumes at its last stored checkpoint and a sub-agent reseeds from the request messages.

The provider changes model live with the CLI's `set_model` control request. It changes effort with `apply_flag_settings` and `settings.effortLevel`. These changes update the existing session's state rather than creating a second process. Advisor mode uses a separate `|advisor` session key derived from the parent key. Advisor processes are tool-less and have their own append-only advisor transcript.

For test-only fixture capture, set `STEINER_CLAUDESUB_RECORD` to a directory. Recording is opt-in and disabled by default. When enabled, startup creates the directory if needed, normalizes its permissions to `0700`, and appends each raw, nonempty stdout line to a safely encoded per-session JSONL file, normalizing that existing or new file's permissions to `0600`; a recorder setup failure aborts startup rather than starting an unrecorded session. The encoded filename is not the literal session key, so hostile keys cannot escape the requested directory.

Recordings can contain prompts, tool inputs and outputs, file contents, account details, and other sensitive model data. Treat them as sensitive test artifacts. Before committing fixtures, copy only representative lines into `internal/provider/testdata/claudesub/`, remove prompts and tool or account data that are not needed for the test, redact emails, organization IDs, paths, and file contents, and review the diff. Do not use recordings from a real project as fixtures without this redaction step.

The pool locates `claude` once on `PATH`, checks `claude --version`, and checks `claude auth status`. It requires CLI version `2.1.294` or later, `authMethod: claude.ai`, and `apiProvider: firstParty`. It does not read a credential file. Process teardown, temporary-directory cleanup, advisor reaping, and shutdown deadlines are owned by the pool. Inactive advisor sessions are reaped after 60 minutes without a call. Non-advisor sessions are not reaped by this policy and stay open until the pool closes them.

## CLI invocation

Normal sessions use the stream-json process shape:

```text
claude -p --input-format stream-json --output-format stream-json --verbose --include-partial-messages --model <model> --tools "" --strict-mcp-config --setting-sources= --disable-slash-commands --settings {"autoCompactEnabled":false,"fastMode":false} --permission-mode dontAsk
```

When tools exist, the process also receives a private MCP config and an allowlist for `mcp__steiner__*`. Ephemeral processes (advisor, sub-agent, summary helper, and discovery) add `--no-session-persistence`. Parent processes omit it. A process that restores stored state adds `--resume <session-id>` and `--resume-session-at <final-uuid>`. A fork also adds `--fork-session`. Summary helpers add `--max-turns 1`.

A private system prompt is passed with `--system-prompt-file`. Reasoning `none` maps to `--thinking disabled`; other known effort values use adaptive thinking with summarized display and `--effort`. Steiner sets `CLAUDE_CODE_DISABLE_FAST_MODE=1`, `CLAUDE_CODE_RETRY_WATCHDOG=1`, and a bounded `MAX_MCP_OUTPUT_TOKENS` value. It also forces `CLAUDE_CODE_DISABLE_AUTO_MEMORY=1`, `CLAUDE_CODE_DISABLE_CLAUDE_MDS=1`, `CLAUDE_CODE_SILENT_TURN_REMINDER=0`, `CLAUDE_CODE_TOTAL_TOKENS_REMINDER=off`, `CLAUDE_CODE_TODO_REMINDER_MODE=off`, and `CLAUDE_CODE_DISABLE_GIT_INSTRUCTIONS=1`. These override inherited values. Credential and CLI entrypoint variables are stripped from the child environment. The discovery argv also includes `--disable-slash-commands`.

### Context injections

Steiner turns off these CLI context sources, by environment override or argv flag:

- Auto-memory `MEMORY.md`: `CLAUDE_CODE_DISABLE_AUTO_MEMORY=1`.
- `CLAUDE.md`, `CLAUDE.local.md`, and `~/.claude/CLAUDE.md` discovery: `CLAUDE_CODE_DISABLE_CLAUDE_MDS=1`. `--setting-sources=` does not cover these files.
- Skills listing: `--disable-slash-commands`.
- "The user hasn't heard from you" nudges: `CLAUDE_CODE_SILENT_TURN_REMINDER=0`.
- Token-count reminders: `CLAUDE_CODE_TOTAL_TOKENS_REMINDER=off`.
- Todo reminders: `CLAUDE_CODE_TODO_REMINDER_MODE=off`.
- Git instructions and status prefetch: `CLAUDE_CODE_DISABLE_GIT_INSTRUCTIONS=1`.

These switches were verified against CLI 2.1.296.

The CLI still injects the following. No compatible switch exists:

- Account email. A system reminder ("As you answer the user's questions, you can use the following context:") carries the email from `oauthAccount` in the CLI global config (`~/.claude.json`). Only bare or simple mode and `ANTHROPIC_UNIX_SOCKET` suppress it, and both disable stored subscription OAuth. `CLAUDE_CODE_USER_EMAIL` is a fallback only and cannot override the stored value. The stream-json `initialize` request has no field for it.
- SDK identity prefix. "You are a Claude agent, built on Anthropic's Claude Agent SDK." precedes the custom system prompt. It is fixed in `-p` mode. `--append-system-prompt` only swaps it for another SDK identity string.
- `# Environment` block: working directory, git-repo flag, platform, shell, OS version, a rule about treating downloaded files and archives as untrusted, and model name, ID, and knowledge cutoff. `--exclude-dynamic-system-prompt-sections` only relocates this block and is ignored with a custom system prompt. A sub-agent in a worktree sees its own working directory (`<cwd>`).
- "Today's date is <date>." line. It is always built and duplicates Steiner's `Current date:` line. A long session can also get a date-change reminder.
- Account, project, or git context can be re-announced mid-session if it changes.

Rejected options:

- `--bare` or `CLAUDE_CODE_SIMPLE`: refuses subscription OAuth. Auth works only with `ANTHROPIC_API_KEY` or `apiKeyHelper`.
- `--safe-mode`: also disables MCP servers, which the tool bridge needs.
- `ANTHROPIC_UNIX_SOCKET`: requires an explicit OAuth token.

The account email reaches the model on every `claude_subscription` request. Steiner cannot prevent this. See [Context the CLI adds](../user/claude-subscription.md#context-the-cli-adds).

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

The provider decodes stream-json partial messages, registers tool-use IDs, and emits text and thinking deltas. It waits for the assistant-message boundary before treating a tool call as the completed assistant message. The assistant echo is used to confirm streamed tool calls or recover a fallback call, and the terminal result closes the turn. This boundary prevents duplicate text and ensures the assembled tool-call message is emitted once. When the streamed tool input does not parse, the CLI echo carries `__unparsedToolInput` with `raw` and `len`. The CLI caps `raw` at the first 2048 UTF-16 code units of the input, dropping a high surrogate the cut would split, and sets `len` to the full input length in UTF-16 code units. The decoder accepts the sentinel only when `raw` equals the accumulated streamed input or that same CLI-style prefix of it. `len` must be a non-negative integer and is not otherwise compared. The emitted sentinel carries the full accumulated input with its UTF-8 byte length recomputed.

Usage is checked before a new user message is written. The gate passes only when `rate_limits_available` is true and `rate_limits.extra_usage.is_enabled` is explicitly false. An unconfirmable state (undecodable, `rate_limits_available` false or missing, or `rate_limits`, `extra_usage`, or `is_enabled` missing or null) is re-checked with backoff (500ms, 1s, then 2s) for about 15s, so a fresh session whose metadata is still loading can pass. Each `get_usage` request is still bounded by the 30s CLI control timeout. If the state is still unconfirmed, the turn fails closed and the refusal names the unconfirmable state in parentheses. Enabled credits refuse at once without polling, and a `get_usage` transport error returns at once. Later rate-limit events also fail closed when they indicate overage or cannot be classified.

## Transcript synchronization

The sync record is append-only. User and tool entries are compared by role and content digest. Assistant entries are compared by tool-call IDs because the CLI may normalize assistant text and reasoning. Images are excluded from the content digest, and sent image fingerprints are tracked separately. This permits the one tolerated retroactive edit: Steiner may remove image data after consumption. Other rewrites, omissions, or reordering make the live process unusable for that request. The process is retired, and the request forks or reseeds (see Session recovery and compaction). Advisor requests retry once on a fresh process.

After an interrupt, only the assistant entry produced by that interrupted call has the narrow cleanup tolerance. A follow-up user message makes that assistant entry immutable. Advisor mode sends the parent snapshot as labelled transcript text, then the advisor question, and records the parent snapshot so later advisor calls append only the new parent delta.

## Session recovery and compaction

A new parent process restores stored state only when the stored CLI session ID, prompt/tools fingerprint, and final message UUID match the request's history (`claudeSubRestoreFor`). The restore target always carries the stored final UUID as `--resume-session-at`. An ordinary restore is `--resume <session-id> --resume-session-at <final-uuid>` with no fork flag, so the process resumes at the Steiner record even if the CLI session has advanced independently. When the transport session matches the stored owner, the process resumes. When it differs, as after a Steiner Fork, the process forks at that UUID with `--fork-session`. Any other failed check, including a prompt/tools fingerprint mismatch in the stored state, reseeds.

A compatible live parent process is different. Restore compares the stored fingerprint, but a primed live process that accepts the request's history is reused as is and keeps the prompt and tools it started with (D5). A changed system prompt or tool set does not reseed a live process. Only restoring stored state compares the fingerprint.

The prompt/tools fingerprint includes the launch profile (the disabled context sources in CLI invocation). CLI sessions stored before that change fail the fingerprint check once and reseed instead of resuming. Their transcripts may contain injected memory or account context. The reseed costs one uncached turn per resumed session.

A reseed (`claudeSubReseedDelta`) sends the whole non-system history as one user transcript message. In-memory images are included, earlier tool calls are rendered as text, and image data stripped after consumption cannot return. The rendered entries become the new sync record. A reseeded turn does not reuse prompt cache.

The compaction trigger for this provider is `statefulUsageAboveThreshold` in `internal/agent/stateful_compaction_guard.go`. It compares the last response's `PromptTokens` with 70% of the context window. For `claude_subscription`, `PromptTokens` is the CLI's total input, summed by `claudeSubUsage.toUsageStats` from input, cache creation, and cache read tokens of the terminal assistant message. The CLI result's usage covers the whole query, including tool round-trips, so it is not used for context occupancy. A request that does not fit also triggers compaction. `errStatefulCompaction` applies only to stateful providers without `SupportsStatefulCompaction`, which this provider implements.

Summary routing in `claudeSubSummaryTurn`:

- Sub-agent (`ParentTransportSession` set): marked in-place summary on the sub-agent's own process. The process is tainted and retired. A process that cannot take the request is replaced, and the marked request is reseeded on it.
- Parent fork: when the stored state matches the live checkpoint (`claudeSubStateMatches`, which ignores the fingerprint) and its prompt/tools fingerprint equals the request's, and the source ends at an assistant UUID, an ephemeral helper forks the stored CLI session at the source-tail UUID, which is the final assistant uuid of the source messages and is not necessarily the record tip. The helper runs with `--resume <session-id>`, `--resume-session-at <source-tail-uuid>`, `--fork-session`, `--no-session-persistence`, and `--max-turns 1`. It returns no backend state, and the live parent is left untouched.
- Parent in place: when the fork is not possible or fails before any output, and a live process exists, the marked summary is appended to the live transcript. The process is tainted and retired, and the final chunk clears the generation's backend state.
- Parent cold reseed: when no live process exists, the request's messages are reseeded unmarked on an ephemeral helper with no persistence and `MaxTurns` 1. The request already ends in the compaction instruction, so no marker is added. A stored state that is not live is not forked for the summary.

## Provider diagnostics

`ClaudeSubscriptionProvider` writes one provider-stream record per turn through the provider stream-error logger, whatever the outcome (`claudeSubStream` in `claudesub_stream.go`). The record has `transport` `cli` and `attempts` `1`. `call_kind` is `advisor`, `summary`, `sub_agent`, or `normal` (`claudeSubCallKind`). Advisor takes precedence over summary, and summary over sub-agent. The record carries the outcome, duration, chunk count, TTFT, and a bounded error. The error is the diagnostic form of the failure: CLI terminal result text, the stderr tail, and control-response error text are never recorded, though the caller's chunk error keeps them. `partial_stream` is set when chunks reached the caller and the turn then failed. The CLI transport has no HTTP exchange, so the record never has `request_url`, request or response headers, or bodies, even with `capture_bodies` enabled.

When a provider stream-error logger is configured, each model call writes exactly one record, including a closed-pool refusal before the CLI starts and a usage-gate refusal before the CLI accepts the user request. Those record `exhausted` with `attempts` `1`. A nil or uninitialized provider returns before streaming and writes nothing. A resumed process that fails before output is reseeded inside the same call. That internal reseed is not reflected in `attempts`, which stays `1`.

A decode error from the CLI stream (`errClaudeSubDecodeStream`) is classed `decode_error` in `error_class`, like other decode failures. The classifier is diagnostics only: retry and remediation decisions do not read it.

A `message_start` that follows `message_stop` is still a fail-closed `errClaudeSubDecodeStream` (`decode_error`), but its caller-visible text explains that the Claude CLI started another response within the turn, possibly after a safety interruption, and that the affected response was stopped, not the run. The process is still retired and the turn is never retried inline.

## Model enumeration

Discovery has a 20-second outer budget, with work capped at 12 seconds and an 8-second cleanup reserve. It uses the signed-in CLI's `initialize` response, skips the `default` alias, keeps the first entry for each resolved model ID, and preserves display names, descriptions, and supported effort levels. Discovery is fail-soft: an error or empty usable result returns the static fallback catalog.

The manual verification checklist is intentionally Haiku-only. Run it with one full Haiku model ID from the signed-in CLI and confirm: CLI version and auth validation, model discovery, one text turn, one tool call with heartbeat behavior, usage-gate refusal with extra usage enabled, live model and effort switching, interrupt cleanup, and process and temporary-directory teardown. Do not use Opus, Sonnet, or other models for this checklist. The checklist does not yet cover resume, fork, reseed, or compaction against the live CLI.

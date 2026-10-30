# Claude subscription internals

User guide: [Claude subscription](../user/claude-subscription.md).

The `claude_subscription` provider drives the signed-in `claude` CLI as a stateful adapter. The CLI is not a stable protocol contract, so this page describes the implementation the tests cover.

## Process pool and session identity

`ClaudeSubscriptionPool` owns CLI processes. Parent normal calls are pooled by `TransportSession`, falling back to the parent transport session and then `default`. Model and effort are not part of the key. Calls for one key are serialized, and a process is reused until teardown. A process that exits is evicted, and its successor reseeds.

Only parent normal calls persist (`claudeSubPersists`). Advisor calls use the same key with the `|advisor` suffix (`claudeSubAdvisorKeySuffix`). Advisor, sub-agent, and summary calls store no backend state. A parent turn returns an opaque backend state holding the CLI session ID, transport session, sync record counts and digests, final message UUID, and a prompt/tools fingerprint. It holds no message text.

A model or effort change reuses the live process: `set_model` for the model and `apply_flag_settings` for `effortLevel`.

The pool runs `claude --version` and `claude auth status`. The status must report `authMethod` `claude.ai` and `apiProvider` `firstParty`. The provider never reads a credential file. Advisor sessions idle for 60 minutes are reaped (`claudeSubDefaultIdleTTL`). Other sessions stay open until the pool closes them. Session close is bounded by 10 seconds (`claudeSubSessionCloseTimeout`), and pool shutdown by 15 seconds.

`STEINER_CLAUDESUB_RECORD` names a directory for raw stdout capture. The directory is created with mode `0700`, and each per-session JSONL file is written with mode `0600` under an encoded name, so session keys cannot escape the directory. Recordings contain prompts, tool data, file contents, and account details. Before committing a fixture to `internal/provider/testdata/claudesub/`, keep only the representative lines and redact emails, organization IDs, paths, and file contents.

## CLI invocation

Normal sessions use this process shape:

```text
claude -p --input-format stream-json --output-format stream-json --verbose --include-partial-messages --model <model> --tools "" --strict-mcp-config --setting-sources= --disable-slash-commands --settings {"autoCompactEnabled":false,"fastMode":false} --permission-mode dontAsk
```

Additional flags:

- `--system-prompt-file` carries the private system prompt.
- With tools, a private MCP config and an allowlist for `mcp__steiner__*` are added.
- Calls that do not persist add `--no-session-persistence`.
- Restoring stored state adds `--resume <session-id>` and, when a target UUID is set, `--resume-session-at <uuid>`. Forks add `--fork-session`.
- Summary helpers set `--max-turns 1`.
- Effort `none` maps to `--thinking disabled`. A recognized effort (`low`, `medium`, `high`, `xhigh`, `max`) enables adaptive thinking and passes `--effort`. Empty or unknown effort is adaptive without `--effort` (`claudeSubEffort`).

The child environment strips `ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN`, `CLAUDE_CODE_OAUTH_TOKEN`, and `CLAUDE_CODE_ENTRYPOINT`. These values override inherited ones: `CLAUDE_CODE_DISABLE_FAST_MODE=1`, `CLAUDE_CODE_RETRY_WATCHDOG=1`, `CLAUDE_CODE_DISABLE_AUTO_MEMORY=1`, `CLAUDE_CODE_DISABLE_CLAUDE_MDS=1`, `CLAUDE_CODE_SILENT_TURN_REMINDER=0`, `CLAUDE_CODE_TOTAL_TOKENS_REMINDER=off`, `CLAUDE_CODE_TODO_REMINDER_MODE=off`, and `CLAUDE_CODE_DISABLE_GIT_INSTRUCTIONS=1`. `MAX_MCP_OUTPUT_TOKENS` is set to a bounded value.

The CLI still injects account and environment context that these switches do not remove. See [Context the CLI adds](../user/claude-subscription.md#context-the-cli-adds).

## Loopback MCP host

Each tool-enabled session starts an MCP host on `127.0.0.1` with an OS-chosen port and a bearer token. Requests without the token are rejected. Tools are published as `mcp__steiner__<name>`, and names over the 64-character limit (`claudeSubToolNameMax`) are shortened with a hash suffix.

The stream registers each tool use ID. The `tools/call` whose `_meta["claudecode/toolUseId"]` matches receives the result. While the tool runs, the handler sends progress notifications every 20 seconds (`claudeSubHeartbeat`).

## Control messages and stream decoding

Conversation uses four control requests: `set_model` when the requested model differs from the live model, `apply_flag_settings` to change effort, `get_usage` before a new user message for the usage gate, and `interrupt` when pending tool work is stale or a call is cancelled. `initialize` is used only by model discovery.

The decoder waits for the assistant-message boundary before treating a tool call as complete, which prevents duplicate text. A tool input that does not parse is recorded on that call as `argument_decode_error`, and the turn continues. The CLI's `__unparsedToolInput` sentinel (`UnparsedToolInputKey`) carries the raw text of an input the CLI could not parse. The decoder checks it against the streamed input.

## Transcript synchronization

The sync record is append-only. User and tool messages are compared by content digest (`claudeSubDigest`). Images are excluded from the digest because steiner strips image data after use, and image fingerprints are compared separately. Assistant entries are matched by tool-call IDs, since the CLI may normalize their text. Any other rewrite, omission, or reorder makes the live process unusable for that request: the process is retired, and the request forks or reseeds.

## Session recovery

A new parent process restores stored state only when the stored CLI session ID, prompt/tools fingerprint, and final message UUID match the request history (`claudeSubRestoreFor`). A restore resumes at the stored UUID with `--resume-session-at` when one is set. State owned by another transport session forks instead: `claudeSubRestoreFor` returns Fork when `stored.TransportSession` differs from the key, and the CLI gets `--fork-session`. Any failed check reseeds. A live process that accepts the history is reused as is, even if the prompt or tool set has changed. When a live process rejects the request, the retry reseeds unless the stored state belongs to another transport session (`claudeSubAttachSession`).

A reseed sends the whole non-system history as one user transcript message. Images still in memory are included, and stripped image data does not return. Tool calls in the history are rendered as text.

A generation whose backend state was cleared has nothing to restore, so its next request starts fresh and reseeds from the preserved conversation. Backend state is saved with the session, so a crash between retiring a process and saving the session leaves the previously persisted state on disk; the next request validates that state like any stored state.

## Compaction

`statefulUsageReachesSelectedThreshold` in `stateful_compaction_guard.go` compares the last response's `PromptTokens` with `ModelTokenBudget.CompactionThresholdTokens` when it is set. Otherwise `statefulUsageAboveThreshold` compares against `CompactionThreshold`, a fraction that `deriveEffectiveLimits` sets to 0.70. `compactionUsageThreshold` (also 0.70) is the fallback when that is zero. For this provider, `PromptTokens` is the CLI's total input from the terminal assistant message (`claudeSubUsage.toUsageStats` sums input, cache creation, and cache read tokens). The CLI's result usage covers the whole query, including tool round-trips, so it is not used. A request that does not fit also triggers compaction, as it does for every provider.

Summary routing is owned by `claudeSubSummaryTurn` (`claudesub_turn_summary.go`), tried in this order:

- Sub-agent (`ParentTransportSession` set): the summary is marked in place on the sub-agent's own process, which is then retired.
- Parent, tried first (`claudeSubTryParentFork`): when the stored state matches the live checkpoint and fingerprint, an ephemeral helper forks the stored CLI session at the source tail UUID (`--fork-session`, `--max-turns 1`) under `key + claudeSubSummarySuffix`. The live parent is untouched.
- Parent, when forking is not possible or fails before output (`claudeSubParentInPlace`): the summary is appended in place to the live transcript, and the process is retired.
- Parent with no live process, or whose record cannot take the summary: the request is reseeded on an ephemeral helper with no persistence.

When a live parent exists, an in-place parent summary retires it whatever the summary's outcome. `claudeSubDeliverTainted` taints the record before the first write, and `claudeSubParentInPlace` retires and releases the process afterwards, so it is never reused. The terminal chunk of a successful in-place summary carries `ClearBackendState` (`claudeSubClearOnDone`), including a summary finished by a cold reseed. A failed or cancelled in-place summary delivers no terminal chunk, so `claudeSubParentInPlace` marks its error with `markBackendStateRetired` and sets the request signal created by `provider.WithBackendStateRetirement`; `provider.BackendStateRetired` and `provider.BackendStateRetirementSignaled` report the retirement. An acquire failure, and a cold reseed with no live parent, touch no parent state and stay unmarked. A fork helper runs under `key + claudeSubSummarySuffix` and leaves the parent process and its state untouched, so it clears nothing.

## Usage gate

Before a new user message is written, `get_usage` is polled. The refusal rules are in the [extra usage safety gate](../user/claude-subscription.md#extra-usage-safety-gate). Unconfirmed states, including missing fields, are retried with 500ms, 1s, then 2s backoff for up to 15 seconds (`claudeSubUsageGateBudget`). Each `get_usage` is bounded by the 30 second control timeout.

If the live process still cannot confirm, `confirmUsageFresh` asks a new CLI process once, because the CLI caches an incomplete usage read in the running process. Confirmation is serialized pool-wide and bounded to 8 seconds (`claudeSubUsageProbeTimeout`).

## Provider diagnostics

When a stream-error logger is configured, each model call writes one provider-stream record (`claudeSubStream`) with transport `cli` and `attempts` 1. `call_kind` is `advisor`, `summary`, `sub_agent`, or `normal` (`claudeSubCallKind`), in that precedence. The record carries outcome, duration, chunk count, time to first token, and a bounded error. CLI result text, the stderr tail, and control-response text are not recorded. `partial_stream` is set when chunks reached the caller before the failure. Records have no request URL, headers, or bodies, because the CLI has no HTTP exchange. A CLI decode failure (`errClaudeSubDecodeStream`) is classified `decode_error`.

## Model discovery

Discovery runs a short-lived CLI under a 20 second outer budget (`claudeSubDiscoveryTimeout`): 12 seconds for work and 8 seconds reserved for cleanup. It reads the `initialize` response, skips the `default` alias, and keeps the first entry for each resolved model ID. Failure or an empty usable result returns the static fallback catalog.

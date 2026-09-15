# Execution modes: Internals

User-facing documentation: [Execution modes](../user/execution-modes.md).

## Prompt and cache mechanics

Execution mode does not alter the system preamble, tool schemas, or other static prompt-prefix content. The `Execution modes` preamble section describes both modes and is static. A per-turn `prompt.ModeNotice` is prepended to each outgoing user message and stored verbatim in the conversation. It is not stripped on later turns: the provider prompt-cache breakpoint is on the second-to-last user message, so removing an already-stored notice would rewrite the cached prefix on every subsequent turn.

Mode switching therefore changes dynamic conversation content only. The mode notice is also what makes the active mode visible after a restored session or compaction without changing the static prefix.

## Enforcement call chain

The write restriction for `mutate` and other path-writing tools, and the denial of `code` delegation in plan mode, are enforced in `internal/tool` and `internal/delegation` regardless of sandbox availability. Filesystem read-only behavior for `bash` and config-defined subprocess tools depends on the sandbox.

The executor resolves the sandbox decision once per tool call in `internal/tool.Executor.runPipeline`. Both the `bash` and subprocess dispatch paths consume that same `readOnlyProject` decision. An earlier implementation computed the paths independently, which let a config-defined subprocess tool retain a writable project mount in plan mode while `bash` was read-only. The shared resolution now keeps those paths aligned.

The composition root threads the parent's live execution-mode getter into every child executor as `ModeGetter`. A child executor therefore sees plan mode when its parent is in plan mode: its `mutate` calls are restricted to `.steiner/plans/`, and its `bash` and subprocess calls receive the read-only project mount. This is inherited runtime state, not a per-agent-type policy. The existing `readOnlyBash` flag for `explore` children remains a separate allowlist-specific restriction.

## Child prompt composition

Child preambles suppress the execution-modes section. Code children retain the shared editing, verification, and final-response workflow sections; non-code children receive only delegated-task authorization. Oneshot phases use the delegated-child workflow mode while still acting as orchestrators, so their prompts retain delegation instructions and can dispatch code children. Ordinary delegated children do not set `DelegationEnabled` and do not receive the delegation canon.

## Persistence and restoration

The current mode is stored in `session.Mode` and restored with the session. A saved session with no mode falls back to `modes.default`. Restore accepts only `plan` and `build`; any other non-empty value is rejected with a `load session failed` error, preventing an unknown value from restoring a writable session. Both modes retain the mode notice in outgoing messages, so the mode survives resume and compaction. The next turn re-announces the restored mode.

The full user-visible enforcement matrix, sandbox limitations, switching commands, and configuration are in [Execution modes](../user/execution-modes.md).
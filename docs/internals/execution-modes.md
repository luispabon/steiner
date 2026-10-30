# Execution modes internals

User guide: [Execution modes](../user/execution-modes.md).

## Prompt and cache mechanics

Mode is conveyed to the model with `prompt.ModeNotice` (`internal/prompt/mode_notice.go`). Interactive sessions keep the notice sticky (`modeNotice`, `internal/interactive/session.go`), so it stays under the provider's second-to-last user cache breakpoint (`internal/provider/anthropic_wire.go`).

Delegated child workflow modes omit the execution-modes section. `sectionExecutionModes` in `internal/prompt/system.go` returns nothing for them.

## Enforcement

- Sandbox resolution is documented in [Tool sandboxing internals](sandboxing.md#resolution).
- With no mode getter set, the mode defaults to build.
- Plan-mode path checks use a lexical allowlist and a symlink-resolved check (`internal/tool/policy_paths.go`).
- Plan-mode writable directories come from `config.PlanModeWritableDirs()`: `.steiner/plans` and `.steiner/security`.
- Mode changes call `mcpManager.UpdatePlanMode` (`cmd/steiner/interactive_session.go`). Delegated child executors take the parent's mode getter through `WithModeGetter` (`internal/delegation/bootstrap.go`).
- Plan-mode limits on delegation use `UsesWorktree` (`internal/delegation/agent_type.go`): `checkPlanModeWriteDenial` (`internal/delegation/specialized_tools.go`) denies `code` and `documentor` children, and `denyFollowUpInPlanMode` (`internal/delegation/follow_up.go`) denies `follow_up` when the child has `mutate`. Details are in [Sub-agent delegation internals](sub-agent-delegation.md).

## Persistence and restoration

`internal/interactive/persistence.go` saves `sess.Mode`. An empty saved mode falls back to `Config.Modes.Default`, which is build by default (`internal/config/load.go`). Restore accepts only `plan` and `build`. Any other value fails with `load session: unknown mode "<mode>"`, reported as `load session failed: %v`.

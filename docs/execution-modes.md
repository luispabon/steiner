# Execution modes

Interactive sessions run in one of two execution modes: `plan` or `build`. The mode controls whether the agent may write to the project and delegate mutating work, without changing the system prompt, tool definitions, or any other part of the cached prompt prefix.

## Mode semantics

- **`build`** — the default mode. Normal workspace editing: `mutate`, `bash`, and the `code` sub-agent tool are all available without restriction.
- **`plan`** — project edits are restricted: writes outside `.steiner/plans/` are denied, and plan artifacts may be written under `.steiner/plans/`. Plan mode doubles as a chat/Q&A mode: discuss freely, and write a plan file only when handing off to build mode.

There is no third "chat" mode and no auto-detection — plan mode itself serves that purpose. There is also no `--mode` CLI flag; execution modes apply to interactive sessions only. Oneshot and non-interactive `exec` runs are unaffected and keep their existing behaviour. Use the persistent `--profile <name>` flag to select model assignments at startup for normal interactive, `--exec`, and oneshot runs; it is separate from execution-mode selection.

## Switching modes

- **Shift+Tab** toggles between `plan` and `build` in the TUI. The binding is checked after overlay key handling, so open overlays (pickers, modals) take priority over the toggle.
- **`/mode`** with no argument toggles the mode; `/mode plan` or `/mode build` sets it explicitly. An unrecognized argument reports `mode "<arg>" is not valid (use plan: restricted edits, plan artifacts only; or build: normal workspace editing)` and leaves the mode unchanged.
- **`workflow_handoff` acceptance** flips the session to `build` mode automatically. Three handoff targets exist, each with its own artifact and startup contract:
  - **Structured `implement` target**: requires `overview.md` + `plan.yaml` and starts the structured `/implement` workflow (`/implement <target>`) in the fresh session.
  - **Structured `review` target**: requires `overview.md` + `plan.yaml` and starts the structured `/review` workflow (`/review <target>`) in the fresh session.
  - **Loose `build` target**: requires only `plan.md` and directly executes that standalone `plan.md` in build mode. It submits a literal prompt (`Implement the plan at <target>/plan.md. It is the complete record of what was agreed — read it before making any changes.`) to the model without depending on skill discovery; this handoff path works even with skills disabled.
  
  There is no model-initiated path from build back to plan; only this workflow_handoff transition and the user-driven toggle/command/skill-invocation paths change mode.
- **Direct skill invocation** (`/<skillname> [args]`, typed or picked from the slash overlay) sets the mode to match the invoked skill before submitting: `/plan` switches to `plan` mode; every other skill (`/implement`, `/review`, `/simplify`, `/pull-request`, and any project/user-defined skill) switches to `build` mode. This keeps the mode notice and the invoked skill's own workflow rules from contradicting each other — e.g. invoking `/plan` while sitting in `build` mode no longer leaves the model to reconcile "Normal workspace editing" against the plan skill's "never implement" rule. The literal `/<skillname> [args]` text is submitted to the model (not stripped down to bare args), so the skill's own "invoked by name" trigger fires reliably instead of relying solely on the passive "Active Skills" framing block.

Switching mode emits a `status: mode → <mode>` transcript line and updates the footer badge (`⏸ plan` / `⏵⏵ build`) and the sidebar mode row. A bracketed mode notice (`prompt.ModeNotice`) is prepended to every outgoing user message in both modes and stored verbatim in the conversation, so the model can always see the current mode. A restored session re-announces its mode on the next turn automatically.

## Cache safety

Mode never changes the system preamble, tool schemas, or any other part of the cached prompt prefix — the "Execution modes" preamble section is static and describes both modes regardless of which is active. The per-turn mode notice is prepended to outgoing user messages and stored verbatim — it is never stripped, so that turn N+1's cached prefix through turn N remains byte-identical to what turn N sent. The provider's prompt-cache breakpoint lands on the second-to-last user message; stripping a message already behind that breakpoint would invalidate the cached prefix on every subsequent turn. Keeping the notice in place maintains cache integrity.

## Enforcement matrix

| Surface | `plan` mode, sandbox on | `plan` mode, sandbox off/unavailable | `build` mode |
|---|---|---|---|
| `mutate` (and other path-writing tools) | Denied outside `.steiner/plans/` at the tool-policy layer | Denied outside `.steiner/plans/` at the tool-policy layer (same as sandboxed) | Allowed |
| `bash` | Project bind-mounted read-only under bubblewrap, except `.steiner/plans/` and `.git` which stay writable | Unenforced — the command runs with no sandbox wrapping at all | Allowed, sandboxed as normal |
| Config-defined subprocess tools | Project bind-mounted read-only under bubblewrap, same as `bash` | Unenforced — no sandbox wrapping | Allowed, sandboxed as normal |
| MCP tools | Available; `allow` approval downgrades to `ask` (see [MCP servers](mcp.md)) | Available; `allow` approval downgrades to `ask` (same) | Available |
| `code` sub-agent tool | Denied | Denied | Allowed |
| `review` sub-agent tool | Allowed | Allowed | Allowed |
| `follow_up` targeting a `code`-derived child | Denied | Denied | Allowed |

The `mutate` write restriction and the sub-agent denials are enforced in `internal/tool` and `internal/delegation` regardless of sandbox state — they do not depend on bubblewrap being available. `bash`'s and config-defined subprocess tools' filesystem read-only enforcement depends on the sandbox: without a working sandbox (`sandbox.enabled: false`, a non-Linux platform, or `bwrap` missing from `PATH`), a plan-mode `bash` or subprocess-tool command can still write to `.steiner/plans/` (or anywhere else) via direct filesystem access. In that state plan mode is an agent/tool policy, not a filesystem-level guarantee: `mutate` and sub-agent denials still hold, but `bash` and subprocess tools run unenforced.

Under a working sandbox, plan mode's guarantee is "the working tree stays read-only," not "nothing under the project can be written." Alongside `.steiner/plans/`, `.git` is bound writable so a planning session can stage, commit, and create branches — a plan-mode `bash` command still cannot modify a tracked file directly (`echo x > file`, `git checkout -- .`, `git stash` all fail at the mount), but it can `git add`/`git commit`/`git branch` freely. That also makes `.git/hooks` and `.git/config` (including `core.hooksPath`) writable in plan mode, so treat plan-mode `bash` as trusted the same way build mode already is — it is not a sandbox against a malicious prompt. For a linked worktree, where `.git` is a pointer file, the resolved gitdir and its shared common dir are bound writable too (see `gitWritableBinds` in `internal/sandbox/mounts.go`); if the pointer can't be resolved, `.git` binding is skipped and git plumbing stays unavailable in that worktree rather than failing the whole sandboxed command.

`bash` and subprocess tools receive identical `readOnlyProject` treatment because the executor resolves the sandbox decision exactly once per tool call, in `internal/tool.Executor.runPipeline`, and both dispatch paths consume that same resolved decision (see [Tool sandboxing](tool-sandboxing.md#sandbox-wrapper-resolution)). Earlier, `bash` and subprocess tools computed their sandbox mode independently, which let a config-defined subprocess tool run with an unrestricted (writable) project mount in plan mode while `bash` was correctly read-only.

Child agents (see [Sub-agent delegation](sub-agent-delegation.md)) inherit the parent's live execution mode: the composition root threads a `ModeGetter` into every child executor the same way it threads the sandbox wrapper. A child's own `mutate` calls are restricted to `.steiner/plans/` and its `bash`/subprocess calls get the read-only project mount whenever the parent session is in plan mode, exactly as if the parent had run the command itself.

## Persistence

The current mode is saved with the session (`session.Mode`) and restored on resume. If a saved session has no mode recorded, the session falls back to `modes.default`. `plan` and `build` are accepted as persisted values; any other non-empty value is rejected at restore with a `load session failed` error, so an unknown mode can never restore a writable session. Both plan- and build-mode sessions carry the mode notice on every outgoing user message, so the mode is always visible in the conversation history and survives resume and compaction without special handling. A restored session re-announces its mode on the next turn automatically.

## Configuration

```yaml
modes:
  default: build
```

`modes.default` accepts `plan` or `build` and defaults to `build`. See [Configuration](configuration.md#modes-block) for the full field reference.

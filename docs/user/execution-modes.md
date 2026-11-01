# Execution modes

Interactive sessions run in one of two execution modes, `plan` or `build`. The mode controls whether the agent may write to the project and dispatch sub-agents that edit files.

Execution modes apply to interactive sessions only. There is no `--mode` flag, and `steiner oneshot` and `--exec` runs are not affected. `--profile` selects model assignments and is unrelated to the execution mode.

## Modes

- **`build`**: the default. Normal workspace editing. `mutate`, `bash`, and the `code` and `documentor` sub-agents are available. Writes still go through the usual path policy, sandbox, and approval checks; see [Sandboxing](sandboxing.md).
- **`plan`**: project edits are restricted. Writes are allowed only under `.steiner/plans/` (plan artifacts) and `.steiner/security/` (security audit reports). Use plan mode for planning, questions, and discussion.

## Switching modes

- **Shift+Tab** toggles between `plan` and `build`. Open overlays take the key first.
- **`/mode`** toggles the mode. `/mode plan` or `/mode build` sets it. Any other argument reports an error and leaves the mode unchanged.
- **Accepting a `workflow_handoff`** switches the session to `build`. The handoff clears the conversation, optionally switches model, then starts the next step: the `implement` and `review` targets run the matching skill on a plan folder containing `overview.md` and `plan.yaml`; the `build` target submits a prompt to implement the folder's `plan.md` directly.
- **Invoking a skill** with `/<skill>` switches to `plan` for `/plan` and to `build` for every other skill, including custom ones. `/security-audit` leaves the mode unchanged. See [Skills](skills.md).

The footer badge and sidebar show the current mode, and a status line confirms each switch. A restored session keeps its saved mode.

Set the startup mode with `modes.default` (`plan` or `build`, default `build`). See [Configuration](configuration.md#modes-block).

## Enforcement matrix

| Surface | `plan` mode | `build` mode |
|---|---|---|
| `mutate` and other path-writing tools | Denied outside `.steiner/plans/` and `.steiner/security/` | Allowed, subject to path policy and approvals |
| `bash` and config-defined subprocess tools | Project mounted read-only, except the plan-mode writable paths, when the sandbox is active; unrestricted without a sandbox | Allowed, sandboxed as normal |
| MCP tools | Available; a server with approval `allow` asks instead | Available |
| `code` and `documentor` sub-agents | Denied | Allowed |
| `follow_up` to a `code` or `documentor` sub-agent | Denied | Allowed |
| Other sub-agent types (`explore`, `review`, `evaluate`, and so on) | Allowed | Allowed |

The `mutate` and sub-agent restrictions apply with or without a sandbox. The `bash` restriction needs a working sandbox: without one, plan-mode `bash` and subprocess tools can write anywhere the user can, so plan mode is not a filesystem guarantee. Even with the sandbox, plan-mode `bash` can still run git commands that commit or create branches. Treat plan mode as a guard against accidental edits, not as protection from a hostile prompt. See [Sandboxing](sandboxing.md) for the mounts.

The `documentor` sub-agent is denied in plan mode because it has `mutate`, even though its writes are limited to documentation files.

# Execution modes

Interactive sessions run in one of two execution modes: `plan` or `build`. The mode controls whether the agent may write to the project and delegate mutating work.

For prompt-prefix, executor, child propagation, and session restoration mechanics, see [Execution modes internals](../internals/execution-modes.md).

## Mode semantics

- **`build`**: the default mode. Normal workspace editing: `mutate`, `bash`, and the `code` sub-agent tool are available without restriction.
- **`plan`**: project edits are restricted. Writes outside `.steiner/plans/` are denied, and plan artifacts may be written under `.steiner/plans/`. Plan mode also serves as chat and Q&A mode.

There is no third `chat` mode and no auto-detection. There is no `--mode` CLI flag; execution modes apply to interactive sessions only. Oneshot and non-interactive `exec` runs are unaffected. Use `--profile <name>` to select model assignments; it is separate from execution-mode selection.

## Switching modes

- **Shift+Tab** toggles between `plan` and `build` in the TUI. Open overlays take priority over the toggle.
- **`/mode`** with no argument toggles the mode. `/mode plan` or `/mode build` sets it explicitly. Invalid arguments report an error and leave the mode unchanged.
- **`workflow_handoff` acceptance** switches the session to `build` mode. Structured `implement` and `review` targets require `overview.md` and `plan.yaml` and start their named workflow. The loose `build` target requires `plan.md` and executes that plan directly, even when skills are disabled.
- **Direct skill invocation** switches to `plan` for `/plan` and to `build` for other skills, including `/implement`, `/review`, `/simplify`, and `/pull-request`.

Switching emits a status line and updates the footer badge and sidebar mode row. A restored session re-announces its mode on the next turn.

## Enforcement matrix

| Surface | `plan` mode, sandbox on | `plan` mode, sandbox off/unavailable | `build` mode |
|---|---|---|---|
| `mutate` and other path-writing tools | Denied outside `.steiner/plans/` | Denied outside `.steiner/plans/` | Allowed |
| `bash` | Project read-only under bubblewrap, except `.steiner/plans/` and `.git` | Unenforced | Allowed, sandboxed as normal |
| Config-defined subprocess tools | Project read-only under bubblewrap | Unenforced | Allowed, sandboxed as normal |
| MCP tools | Available; `allow` becomes `ask` | Available; `allow` becomes `ask` | Available |
| `code` sub-agent tool | Denied | Denied | Allowed |
| `review` sub-agent tool | Allowed | Allowed | Allowed |
| `follow_up` targeting a `code` child | Denied | Denied | Allowed |

The `mutate` and sub-agent restrictions do not depend on bubblewrap. Without a working sandbox, plan-mode `bash` and subprocess tools can write through direct filesystem access, so plan mode is not a filesystem guarantee in that state.

With a working sandbox, the project stays read-only except `.steiner/plans/` and `.git`. Plan-mode `bash` can stage, commit, and create branches, so treat it as trusted rather than as protection from malicious prompts.

## Configuration

```yaml
modes:
  default: build
```

`modes.default` accepts `plan` or `build` and defaults to `build`. See [Configuration](configuration.md#modes-block) for the full field reference.

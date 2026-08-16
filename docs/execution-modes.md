# Execution modes

Interactive sessions run in one of two execution modes: `plan` or `build`. The mode controls whether the agent may write to the project and delegate mutating work, without changing the system prompt, tool definitions, or any other part of the cached prompt prefix.

## Mode semantics

- **`build`** — the default mode. Normal workspace editing: `mutate`, `bash`, and the `code` sub-agent tool are all available without restriction.
- **`plan`** — project edits are restricted: writes outside `.steiner/plans/` are denied, and plan artifacts may be written under `.steiner/plans/`. Plan mode doubles as a chat/Q&A mode: discuss freely, and write a plan file only when handing off to build mode.

There is no third "chat" mode and no auto-detection — plan mode itself serves that purpose. There is also no `--mode` CLI flag; execution modes apply to interactive sessions only. Oneshot and non-interactive `exec` runs are unaffected and keep their existing behaviour.

## Switching modes

- **Shift+Tab** toggles between `plan` and `build` in the TUI. The binding is checked after overlay key handling, so open overlays (pickers, modals) take priority over the toggle.
- **`/mode`** with no argument toggles the mode; `/mode plan` or `/mode build` sets it explicitly. An unrecognized argument reports `mode "<arg>" is not valid (use plan: restricted edits, plan artifacts only; or build: normal workspace editing)` and leaves the mode unchanged.
- **`workflow_handoff` acceptance** flips the session to `build` mode automatically. Three handoff targets exist, each with its own artifact and startup contract:
  - **Structured `implement` target**: requires `overview.md` + `plan.yaml` and starts the structured `/implement` workflow (`/implement <target>`) in the fresh session.
  - **Structured `review` target**: requires `overview.md` + `plan.yaml` and starts the structured `/review` workflow (`/review <target>`) in the fresh session.
  - **Loose `build` target**: requires only `plan.md` and directly executes that standalone `plan.md` in build mode. It submits a literal prompt (`Implement the plan at <target>/plan.md. It is the complete record of what was agreed — read it before making any changes.`) to the model without depending on skill discovery; this handoff path works even with skills disabled.
  
  There is no model-initiated path from build back to plan; only this workflow_handoff transition and the user-driven toggle/command change mode.

Switching mode emits a `status: mode → <mode>` transcript line and updates the footer badge (`⏸ plan` / `⏵⏵ build`) and the sidebar mode row. A bracketed mode notice (`prompt.ModeNotice`) is prepended to every outgoing user message in both modes and stored verbatim in the conversation, so the model can always see the current mode. A restored session re-announces its mode on the next turn automatically.

## Cache safety

Mode never changes the system preamble, tool schemas, or any other part of the cached prompt prefix — the "Execution modes" preamble section is static and describes both modes regardless of which is active. The per-turn mode notice is prepended to outgoing user messages and stored verbatim — it is never stripped, so that turn N+1's cached prefix through turn N remains byte-identical to what turn N sent. The provider's prompt-cache breakpoint lands on the second-to-last user message; stripping a message already behind that breakpoint would invalidate the cached prefix on every subsequent turn. Keeping the notice in place maintains cache integrity.

## Enforcement matrix

| Surface | `plan` mode, sandbox on | `plan` mode, sandbox off/unavailable | `build` mode |
|---|---|---|---|
| `mutate` (and other path-writing tools) | Denied outside `.steiner/plans/` at the tool-policy layer | Denied outside `.steiner/plans/` at the tool-policy layer (same as sandboxed) | Allowed |
| `bash` | Project bind-mounted read-only under bubblewrap, except `.steiner/plans/` which stays writable | Unenforced — the command runs with no sandbox wrapping at all | Allowed, sandboxed as normal |
| MCP tools | Available; `allow` approval downgrades to `ask` (see [MCP servers](mcp.md)) | Available; `allow` approval downgrades to `ask` (same) | Available |
| `code` sub-agent tool | Denied | Denied | Allowed |
| `review` sub-agent tool | Allowed | Allowed | Allowed |
| `follow_up` targeting a `code`-derived child | Denied | Denied | Allowed |

The `mutate` write restriction and the sub-agent denials are enforced in `internal/tool` and `internal/delegation` regardless of sandbox state — they do not depend on bubblewrap being available. Only `bash`'s filesystem read-only enforcement depends on the sandbox: without a working sandbox (`sandbox.enabled: false`, a non-Linux platform, or `bwrap` missing from `PATH`), a plan-mode `bash` command can still write to `.steiner/plans/` via direct filesystem access. In that state plan mode is an agent/tool policy, not a filesystem-level guarantee: `mutate` and sub-agent denials still hold, but `bash` runs unenforced.

## Persistence

The current mode is saved with the session (`session.Mode`) and restored on resume. If a saved session has no mode recorded, the session falls back to `modes.default`. `plan` and `build` are accepted as persisted values; any other non-empty value is rejected at restore with a `load session failed` error, so an unknown mode can never restore a writable session. Both plan- and build-mode sessions carry the mode notice on every outgoing user message, so the mode is always visible in the conversation history and survives resume and compaction without special handling. A restored session re-announces its mode on the next turn automatically.

## Configuration

```yaml
modes:
  default: build
```

`modes.default` accepts `plan` or `build` and defaults to `build`. See [Configuration](configuration.md#modes-block) for the full field reference.

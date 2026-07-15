# Execution modes

Interactive sessions run in one of two execution modes: `plan` or `build`. The mode controls whether the agent may write to the project and delegate mutating work, without changing the system prompt, tool definitions, or any other part of the cached prompt prefix.

## Mode semantics

- **`build`** — the default mode. Normal editing rules apply: `mutate`, `bash`, and the `code` sub-agent tool are all available without restriction.
- **`plan`** — the project is treated as read-only. Writes are permitted only under `.steiner/` (plans, scratch state). Plan mode doubles as a chat/Q&A mode: discuss freely, and only produce a plan artifact under `.steiner/plans/` when the user actually asks for one.

There is no third "chat" mode and no auto-detection — plan mode itself serves that purpose. There is also no `--mode` CLI flag; execution modes apply to interactive sessions only. Oneshot and non-interactive `exec` runs are unaffected and keep their existing behaviour.

## Switching modes

- **Shift+Tab** toggles between `plan` and `build` in the TUI. The binding is checked after overlay key handling, so open overlays (pickers, modals) take priority over the toggle.
- **`/mode`** with no argument toggles the mode; `/mode plan` or `/mode build` sets it explicitly. An unrecognized argument reports `mode "<arg>" is not valid (use plan or build)` and leaves the mode unchanged.
- **`workflow_handoff` acceptance** flips the session to `build` mode automatically — the intended path for moving from an approved plan into implementation. There is no model-initiated path from build back to plan; only this workflow_handoff transition and the user-driven toggle/command change mode.

Switching mode emits a `status: mode → <mode>` transcript line, updates the footer badge (`⏸ plan` / `⏵⏵ build`) and the sidebar mode row, and queues a bracketed mode notice (`prompt.ModeNotice`) that is prepended to the next outgoing user message — it is never stored as a separate conversation message, so it does not appear in the transcript or get replayed on the next turn once consumed.

## Cache safety

Mode never changes the system preamble, tool schemas, or any other part of the cached prompt prefix — the "Execution modes" preamble section is static and describes both modes regardless of which is active. The only mode-carrying content is the per-turn notice prepended to (and then stripped back out of) the outgoing user message, so switching modes does not invalidate the provider's prompt cache.

## Enforcement matrix

| Surface | `plan` mode, sandbox on | `plan` mode, sandbox off/unavailable | `build` mode |
|---|---|---|---|
| `mutate` (and other path-writing tools) | Denied outside `.steiner/` at the tool-policy layer | Denied outside `.steiner/` at the tool-policy layer (same as sandboxed) | Allowed |
| `bash` | Project bind-mounted read-only under bubblewrap, except `.steiner/` which stays writable | Unenforced — the command runs with no sandbox wrapping at all | Allowed, sandboxed as normal |
| `code` sub-agent tool | Denied | Denied | Allowed |
| `review` sub-agent tool | Allowed | Allowed | Allowed |
| `follow_up` targeting a `code`-derived child | Denied | Denied | Allowed |

The `mutate` write restriction and the sub-agent denials are enforced in `internal/tool` and `internal/delegation` regardless of sandbox state — they do not depend on bubblewrap being available. Only `bash`'s filesystem read-only enforcement depends on the sandbox: without a working sandbox (`sandbox.enabled: false`, a non-Linux platform, or `bwrap` missing from `PATH`), a plan-mode `bash` command can still write to the project.

## Persistence

The current mode is saved with the session (`session.Mode`) and restored on resume. If a saved session has no mode recorded, the session falls back to `modes.default`. Resuming into `plan` mode (whether restored or freshly defaulted) re-queues the mode notice so the model is reminded of the active mode on the first turn after resume. Manual compaction also re-queues the mode notice, since compaction discards the raw history the model would otherwise have used to remember the current mode.

## Configuration

```yaml
modes:
  default: build
```

`modes.default` accepts `plan` or `build` and defaults to `build`. See [Configuration](configuration.md#modes-block) for the full field reference.

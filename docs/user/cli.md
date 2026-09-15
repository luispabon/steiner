# CLI reference

This reference assumes `steiner` is installed. See [Installation](installation.md) for release binaries.

## Modes

Interactive mode launches the TUI:

```bash
steiner
```

Single-shot mode runs one request and exits:

```bash
steiner --exec "explain the auth package"
```

Select a raw provider/model reference for a one-shot request:

```bash
steiner --model ollama/qwen2.5-coder:14b --exec "explain the auth package"
```

The same selection can be set for the active orchestrator with `STEINER_MODEL`:

```bash
STEINER_MODEL=ollama/qwen2.5-coder:14b steiner --exec "explain the auth package"
```

## Commands

| Command | What it does |
|---------|--------------|
| `version` | Print build version; `--short` prints script-friendly output |
| `config` | Print resolved configuration |
| `update` / `upgrade` | Self-update to the latest release |
| `tools` | List configured tools and approval status |
| `skills` | List discovered skills |
| `models refresh` | Refresh model discovery caches |
| `models status` | Show model discovery cache status |
| `worktrees --list` | List delegation worktrees |
| `worktrees --prune <id>` | Remove one delegation worktree |
| `worktrees --prune-all` | Remove all delegation worktrees |
| `--help` | Print all flags and usage |

## Version output

`steiner --version` and `steiner version` print build metadata. Use `steiner version --short` to print only the raw version string for scripts and CI.

## Interactive commands

Use `/compact` or `/compact <focus text>` to compact the conversation. Use `/model` to change the active orchestrator model. For example, enter `/model ollama/qwen2.5-coder:14b` to select a raw reference. Use `/profile <name>` to select a profile for future role assignments. Use `/mode [plan|build]` to switch execution mode. See [Execution modes](execution-modes.md).

A configured alias is an optional alternate for a model with persistent customization, such as `--model careful` when `careful` is defined under `models.definitions` with reasoning or retry settings. Raw references are the normal path when no such customization is needed.

For the full configuration and flag reference, see [Configuration](configuration.md). For provider setup, see [Provider and model setup](provider-and-model-setup.md).

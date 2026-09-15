# CLI reference

## Modes

Interactive mode launches the TUI:

```bash
go run ./cmd/steiner
# or
./bin/steiner
```

Single-shot mode runs one request and exits:

```bash
go run ./cmd/steiner --exec "explain the auth package"
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

Use `/compact` or `/compact <focus text>` to compact the conversation. Use `/model` to change the active orchestrator model and `/profile <name>` to select a profile for future role assignments. Use `/mode [plan|build]` to switch execution mode. See [Execution modes](execution-modes.md).

For the full configuration and flag reference, see [Configuration](configuration.md).

# CLI reference

This page lists every `steiner` command and flag. Run `steiner --help` or `steiner <command> --help` for the same list from the binary. For installation, see [Installation](installation.md).

## Usage

```text
steiner [flags]               start the interactive TUI
steiner --exec [flags] [prompt]   run one request and exit
steiner <command> [flags]     run a subcommand
```

With no command and no `--exec`, Steiner starts the interactive TUI described in [Interactive mode](interactive.md). Extra arguments without `--exec` are rejected.

## Global flags

All flags except `--resume` are accepted by every subcommand, though most only affect commands that load configuration or run the agent.

| Flag | What it does |
|------|--------------|
| `--config <path>` | Use this file as the project config instead of `.steiner/config.yaml`. The user config is still loaded first. The file must exist. See [Configuration](configuration.md). |
| `--model <ref>` | Set the active orchestrator model for this run. Takes precedence over `STEINER_MODEL`. See [Provider and model setup](provider-and-model-setup.md). |
| `--profile <name>` | Select a model profile for this run. |
| `--exec` | Run a single request and exit. See [Single-request mode](#single-request-mode). |
| `--max-turns <n>` | Maximum agent turns in `--exec` mode. `0` (the default) uses `limits.max_turns` from the [`limits` block](configuration.md#limits-block). |
| `--enable-streaming` | Stream responses in `--exec` mode. Without it, Steiner waits for each complete response. |
| `--unsafe` | Disable the sandbox for tool execution. See [Sandboxing](sandboxing.md). |
| `--trust-project-config` | Trust this project's config for this run without prompting. Trust is not saved. |
| `--verbose` | Set the log level to `debug`. |
| `--log-file <path>` | Write full session logs to this file, plus related log files next to it. Overrides the [`logging` block](configuration.md#logging-block). The logs may contain prompts, file contents and tool output; treat them as sensitive. |
| `--compaction-log-file <path>` | Write compaction logs to this file. Overrides `logging.compaction_log_file`. |
| `--dev` | Select the dev release channel for `steiner update`. Has no other effect. |
| `--resume [id]` | Root command only. With an ID, start the TUI with that saved session. Without a value, print saved sessions (number, title, model, last update, ID) and exit. Cannot be combined with `--exec`. |
| `-h`, `--help` | Print help. |
| `-v`, `--version` | Print build information (same as `steiner version`). |

## Single-request mode

`--exec` runs one request and exits. Pass the prompt as arguments, or pipe it on standard input when no arguments are given:

```bash
steiner --exec "explain the auth package"
steiner --model ollama/qwen2.5-coder:14b --exec "explain the auth package"
git diff | steiner --exec
```

Events and the final reply are written to standard output.

When a tool call needs approval, Steiner reads one line from the terminal (`/dev/tty`) when one is available, otherwise from standard input. `y` or `yes` approves; anything else, or no input, denies. [Sandboxing](sandboxing.md) explains which calls need approval.

Project config trust is never prompted in `--exec` mode. If the project has an untrusted `.steiner/config.yaml`, pass `--trust-project-config` or set `STEINER_TRUST_PROJECT_CONFIG=1`; otherwise the command fails. The same applies to any command run without an interactive terminal. See [Configuration](configuration.md) for project trust.

## Commands

| Command | What it does |
|---------|--------------|
| `version [--short]` | Print version, commit, build date, Go version and release channel. `--short` prints only the version string, for scripts. |
| `config` | Print the resolved configuration as YAML. It includes an untrusted project config, marked with a `# project ... is NOT trusted` line, without trusting it. Configuration errors are reported, so this is also the way to validate config. |
| `tools` | List the names of the tools available to the agent. See [Tools](tools.md). |
| `skills` | List discovered skills. See [Skills](skills.md). |
| `model inspect <alias>` | Show how a model resolves: provider, backend model ID, transport, limits, compaction thresholds, reasoning support, metadata sources and warnings. |
| `model-metadata status` | Show the model metadata cache path, size, age, freshness and model count. |
| `model-metadata refresh` | Download fresh model metadata. |
| `model-metadata clear` | Delete the model metadata cache. |
| `models refresh` | Re-fetch the model lists of configured providers. |
| `models status` | Show whether each provider's model list cache is fresh, stale or missing. |
| `cache refresh` | Run `model-metadata refresh` and `models refresh` together. |
| `oneshot <task>` | Run an autonomous oneshot task headlessly. See [Oneshot](oneshot.md). |
| `oneshot --list` | List resumable oneshot runs. |
| `oneshot --resume <id>` | Resume a oneshot run. |
| `worktrees --list` | List worktrees created by `code` and `documentor` sub-agents, with ID, branch and path. See [Sub-agent delegation](sub-agent-delegation.md). |
| `worktrees --prune <id>` | Remove one worktree by the ID from `--list`. |
| `worktrees --prune-all` | Remove all of them. |
| `update [version] [--dev]` | Update the running binary. `upgrade` is an alias. See [Installation](installation.md#updating). |
| `login codex [--timeout <duration>] [--debug-url]` | Log in to OpenAI Codex in the browser and save the token. `--timeout` defaults to `2m`; `--debug-url` prints the authorisation URL. See [Provider and model setup](provider-and-model-setup.md). |
| `login codex status` | Show whether a Codex token is saved and when it expires. |
| `completion <shell>` | Print a shell completion script (`bash`, `zsh`, `fish` or `powershell`). |
| `help [command]` | Print help for a command. |

`worktrees` needs exactly one of `--list`, `--prune` or `--prune-all`. `oneshot` needs a task, `--list` or `--resume`, and `--list` and `--resume` cannot be combined with a task or with each other.

Commands that load configuration (`tools`, `skills`, `model inspect`, `models`, `cache refresh`, `oneshot`) check project trust. In an interactive terminal they show the trust prompt; otherwise they need `--trust-project-config` or `STEINER_TRUST_PROJECT_CONFIG=1`.

Interactive mode and `--exec` stop with a `no model configured` error until a model can be resolved from a profile `default_model`, `--model` or `STEINER_MODEL`. `oneshot` additionally needs models for its phases and the advisor. `config`, `tools` and `skills` run without a model.

For the environment variables Steiner reads, see [Configuration](configuration.md).

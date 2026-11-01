# Quickstart

This guide takes you from a provider connection to your first interactive task. First [install Steiner](installation.md).

## Prerequisites

- A running provider endpoint or a cloud provider API key.
- On Linux, `bubblewrap` (`bwrap`) on `PATH` for sandboxing.
- `git` for delegation worktrees and oneshot runs.

## Configure a model

For a local example, start an Ollama model:

```bash
ollama run qwen2.5-coder:14b
```

Create `~/.config/steiner/config.yaml` for your user, or `.steiner/config.yaml` in your project, with this provider and model profile:

```yaml
providers:
  ollama:
    type: ollama
    base_url: http://localhost:11434/v1

models:
  profiles:
    default:
      default_model: ollama/qwen2.5-coder:14b
```

Ollama requires an explicit `base_url`. Replace the model ID with one installed in Ollama. For cloud providers and API keys, see [provider and model setup](provider-and-model-setup.md). See [configuration](configuration.md) for file precedence and settings.

## Start a session

In your project directory, run:

```bash
steiner
```

If prompted to trust the project, review its configuration changes before accepting. This trust prompt is interactive only.

Describe a small task, such as updating a project README, and press `enter`. When a tool accesses a path outside the project or a sandboxed command is denied, you may be asked for approval: press `y` to approve once, `a` to allow the tool for the session, or `n` to deny it. Review the resulting edit in your project. To exit, use `/exit` or press `ctrl+c` while idle.

See [interactive mode](interactive.md) for controls and approval details.

## Next steps

- Run one non-interactive request with `steiner --exec "summarise this repository"`.
- Press `ctrl+f1` for help.
- Press `shift+tab` to switch between plan and build modes. See [execution modes](execution-modes.md).
- Type `/` in an empty composer to browse commands and [skills](skills.md).

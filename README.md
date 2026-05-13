# steiner

`steiner` is a minimal, local-first coding agent in Go. It is built to work against real local repositories with bounded context, explicit approvals, and OpenAI-compatible providers, including a local server on `http://localhost:11434/v1`.

## Today in brief

* Single-agent loop with interactive terminal mode and `--exec`
* Config, tools, skills, and version commands are available now
* Default provider targets a local OpenAI-compatible endpoint
* Mutating tools are approval-gated by default; reads are auto-approved

## Quickstart

Requirements: Go `1.25+`.

1. Start from source in this repo.
2. Make sure an OpenAI-compatible server is running at `http://localhost:11434/v1`, or override the provider settings in config.
3. Run a first request from source:

```bash
go run ./cmd/steiner --exec "summarize this repository in one sentence"
```

4. Optional: inspect the resolved configuration before you do real work:

```bash
go run ./cmd/steiner config
```

If you want a local binary:

```bash
make build-binaries
./bin/steiner
```

## Common commands

* `go run ./cmd/steiner` or `./bin/steiner` - interactive mode
* `go run ./cmd/steiner --exec "..."` - run one request and exit
* `go run ./cmd/steiner version` - print the build version
* `go run ./cmd/steiner config` - print the resolved configuration
* `go run ./cmd/steiner tools` - list configured tools
* `go run ./cmd/steiner skills` - list discovered skills

## Configuration

Configuration precedence is:

1. compiled defaults
2. `~/.config/steiner/config.yaml`
3. `.steiner/config.yaml`
4. environment variables with the `STEINER_` prefix
5. CLI flags

Key environment variables:

* `STEINER_MODEL`
* `STEINER_SCHEDULER_PARALLELISM`
* `STEINER_MAX_TURNS`
* `STEINER_MAX_TOKENS`
* `STEINER_LOG_LEVEL`
* `STEINER_LOG_FILE`
* `STEINER_TOOL_OUTPUT_MAX_BYTES`

### Config format

The top-level `model` property accepts **two shapes**:

1. **Alias string** — references a preset from the `models` map below. The matching `models.<alias>` entry **replaces** the compiled defaults entirely.
2. **Inline mapping** — directly configures the active model with the same keys as a `models` entry. This **patches** the compiled defaults field-by-field.

The example below reflects the compiled defaults in `internal/config/defaults.go`.

```yaml
# How many provider requests the scheduler may run at once.
scheduler:
  # Minimum is 1.
  parallelism: 1

# @TODO: re-evaluate this, it's dirty and unclear
# --- Option 1: model alias from models property ---
# model: default
#
# --- Shape 2: inline mapping ---
# model:
#   type: openai_compat
#   base_url: http://localhost:11434/v1
#   model: qwen3-35b-a3b
#   max_completion_tokens: 8192
#   context_size: 32768
#   compaction:
#     safety_margin_tokens: 2048
#     summary_max_tokens: 1024

# Using the alias form for this example:
model: default

# Available model/provider presets.
models:
  # Default model alias used when `model: default` is selected.
  default:
    # Provider implementation. Currently `openai_compat` is supported.
    type: openai_compat

    # Base URL for the OpenAI-compatible API.
    base_url: http://localhost:11434/v1

    # API key sent to the provider. Empty is fine for many local servers.
    api_key: ""

    # Backend model identifier sent in chat requests.
    model: qwen3-35b-a3b

    # Per-request output token cap.
    max_completion_tokens: 8192

    # Estimated total context window for prompt budgeting.
    context_size: 32768

    # Extra parameters sent to the provider with each request.
    extra_params:
      temperature: 0.2

    # Prompt overrides for this model. These replace the embedded defaults.
    prompts:
      # Replaces the default agent system preamble.
      system: ""

      # Replaces the default compaction system prompt.
      compaction: ""

    # Compaction settings used when prompt context gets tight.
    compaction:
      # Tokens reserved as headroom before compaction is triggered.
      safety_margin_tokens: 2048

      # Max size of the generated summary during compaction.
      summary_max_tokens: 1024

    # Thinking configuration for extended reasoning.
    thinking:
      # Master switch; when false, thinking params are never injected.
      enabled: true

      # Apply thinking to scaffold inference calls.
      enabled_scaffolding_inference: false

      # Marker to include in a message to suppress thinking for that request only.
      # When found in the last user message, the marker is stripped and thinking
      # is suppressed for that turn.
      disable_marker: "<|think_off|>"

      # Parameters merged into the API request when thinking is active.
      # Supports scalar and nested map values. Takes precedence over extra_params
      # on key collision.
      params:
        thinking:
          type: enabled
          budget_tokens: 10000

# Global loop and tool execution limits.
limits:
  # Maximum number of agent turns; 0 means no turn cap.
  max_turns: 50

  # Maximum total tokens the run may consume before stopping.
  max_tokens: 500000

  # Fallback timeout for tools without a specific override.
  tool_timeout_default: 30s

  # Per-tool timeout overrides.
  tool_timeouts:
    # Shell command execution timeout.
    bash: 2m0s

    # File read timeout.
    read: 5s

    # File write timeout.
    write: 5s

    # File edit timeout.
    edit: 5s

    # Text search timeout.
    grep: 30s

    # Directory listing timeout.
    ls: 5s

  # Maximum bytes captured from any single tool result.
  tool_output_max_bytes: 65536

# Approval policy for built-in and configured tools.
approval:
  # Default approval mode for tools without a specific override.
  default: auto

  # Per-tool approval overrides.
  # Omit a tool here to inherit approval.default.
  tool_overrides:
    # Prompt before shell commands.
    bash: prompt

    # Prompt before file writes.
    write: prompt

# Optional sub-agent limits and permissions.
sub_agent:
  # Enables isolated delegated sub-agents.
  enabled: false

  # Maximum turns each sub-agent may use when enabled.
  max_turns: 15

  # Maximum total tokens each sub-agent may consume when enabled.
  max_tokens: 100000

  # Tool names sub-agents are allowed to call.
  allowed_tools:
    - read
    - glob
    - grep
    - ls
    - write
    - edit
    - bash

# Custom JSON-schema tools exposed to the agent.
tools:
  # Tool name shown to the model.
  repo-lint:
    # Executable path invoked for this tool.
    exec: /usr/local/bin/repo-lint

    # Optional first argument passed after exec.
    subcommand: repo-lint

    # Human-readable description shown in the tool schema.
    description: Runs the repo linter and reports actionable failures.

    # JSON Schema for tool input.
    parameters:
      type: object
      properties:
        # Example input field accepted by the tool.
        path:
          type: string
      required:
        - path
    # Tool-specific timeout override.
    timeout: 30s

    # Approval mode for this tool.
    approval: auto

    # Tool-specific execution constraints consumed by the runtime/tooling.
    constraints:
      # Example allowlist for paths the tool may target.
      allowed_dirs:
        - /home/luis/Projects/AI/steiner

# Files considered for automatic project context gathering.
project_context:
  # Token budget reserved for project context snippets.
  max_tokens: 2000

  # Extra files to include in project context when present.
  extra_files:
    - README.md
    - AGENTS.md

  # Glob patterns skipped during project context discovery.
  ignore_files:
    - "*.lock"
    - node_modules/

# Filesystem policy applied to tool execution.
paths:
  # Restricts file access to the project root unless explicitly allowed.
  project_root_only: true

  # Additional writable paths allowed outside the project root.
  writable_paths:
    - /tmp/steiner

  # Paths that remain blocked even if a tool would otherwise be allowed.
  blocked_paths:
    - ~/.ssh

  # Additional paths excluded from project context gathering.
  exclude_paths:
    - /tmp

  # Glob patterns excluded from project context gathering.
  exclude_patterns:
    - "*.tmp"

# Session logging settings.
logging:
  # Turns session log writing on or off.
  enabled: true

  # Log verbosity. Supported: trace, debug, info, warn, error.
  level: info

  # Log file destination; `~` expands to the user's home directory.
  file: ~/.local/share/steiner/steiner.log

  # Include thinking chunks in the session log.
  thinking_chunk: false
```

Approval defaults are conservative: `read`, `glob`, `grep`, and `ls` are auto-approved; mutating actions like `write`, `edit`, and `bash` prompt first. For most installs, the minimum useful config is just `model`, one `models.<alias>` entry, and any overrides you actually need in `limits`, `approval`, `tools`, `project_context`, `paths`, or `logging`.

## Development

### Build and test

```bash
go build ./...
go test ./...
make build-binaries
go vet ./...
```

### Development checks

Install local check tools:

```bash
make install-check-tools
```

Fast local loop:

```bash
make quick-check
```

Full local/CI gate:

```bash
make ci-check
```

Formatting:

```bash
make fmt
```

The full linter configuration is in `.golangci.yml` at the repo root.

## Development notes

Repo layout is compact: `cmd/` for entrypoints, `internal/` for agent/provider/tool/config code, `docs/` for product docs, and `testdata/` for fixtures. Conventions and deeper repo rules live in [AGENTS.md](AGENTS.md).

## Further reading

* [AGENTS.md](AGENTS.md)
* [docs/PRD.md](docs/PRD.md)
* [docs/ROADMAP.md](docs/ROADMAP.md)
* [docs/INITIAL_IMPLEMENTATION_PLAN.md](docs/INITIAL_IMPLEMENTATION_PLAN.md)

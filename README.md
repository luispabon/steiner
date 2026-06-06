# steiner

A minimal, local-first Go coding agent with bounded context and sandboxed execution.

- **Delegation-first**: sub-agents isolate work so it never fills the parent conversation
- **Sandboxed execution**: bash and subprocess tools run inside a bubblewrap sandbox; sandbox violations prompt for user decision
- **Provider-agnostic**: same config shape for local and cloud providers

## Why steiner

Most coding agents are designed for cloud models with large context windows. Steiner is designed for the opposite constraint: local LLMs where context is expensive, reasoning quality degrades as the window fills, and you want to know exactly what is happening before anything changes on disk.

The core bet is that delegation is a better context strategy than summarisation. When a sub-agent handles exploration or code changes, the parent conversation never sees the intermediate turns — only the result. Combined with per-source byte budgets and late-stage compaction, this keeps the working context lean across long sessions.

By default, bash and subprocess tools run inside a sandbox that prevents writes outside the workspace and filters credential-bearing environment variables. Users can temporarily allow writable access to specific paths or disable sandboxing entirely with the `--unsafe` flag.

The provider/model split means you can point steiner at Ollama, LM Studio, OpenRouter, or a native Anthropic or OpenAI endpoint with the same config shape and the same tool behavior.

## Quickstart

1. **Prerequisites**: Go `1.25+`. For local models, [Ollama](https://ollama.com) or [LM Studio](https://lmstudio.ai).
2. **Start a local model** (Ollama example):
   ```bash
   ollama run qwen2.5-coder:14b
   ```
3. **Run from source**:
   ```bash
   go run ./cmd/steiner --exec "summarize this repository in one sentence"
   ```
   Or start interactive mode:
   ```bash
   go run ./cmd/steiner
   ```
4. **Inspect resolved configuration** before doing real work:
   ```bash
   go run ./cmd/steiner config
   ```
5. **Optional — build a local binary**:
   ```bash
   make build-binaries
   ./bin/steiner
   ```

## Usage

**Interactive mode** — launches the TUI, accepts natural-language requests, streams tool calls and responses:

```bash
go run ./cmd/steiner
# or
./bin/steiner
```

**Single-shot** — runs one request and exits:

```bash
go run ./cmd/steiner --exec "explain the auth package"
```

**Commands reference**:

| Command | What it does |
|---------|--------------|
| `version` | Print the build version |
| `config` | Print the resolved configuration (providers, models, limits) |
| `tools` | List configured tools and their approval status |
| `skills` | List discovered skills |
| `--help` | Print all flags and usage |

## Configuration

Configuration is loaded in this precedence order (later overrides earlier):

1. Compiled defaults
2. `~/.config/steiner/config.yaml`
3. `.steiner/config.yaml` (project-local)
4. Environment variables with the `STEINER_` prefix
5. CLI flags

**Example 1 — local LLM (Ollama or LM Studio)**:

```yaml
default_model: local
providers:
  local:
    type: openai_compat
    base_url: http://localhost:11434/v1
models:
  local:
    provider: local
    id: qwen2.5-coder:14b
```

Use `http://127.0.0.1:1234/v1` as `base_url` for LM Studio.

**Example 2 — cloud provider (Anthropic via OpenRouter)**:

```yaml
default_model: sonnet

providers:
  openrouter:
    type: openrouter
    api_key_env: OPENROUTER_API_KEY

models:
  sonnet:
    provider: openrouter
    id: anthropic/claude-3.7-sonnet
```

For the full configuration reference — all provider types, model fields, limit overrides, sandbox settings, sub-agent config, and environment variables — see [docs/CONFIGURATION.md](docs/CONFIGURATION.md).

## Built-in tools

These tools are always available to the model:

| Tool | Description |
|------|-------------|
| `read` | Read files with offset/limit pagination |
| `mutate` | Apply one or more structured file mutations atomically (create, write, replace, line_replace, delete, move) |
| `glob` | Find files by pattern |
| `grep` | Search file contents with surrounding context |
| `ls` | List directory contents |
| `bash` | Run shell commands |
| `scratchpad` | Record working state (intent, decisions, next action); persists across compaction |
| `display_file` | Show a file in the TUI overlay without adding its contents to the conversation |
| `workflow_handoff` | Create a pending handoff request for an approved `.steiner/plans/...` directory in the parent session |

`read`, `glob`, `grep`, `ls`, and other read-only tools are always available. `bash` and subprocess tools run inside a sandbox by default.

## Sandboxing

By default, `bash` and subprocess tools run inside a Linux sandbox (using `bubblewrap`) that restricts writes to files outside the workspace and filters credential-bearing environment variables. The sandbox bind-mounts the entire host filesystem read-only (`--ro-bind / /`), with writable overlays for the workspace and a sandbox state directory (`.steiner/home/`). All installed toolchains, system libraries, and user config files are accessible read-only without per-path configuration.

When a sandboxed tool attempts to write to a file outside the workspace, the user is prompted to either grant temporary access, use the `--unsafe` flag to disable sandboxing, or cancel the operation. This protects against accidental workspace-external writes and environment variable leakage. Reading host files (including on-disk credentials) is not restricted — sandboxing is not a confidentiality boundary.

**Platform support**: Linux only (automatic detection; sandboxing is disabled on macOS and Windows).

For configuration, environment variable filtering, mount layout, and troubleshooting, see [docs/TOOL_SANDBOXING.md](docs/TOOL_SANDBOXING.md).

## Context management

Local LLMs have limited context windows — often measured in tens of thousands of tokens, not millions. Every turn accumulates tokens from model output and tool results, and long contexts cost more while degrading reasoning quality. Steiner keeps the context window lean through three mechanisms, ordered by effectiveness.

**Delegation** is the primary strategy. Sub-agents isolate work from the parent conversation. The full turn-by-turn transcript of exploration, code changes, or research never enters the parent context at all — only the result comes back. This is the most effective mechanism because it prevents context growth rather than managing it after the fact.

**Per-source byte budgets** cap each context source (preamble, agent summaries, skills, tool results, conversation history) so no single category can crowd out the others. **Compaction** kicks in when estimated prompt tokens reach 70% of the context window: older turns are summarised by the model into a compact durable prefix, then dropped from the live history.

See [docs/CONTEXT_MANAGEMENT.md](docs/CONTEXT_MANAGEMENT.md) for the full reference.

## Sub-agent delegation

Delegation is steiner's primary context management strategy. `steiner` exposes seven sub-agent tools that delegate bounded tasks to isolated child agents. Sub-agent delegation is enabled by default — the model sees these tools alongside the built-ins:

| Tool | What it does | Can mutate? |
|------|--------------|-------------|
| `explore` | Navigate the codebase to find files, symbols, call sites, and patterns | No |
| `research` | Gather and synthesise information from the codebase or web | No |
| `code` | Implement a scoped change — read relevant files, write changes, run tests | Yes |
| `plan` | Analyse a sub-problem, evaluate options, and produce a structured recommendation | No |
| `verify` | Run tests, linters, builds, or other checks and report pass or fail | No |
| `delegate` | Generic sub-agent with custom system prompt, context, and per-invocation overrides | Configurable |
| `follow_up` | Resume an existing sub-agent session by agent ID with a new user message | No |

See [docs/SUBAGENT_DELEGATION.md](docs/SUBAGENT_DELEGATION.md) for full documentation, including per-agent tool allowlists, safety restrictions, and per-invocation overrides.

## Optional features

### Caveman mode

Caveman mode makes the model respond tersely — stripping filler, articles, pleasantries, and hedging. This reduces output token growth and response length while preserving technical content. The instruction is injected into the system preamble, compaction prompts, and sub-agent prompts so terseness is consistent throughout a session.

Disabled by default. Enable via config, env var, CLI flag, or `/caveman` in the interactive TUI:

```yaml
# config.yaml
caveman_mode: true
```

```bash
STEINER_CAVEMAN_MODE=true   # environment variable
--caveman                   # CLI flag
/caveman                    # TUI toggle (persists for the session)
```

### Web search

The `web_search` tool lets the model search the web and return URL, title, and description results. It is **disabled by default** — it only appears in the tool registry when `search.backend` is set in config.

| Backend | Config key | Auth |
|---------|------------|------|
| Google | `google` | `GOOGLE_SEARCH_CX` + `GOOGLE_SEARCH_API_KEY` |
| Kagi | `kagi` | `KAGI_API_KEY` |
| Brave | `brave` | `BRAVE_API_KEY` |
| SearXNG | `searxng` | `search.searxng_url` (self-hosted, no API key) |

Enable by adding a `search` block to your config:

```yaml
search:
  backend: brave   # one of: google, kagi, brave, searxng
```

When enabled, `web_search` is also added to the `research` sub-agent's tool allowlist automatically.

## Development

```bash
# Build
go build ./...
make build-binaries

# Test
go test ./...
go test -race ./...

# Lint and vet
go vet ./...
golangci-lint run ./...

# All checks (lint + vet + format check)
make check

# Format
make fmt
```

For agent contributor context, architecture constraints, and conventions, see [AGENTS.md](AGENTS.md).

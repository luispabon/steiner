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
| `version` | Print the build version; `--short` for script-friendly output |
| `config` | Print the resolved configuration (providers, models, limits) |
| `update` / `upgrade` | Self-update to the latest stable release |
| `tools` | List configured tools and their approval status |
| `skills` | List discovered skills |
| `--help` | Print all flags and usage |

### Output format

`steiner --version` and `steiner version` print a multi-line panel with
build metadata:

```
  version    v0.1.0
  commit     abc1234
  built      2026-06-17T12:00:00Z
  go         go1.25.0
  channel    stable
```

Labels are bold with the configured accent color; values use the default foreground. Use
`steiner version --short` to print only the raw version string (no styling,
no labels) for scripts and CI.

`steiner update` (alias `upgrade`) shows a four-stage output:

```
  current    v0.1.0
  Downloading…
  ✔ updated to v0.2.0
  latest     v0.2.0
```

A spinner animates during the download. On a non-TTY or when `NO_COLOR` is
set, the spinner degrades to a static line. Dev builds without `--dev` print
a warning and exit without contacting GitHub.

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

**Example 3 — one provider with mixed-model transport (OpenCode Go)**:

```yaml
default_model: kimi

providers:
  opencode-go:
    type: openai_compat
    base_url: https://opencode.ai/zen/go/v1
    api_key_env: OPENCODE_API_KEY

models:
  kimi:
    provider: opencode-go
    id: kimi-k2.6
  minimax:
    provider: opencode-go
    id: minimax-m3
```

For the full configuration reference — all provider types, model fields, limit overrides, sandbox settings, sub-agent config, and environment variables — see [docs/CONFIGURATION.md](docs/CONFIGURATION.md).

Workflow handoff defaults live in the `workflow_handoff` block in that reference. If a destination workflow does not have a configured alias, Steiner falls back to the current session model. The handoff model picker can override the pending handoff once without changing config.

## Built-in tools

These built-in tools are available to the model; some are gated by config:

| Tool | Description |
|------|-------------|
| `read` | Read files with offset/limit pagination. Detects image files (.png, .jpg, .jpeg, .gif, .webp) by extension, base64-encodes them, and returns a metadata summary (`[image: WxH format size]`) with the image data attached as a content block. Max image size: 5MB. |
| `mutate` | Apply one or more structured file mutations atomically with sequential in-memory matching, initial-snapshot `file_hash` validation on existing targets, post-operation assertions, and bounded verification context (create, write, replace, line_replace, delete_line, delete, move) |
| `glob` | Find files by pattern |
| `grep` | Search file contents with surrounding context |
| `ls` | List directory contents |
| `bash` | Run shell commands |
| `scratchpad` | Record working state (intent, decisions, next action); persists across compaction |
| `fetch_url` | Fetch a URL and return its content. Web pages are converted to markdown; image URLs (png, jpeg, gif, webp) return image data for vision-capable providers (5MB cap). The uncollapsed view shows a status block (URL, max_size, http code, content length, title, description) above the markdown body. |
| `display_file` | Show a file in the TUI overlay without adding its contents to the conversation |
| `advisor` | Ask a stronger-model steering advisor for concise guidance when `advisor.enabled` is true |
| `workflow_handoff` | Create a workflow handoff request to transition to a different workflow with approved artifacts |

`read`, `glob`, `grep`, `ls`, and other read-only tools are always available. `bash` and subprocess tools run inside a sandbox by default.

`mutate` evaluates operations in order against an in-memory snapshot of earlier edits, then commits the full batch only after planning succeeds. Line-oriented edits can target a single line or a contiguous line range with `line_replace` or `delete_line`. `insert_after` supports appending after the final line even when the file has no trailing newline; in that case the tool synthesizes the file's line ending so the inserted block lands on its own line. Any `file_hash` check compares against the disk state captured at batch start, not against later in-batch changes, and only applies when the target already exists. Missing targets fail explicitly instead of being silently accepted. Successful results also return per-file `file_hashes` plus per-operation `operation_results` with `match_count`, assertion counts, and bounded post-edit `context` so the model can verify edits without an immediate follow-up `read`. `assert_present` and `assert_absent` run against the in-memory post-operation content, and any assertion failure aborts the batch before commit. `move` never overwrites an existing destination; destination collisions fail instead of replacing files.

## Sandboxing

By default, `bash` and subprocess tools run inside a Linux sandbox (using `bubblewrap`) that restricts writes to files outside the workspace and filters credential-bearing environment variables. The sandbox bind-mounts the entire host filesystem read-only (`--ro-bind / /`), with writable overlays for the workspace and a sandbox state directory (`.steiner/home/`). All installed toolchains, system libraries, and user config files are accessible read-only without per-path configuration.

Steiner also creates an ephemeral in-memory OpenSSH client-config overlay for sandboxed commands so `ssh` can read the system client config and static drop-ins without copying SSH config into the workspace. If OpenSSH still rejects the config inside the sandbox, Steiner can prompt to rerun the command outside the sandbox.

When a sandboxed tool attempts to write to a file outside the workspace, the user is prompted to either grant temporary access, use the `--unsafe` flag to disable sandboxing, or cancel the operation. This protects against accidental workspace-external writes and environment variable leakage. Reading host files (including on-disk credentials) is not restricted — sandboxing is not a confidentiality boundary, and private SSH keys are never copied into the workspace.

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

### `cave_human`

`cave_human` combines terse-output behavior with an anti-AI-writing style instruction. It keeps responses short and direct while avoiding filler, hedging, and common AI-writing tells. The instruction is injected into the system preamble, compaction prompts, and sub-agent prompts so it stays consistent throughout a session.

Disabled by default. Enable via config file only:

```yaml
# config.yaml
cave_human: true
```

### Advisor

`advisor` is a stronger-model steering pass for the main loop. It reviews the live parent conversation and returns concise guidance, but it does not call tools, mutate state, or spawn a child loop. The advisor tool definition stays static during a run; `max_uses_per_run` is enforced in handler state so prompt-cache state does not churn mid-conversation.

Disabled by default. Enable via config file only:

```yaml
advisor:
  enabled: true
  model: advisor-model
  max_uses_per_run: 2
  max_tokens: 256
```

`model` must reference a key in `models` when the feature is enabled. `max_tokens` is optional.

See [docs/ADVISOR_SUBAGENT.md](docs/ADVISOR_SUBAGENT.md) for the full behavior and implementation reference.


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

### Image paste

Paste images directly in the interactive TUI with **Ctrl+V**. Images are read from the clipboard or referenced by file path, resized to a max of 2048px on the longest side, and token-accounted automatically. After the model responds, image data is stripped from the conversation and replaced with a text placeholder, keeping context lean. Models without vision capability have images stripped before sending.

Supported formats: PNG, JPG, JPEG, GIF, WebP. Max size: 5MB.

### Conversation forking

Fork the current conversation or any saved session into a new independent session. The new session carries the full conversation history from the source, allowing you to explore alternative directions without modifying the original.

**In interactive mode**, use `/fork` to fork the current live session:

```
/fork
```

**In the session picker**, press `f` on any saved session to fork it. The new session is created with the title "Fork of: <original title>" and can be resumed, edited, or deleted like any other session.

Forks are independent — changes in one session do not affect the source.

## Self-update

Steiner can self-update via the `update` (or `upgrade`) command. It fetches the
latest release from GitHub, verifies the binary checksum, and atomically
replaces the running executable.

A GitHub token is not strictly required, but GitHub API rate limits may apply
without one. Set `STEINER_GITHUB_TOKEN` in your environment to authenticate:

```bash
export STEINER_GITHUB_TOKEN=ghp_...
```

### Dev channel

The `update` command selects the release channel via the `--dev` flag, which is
a root-level persistent flag and also works on the `update` subcommand:

```bash
# Stable channel (default): dev builds upgrade to the latest stable release.
steiner update
steiner --dev=false update

# Dev channel: pulls the build published on every merge to main.
steiner update --dev
steiner --dev update
```

`--dev` selects the dev release channel; its absence selects the stable
channel. Both a dev build version string (`dev` or `dev-<sha>`) and a stable
semver (`vX.Y.Z`) honor the flag, so a dev build can update to a stable
release by running `steiner update` (no `--dev`). The dev binary is always
replaced with the latest dev release; the stable binary is only replaced when
a newer semver is available.

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

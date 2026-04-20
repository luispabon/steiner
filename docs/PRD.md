# steiner - Product Requirements Document

**Version:** 0.1.0 (v1)
**Status:** Draft
**Date:** 2026-04-20

---

## 1. Overview

steiner is a minimal, local-first coding agent written in Go. It takes a task from the user, reasons about it using an LLM, executes tool calls against the local filesystem and shell, and iterates until the task is complete. It prioritises simplicity, extensibility, and a small system prompt footprint over framework complexity.

steiner is not a framework. It is a single opinionated binary that ships with sensible defaults, a plugin system for tools, sub-agent support for task decomposition, and a skills system for reusable context injection.

## 2. Design Principles

- **Minimal system prompt injection.** The agent injects a short preamble, AGENTS.md conventions, and auto-discovered project context. The model's native capabilities do the heavy lifting.
- **Plugin-first tools.** Core tools (read, write, bash, glob) ship as the default plugin. Additional tools use the same external binary interface - no distinction between "built-in" and "user-defined."
- **No framework dependencies.** steiner is a single statically-linked Go binary. No Python, no Node, no runtime dependencies.
- **Sandbox-ready architecture.** v1 executes tools directly, but the tool execution layer is designed as a clean interface that a sandbox wrapper (container-based or otherwise) can sit in front of later.
- **LLM-agnostic with provider abstraction.** v1 targets OpenAI-compatible APIs (covering Ollama, llama.cpp, vLLM, OpenRouter, and the commercial OpenAI/Groq/etc. endpoints). The provider interface is abstract from day one.
- **User-driven context.** Skills are never auto-loaded or surfaced to the LLM. The user decides what context the agent needs and invokes it explicitly.

## 3. Architecture

### 3.1 Core Agent Loop

The agent loop follows the universal ReAct pattern:

```
1. Receive prompt (user input + system prompt + conversation history)
2. Call LLM
3. If response contains tool_calls:
   a. Execute each tool call (with approval check if required)
   b. Append tool results to conversation history
   c. Go to 2
4. If response is text-only (no tool_calls):
   a. Display response to user
   b. If REPL mode: wait for next user input, go to 1
   c. If single-shot mode: exit
```

The loop terminates when the LLM produces a text-only response, or when any termination control is triggered (see section 3.7).

### 3.2 Provider Abstraction Layer

All LLM communication goes through an abstract provider interface:

```go
type Provider interface {
    // ChatCompletion sends a request and returns a response.
    // Streaming is handled internally; the response contains
    // the fully assembled message plus usage metadata.
    ChatCompletion(ctx context.Context, req ChatRequest) (*ChatResponse, error)

    // StreamChatCompletion sends a request and returns a channel
    // of streaming chunks for real-time terminal output.
    StreamChatCompletion(ctx context.Context, req ChatRequest) (<-chan ChatChunk, error)

    // SupportsUsageStats reports whether this provider returns
    // token usage in responses (needed for budget enforcement).
    SupportsUsageStats() bool
}
```

v1 ships with a single provider implementation: `openai_compat`, which covers any API conforming to the OpenAI Chat Completions spec. Additional providers (Anthropic native, Google Gemini) can be added as new implementations of this interface without touching the agent loop.

Configuration selects the provider and passes through provider-specific settings:

```yaml
provider:
  type: openai_compat
  base_url: ${STEINER_BASE_URL:-http://localhost:11434/v1}
  api_key: ${STEINER_API_KEY}
  model: ${STEINER_MODEL:-qwen3-35b-a3b}
  temperature: 0.2
  max_completion_tokens: 8192
```

Environment variable interpolation in YAML values allows secrets to stay out of config files. The config file sets defaults; environment variables can override any value.

### 3.3 Tool System

#### 3.3.1 Tool Execution Interface

All tools - core and user-defined - are external processes conforming to the same contract:

**Input:** JSON payload on stdin:

```json
{
  "tool": "read",
  "arguments": {
    "path": "src/auth/middleware.go"
  },
  "working_directory": "/home/user/project"
}
```

**Output:** JSON payload on stdout:

```json
{
  "result": "package auth\n\nimport...",
  "error": null,
  "metadata": {
    "bytes_read": 2048
  }
}
```

**Raw text fallback:** If the tool's stdout is not valid JSON, steiner wraps it in a result envelope automatically:

```json
{
  "result": "<raw stdout content>",
  "error": null
}
```

If the tool exits with a non-zero exit code, steiner captures stderr and constructs an error response regardless of stdout content.

This fallback means simple shell scripts work as tools without JSON awareness:

```bash
#!/bin/bash
# tools/line-count - counts lines in a file
wc -l "$1"
```

#### 3.3.2 Tool Registration

Tools are declared in YAML config. Each tool definition includes the binary path, a description (passed to the LLM as part of the tool schema), parameter schema, and execution constraints:

```yaml
tools:
  read:
    binary: steiner-core-tools
    subcommand: read
    description: "Read the contents of a file at the given path. Use this to examine source code, configuration, documentation, or any text file."
    parameters:
      path:
        type: string
        required: true
        description: "Absolute or relative path to the file to read"
    timeout: 5s
    approval: auto

  write:
    binary: steiner-core-tools
    subcommand: write
    description: "Write content to a file, creating it if it doesn't exist or overwriting if it does. Always read a file before writing to understand existing content and structure."
    parameters:
      path:
        type: string
        required: true
        description: "Path to the file to write"
      content:
        type: string
        required: true
        description: "Complete file content to write"
    timeout: 5s
    approval: prompt

  bash:
    binary: steiner-core-tools
    subcommand: bash
    description: "Execute a bash command in the project directory. Use for running tests, installing dependencies, git operations, and any command-line task. Prefer targeted commands over broad ones."
    parameters:
      command:
        type: string
        required: true
        description: "The bash command to execute"
    timeout: 120s
    approval: prompt

  glob:
    binary: steiner-core-tools
    subcommand: glob
    description: "Find files matching a glob pattern. Returns a list of matching file paths relative to the project root."
    parameters:
      pattern:
        type: string
        required: true
        description: "Glob pattern (e.g. '**/*.go', 'src/**/*.test.ts')"
    timeout: 10s
    approval: auto

  search:
    binary: steiner-core-tools
    subcommand: search
    description: "Search file contents using a regex pattern. Returns matching lines with file paths and line numbers."
    parameters:
      pattern:
        type: string
        required: true
        description: "Regex pattern to search for"
      paths:
        type: array
        items: string
        required: false
        description: "File paths or glob patterns to search within. Defaults to entire project."
    timeout: 30s
    approval: auto
```

The `steiner-core-tools` binary is a single Go binary that multiplexes the core tools via the `subcommand` field. User-defined tools point to their own binaries.

#### 3.3.3 Tool Schema Generation

steiner converts the YAML tool definitions into the OpenAI-compatible `tools` array format at startup. This is the JSON schema the LLM receives:

```json
{
  "type": "function",
  "function": {
    "name": "read",
    "description": "Read the contents of a file...",
    "parameters": {
      "type": "object",
      "properties": {
        "path": {"type": "string", "description": "..."}
      },
      "required": ["path"]
    }
  }
}
```

#### 3.3.4 Tool Execution Layer

The tool executor is an interface designed for future sandbox interposition:

```go
type ToolExecutor interface {
    Execute(ctx context.Context, tool ToolDef, input ToolInput) (*ToolOutput, error)
}
```

v1 ships with `DirectExecutor`, which spawns the tool binary as a subprocess. A future `ContainerExecutor` would implement the same interface, wrapping tool invocations in `docker run` / `podman run` calls with bind mounts and network restrictions.

### 3.4 Sub-Agent System

Sub-agents use Pattern A: sub-agent as tool with isolated context.

The orchestrator (main agent loop) has access to a special built-in tool called `spawn_agent`. When the LLM calls `spawn_agent`, steiner creates a new agent loop instance with:

- Its own empty conversation history
- Its own system prompt (the sub-agent task description becomes the prompt)
- Its own termination controls (configurable, defaults to lower limits than the parent)
- Access to the same tool set as the parent (configurable - can be restricted)

The sub-agent runs its loop to completion and returns its final text response as the tool result to the orchestrator. The orchestrator never sees the sub-agent's intermediate tool calls or reasoning - only the final output. This keeps the orchestrator's context window clean.

```yaml
sub_agent:
  max_turns: 15
  max_tokens: 100000
  allowed_tools: [read, glob, search, write, bash]
  # Sub-agents cannot spawn further sub-agents by default
  allow_nesting: false
  # Maximum concurrent sub-agents (for parallel spawn)
  max_concurrent: 3
```

The `spawn_agent` tool schema presented to the LLM:

```json
{
  "type": "function",
  "function": {
    "name": "spawn_agent",
    "description": "Spawn a sub-agent to handle a focused subtask. The sub-agent gets its own context window and runs independently. Use this for tasks that require significant exploration or work that would bloat the main conversation context. The sub-agent returns only its final result.",
    "parameters": {
      "type": "object",
      "properties": {
        "task": {
          "type": "string",
          "description": "Clear, specific description of the task for the sub-agent"
        },
        "context": {
          "type": "string",
          "description": "Relevant context from the current conversation that the sub-agent needs"
        }
      },
      "required": ["task"]
    }
  }
}
```

Parallel sub-agent support: the LLM can request multiple `spawn_agent` tool calls in a single response. steiner executes them concurrently (up to `max_concurrent`) using goroutines, collects all results, and returns them to the LLM together.

### 3.5 Skills System

Skills are reusable context snippets that the user injects into the conversation on demand. They are never auto-loaded and are never surfaced to the LLM unless explicitly invoked.

#### 3.5.1 Skill Location and Structure

Skills are stored globally at `~/.config/steiner/skills/`. Each skill is a directory containing a `SKILL.md` file:

```
~/.config/steiner/skills/
  copy-polisher/
    SKILL.md
  go-conventions/
    SKILL.md
  terraform-patterns/
    SKILL.md
```

The `SKILL.md` file contains the full skill content - instructions, examples, rules, templates - whatever the user wants injected into the conversation. There is no required metadata format; the file content is injected verbatim. The directory name is the skill's invocation name.

#### 3.5.2 Skill Invocation

Skills are invoked via the `/` command namespace in the REPL, using the directory name:

```
/copy-polisher
/go-conventions
/terraform-patterns
```

When invoked, the skill content is injected into the conversation history as a system message. The LLM sees it as additional context for the current and subsequent turns. A skill remains active in the conversation history for the remainder of the session (until `/clear`).

In single-shot mode, skills are invoked via the `--skill` flag (repeatable):

```
steiner --exec "refactor the handler" --skill go-conventions --skill copy-polisher
```

#### 3.5.3 Skill Discovery

steiner discovers available skills by scanning `~/.config/steiner/skills/` at startup. The list of available skill names is used solely for autocompletion (see section 5.4). Skill descriptions and content are never passed to the LLM unless the user explicitly invokes them.

### 3.6 System Prompt

The system prompt is composed of three layers, concatenated in order:

**Layer 1: Agent preamble (~150-200 tokens)**

A fixed, minimal preamble that establishes the agent's role and behavioural constraints:

```
You are steiner, a coding agent. You operate by reading files, understanding
code, making changes, and verifying your work.

Rules:
- Always read a file before modifying it.
- After making changes, verify them (run tests, linters, or re-read the file).
- Prefer targeted, minimal changes over broad rewrites.
- If a task is ambiguous, use available tools to gather context before
  asking the user for clarification.
- When spawning sub-agents, give them specific, self-contained tasks with
  enough context to work independently.
```

This preamble is intentionally short. The model's training already knows how to write code, reason about software, and use tools. The preamble exists to establish conventions, not to teach capabilities.

**Layer 2: AGENTS.md conventions (auto-discovered, variable length)**

steiner loads and merges AGENTS.md files from two locations at startup:

1. **Global:** `~/.config/steiner/AGENTS.md` - personal conventions that apply to all projects (coding style preferences, commit message format, preferred tools, etc.)
2. **Project:** `./AGENTS.md` in the project root - project-specific conventions (architecture rules, test frameworks, forbidden patterns, etc.)

Both files are optional. When both exist, the global file content is included first, followed by the project file content, separated by a clear delimiter:

```
--- Global conventions ---
<contents of ~/.config/steiner/AGENTS.md>

--- Project conventions ---
<contents of ./AGENTS.md>
```

AGENTS.md is the emerging industry standard for AI coding agent instructions, supported by Claude Code, Codex, and others. steiner uses it as the sole mechanism for user-authored agent instructions - there is no steiner-specific alternative.

**Layer 3: Project context (auto-discovered, variable length)**

At startup, steiner scans the project root for context files and includes their contents (truncated if necessary) in the system prompt:

Discovery order (first found wins for each category):

| Category | Files checked |
|---|---|
| Project description | `README.md`, `README`, `README.txt` |
| Language/framework | `go.mod`, `package.json`, `Cargo.toml`, `pyproject.toml`, `Makefile` |

**Layer 4: Tool descriptions (auto-generated)**

Not part of the `system` message content - these are passed via the `tools` parameter in the API request. Included here for completeness: the LLM sees tool names, descriptions, and parameter schemas as part of every request.

### 3.7 Termination Controls

Three independent safety valves, any of which can halt the agent loop:

| Control | Scope | Default | Configurable |
|---|---|---|---|
| Max turns | Agent loop | 50 | `limits.max_turns` |
| Token budget | Cumulative across all LLM calls | 500,000 | `limits.max_tokens` |
| Per-tool timeout | Individual tool invocation | 30s | `limits.tool_timeout_default` + per-tool `timeout` |

When a limit is hit:

- **Max turns:** The agent stops, prints the current state, and tells the user how many turns were used and why it stopped.
- **Token budget:** Same as max turns. If the provider doesn't return usage stats (`SupportsUsageStats() == false`), this control is skipped with a warning at startup.
- **Per-tool timeout:** The specific tool invocation is killed (context cancellation). The error is fed back to the LLM as a tool result: `{"error": "tool execution timed out after 120s"}`. The agent loop continues - the LLM can decide how to recover.

### 3.8 Approval System

Each tool has an `approval` setting: `auto` (execute immediately) or `prompt` (show the user and wait for confirmation).

The global `--auto-approve` CLI flag overrides all tools to `auto`. Intended for single-shot mode, CI pipelines, and users who trust their prompts.

When approval is required, steiner displays:

```
[tool: write] src/auth/middleware.go
  Writing 47 lines (1,203 bytes)
  [y]es / [n]o / [v]iew content / [a]lways approve this tool >
```

The `[a]lways` option sets the tool to `auto` for the remainder of the session, reducing friction without permanently changing config.

## 4. Configuration

### 4.1 Hierarchy

Configuration is loaded in order, with later sources overriding earlier ones:

1. **Compiled defaults** - hardcoded in the binary
2. **Global config** - `~/.config/steiner/config.yaml`
3. **Project config** - `.steiner/config.yaml` in the project root (found by walking up from cwd)
4. **Environment variables** - `STEINER_*` prefix, mapped to config keys
5. **CLI flags** - highest priority

### 4.2 Full Config Schema

```yaml
# ~/.config/steiner/config.yaml (global)
# or .steiner/config.yaml (project)

provider:
  type: openai_compat          # Provider implementation
  base_url: http://localhost:11434/v1
  api_key: ${STEINER_API_KEY}
  model: qwen3-35b-a3b
  temperature: 0.2
  max_completion_tokens: 8192

limits:
  max_turns: 50
  max_tokens: 500000
  tool_timeout_default: 30s
  tool_timeouts:              # Per-tool overrides
    bash: 120s
    read: 5s
    write: 5s

approval:
  default: prompt             # Default for new/unknown tools
  overrides:
    read: auto
    glob: auto
    search: auto
    write: prompt
    bash: prompt

sub_agent:
  max_turns: 15
  max_tokens: 100000
  allowed_tools: [read, glob, search, write, bash]
  allow_nesting: false
  max_concurrent: 3

tools: {}                     # Tool definitions (see section 3.3.2)

project_context:
  max_tokens: 2000            # Max tokens for auto-discovered context
  extra_files: []             # Additional files to include in context
  ignore_files: []            # Files to exclude from auto-discovery

logging:
  level: info                 # debug, info, warn, error
  file: ~/.local/share/steiner/steiner.log
```

### 4.3 Environment Variable Mapping

Environment variables use the `STEINER_` prefix with underscore-separated paths:

| Variable | Config path | Example |
|---|---|---|
| `STEINER_API_KEY` | `provider.api_key` | `sk-...` |
| `STEINER_BASE_URL` | `provider.base_url` | `http://localhost:11434/v1` |
| `STEINER_MODEL` | `provider.model` | `qwen3-35b-a3b` |
| `STEINER_MAX_TURNS` | `limits.max_turns` | `100` |
| `STEINER_LOG_LEVEL` | `logging.level` | `debug` |

## 5. CLI Interface

### 5.1 Commands

```
steiner                        # Launch REPL mode in current directory
steiner --exec "task"          # Single-shot mode
steiner --exec "task" --auto-approve  # Single-shot, no approval prompts
steiner --exec "task" --skill go-conventions  # Single-shot with skill
steiner --config path          # Use specific config file
steiner --model model-name     # Override model for this session
steiner --verbose              # Debug-level logging to terminal
steiner version                # Print version and exit
steiner tools                  # List available tools and their approval status
steiner skills                 # List available skills
steiner config                 # Print resolved config (all layers merged)
```

### 5.2 REPL Commands

Within the REPL, commands and skills share the `/` prefix namespace. Built-in command names are reserved and cannot be overridden by skills.

**Built-in commands:**

```
/help                          # Show available commands and skills
/tools                         # List available tools
/skills                        # List available skills
/history                       # Show conversation turn count and token usage
/clear                         # Clear conversation history (start fresh)
/config key value              # Override a config value for this session
/exit                          # Exit the REPL
```

**Skill invocation:**

```
/copy-polisher                 # Inject the copy-polisher skill into context
/go-conventions                # Inject the go-conventions skill into context
```

### 5.3 Terminal Input (Readline)

steiner uses `github.com/reeflective/readline` for terminal input handling, providing standard shell keybindings:

| Keybinding | Action |
|---|---|
| Up / Down | Navigate command history |
| Ctrl+R | Reverse history search |
| Alt+Backspace | Delete word backward |
| Ctrl+W | Delete word backward (alternative) |
| Alt+F / Alt+B | Move cursor forward/backward by word |
| Ctrl+A / Ctrl+E | Move cursor to beginning/end of line |
| Ctrl+C | Cancel current input |
| Ctrl+D | Exit REPL (on empty line) |

**Persistent history:** Command history is stored in `~/.local/share/steiner/history` and persists across sessions.

**Multi-line input:** Supported for pasting code blocks. The readline library handles line continuation and proper cursor movement across multiple lines.

### 5.4 Autocompletion

When the user types `/`, steiner presents a completion popup listing all available commands and skills. Entries are labelled by type to distinguish built-in commands from skills:

```
/co  ->  [cmd]   clear
         [cmd]   config
         [skill] copy-polisher
         [skill] coding-standards
```

Completion behaviour:

- Triggered on `/` keystroke, showing the full list
- Filtered as the user continues typing
- Arrow keys to navigate candidates
- Tab or Enter to select and insert the completion
- Esc to dismiss without selecting
- Built-in commands appear before skills in the list

Built-in command names are reserved. If a skill directory has the same name as a built-in command (e.g. a skill named `exit`), the skill is ignored and a warning is logged at startup.

### 5.5 Terminal Output

steiner streams LLM responses token-by-token to the terminal as plain text. Tool invocations are displayed with a brief header showing the tool name and arguments. Tool output is displayed with syntax highlighting where applicable (using chroma).

```
> find and fix the nil pointer dereference in the user handler

Thinking: Let me search for the user handler files first.

[tool: glob] **/*user*handler*.go
  Found: src/handlers/user_handler.go, src/handlers/user_handler_test.go

[tool: read] src/handlers/user_handler.go
  Read 89 lines

I can see the issue on line 47 - the `user` variable is used without a nil
check after the database lookup. Let me fix that.

[tool: write] src/handlers/user_handler.go
  Approve? [y/n/v/a] > y
  Written 92 lines

[tool: bash] go test ./src/handlers/...
  PASS (0.34s)

Fixed the nil pointer dereference. The `GetUser` handler now returns a 404
response when the user is not found, instead of dereferencing a nil pointer.
```

## 6. Project Structure

```
steiner/
  cmd/
    steiner/
      main.go                  # CLI entrypoint, cobra setup
  internal/
    agent/
      loop.go                  # Core agent loop
      subagent.go              # Sub-agent spawning
    provider/
      interface.go             # Provider interface definition
      openai_compat.go         # OpenAI-compatible provider
    tool/
      executor.go              # ToolExecutor interface + DirectExecutor
      registry.go              # Tool loading from YAML config
      schema.go                # YAML -> OpenAI tool schema conversion
    config/
      config.go                # Config loading, merging, env interpolation
      defaults.go              # Compiled defaults
    prompt/
      system.go                # System prompt assembly
      context.go               # Project context auto-discovery
      agents.go                # AGENTS.md loading and merging
    skill/
      loader.go                # Skill discovery and loading
    repl/
      repl.go                  # Interactive REPL
      completer.go             # Autocompletion for commands and skills
    output/
      stream.go                # Terminal output, syntax highlighting
  cmd/
    steiner-core-tools/
      main.go                  # Core tools binary (read, write, bash, glob, search)
      read.go
      write.go
      bash.go
      glob.go
      search.go
  configs/
    default.yaml               # Reference config with all options documented
  go.mod
  go.sum
  README.md
  LICENSE
```

## 7. Key Dependencies (Go)

| Dependency | Purpose |
|---|---|
| `github.com/spf13/cobra` | CLI framework |
| `github.com/spf13/viper` | Config loading (YAML, env vars) |
| `gopkg.in/yaml.v3` | YAML parsing |
| `github.com/alecthomas/chroma` | Syntax highlighting (pure Go) |
| `github.com/charmbracelet/lipgloss` | Terminal styling |
| `github.com/reeflective/readline` | Readline with history, keybindings, completion |

No CGo dependencies. The binary is fully statically linkable.

## 8. What v1 Does NOT Include

These are explicitly out of scope for v1 but the architecture accommodates them:

- **Container-based sandboxing.** The `ToolExecutor` interface is ready for a `ContainerExecutor` implementation. Not built in v1.
- **Non-OpenAI providers.** The `Provider` interface supports them. Only `openai_compat` ships in v1.
- **MCP server support.** The plugin interface is JSON-on-stdio, which is close to MCP stdio transport. Full MCP client support is a future addition.
- **Conversation persistence.** v1 is ephemeral - conversation history lives in memory and dies with the process. Persistence (SQLite, file-based) is a future feature.
- **Cost tracking / reporting.** Token usage is tracked for budget enforcement but not persisted or reported beyond the current session.
- **Multi-file edit transactions.** v1 edits files one at a time. Atomic multi-file commits (write A + write B as a unit) are a future feature.
- **Web/HTTP interface.** v1 is CLI-only.
- **Hierarchical AGENTS.md.** v1 loads AGENTS.md from the project root only. Subdirectory-level AGENTS.md discovery (injecting different conventions based on which files the agent is working with) is a future feature.
- **Project-local skills.** v1 supports global skills only (`~/.config/steiner/skills/`). Per-project skills (`.steiner/skills/`) are a future feature.

## 9. Success Criteria

v1 is shippable when:

1. The agent loop can complete a multi-step coding task (find bug, read files, edit, run tests) using a local LLM via Ollama.
2. The agent loop can complete the same task using a remote API (OpenRouter, OpenAI).
3. Sub-agent spawning works for task decomposition (orchestrator delegates a search task to a sub-agent, receives results, continues).
4. Tool approval prompts work correctly in REPL mode.
5. `--exec` mode with `--auto-approve` runs headlessly without blocking on input.
6. AGENTS.md loading and merging works correctly: global conventions are loaded, project conventions are appended, missing files are silently skipped.
7. Project context auto-discovery correctly includes README content in the system prompt.
8. Configuration hierarchy resolves correctly: defaults < global < project < env < CLI flags.
9. All three termination controls fire correctly when limits are exceeded.
10. A user-defined tool (external binary) can be added via YAML config and used by the agent without code changes.
11. Skills can be invoked via `/skill-name` in REPL and `--skill` in single-shot mode, injecting content into the conversation.
12. Autocompletion popup works for `/` commands, displaying labelled entries for built-in commands and skills.
13. Readline keybindings work: history navigation, reverse search, word operations, line editing.
14. Command history persists across REPL sessions.

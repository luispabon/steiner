# Steiner Gap Report and Milestone Decisions

> **Status**: Research record — documents product decisions for milestone planning.  
> No GitHub issues have been created from this report.  
> **Author**: Steiner maintainers  
> **Date**: 2026  
> **Baseline version**: Pre-v1.0.0 (current `main`)

---

## Executive summary

Steiner is a local-first, delegation-heavy Go coding agent with sandboxed execution,
provider-agnostic model configuration, and autonomous oneshot orchestration. It is
competitive with the current generation of terminal-native coding agents on its core
design — bounded sub-agent context, approval-gated structured tools, Linux sandboxing,
and worktree-isolated plan/implement/review loops.

This report catalogues competitor capabilities, documents Steiner's shipped baseline,
and records milestone decisions for future development. Milestones reflect product
discussion outcomes, not an objective market requirement count.

**Key findings:**

- **Strengths**: delegation as primary context strategy, structured file mutation,
  Bubblewrap sandbox, provider abstraction, oneshot mode, skills, execution modes.
- **v1.0.0 (confirmed additions)**: MCP client support, cross-platform sandbox
  safe-status UX.
- **v1.1.0**: project-defined prompt templates/commands, configurable TUI keybindings
  (rebinding existing actions only), session search/export/CLI management.
- **v2.0.0**: JSON/NDJSON headless output, LSP integration, mutation
  checkpoints/rewind, named execution profiles, per-project deny/ask/allow permission
  rules, lifecycle hooks, audit logging, configurable tool hotkeys, multi-session TUI
  tabs, native macOS/Windows sandbox backends. Git status/diff views are tentative.
- **v3.0.0**: notebook-aware read/mutation support, expanded prompt-cache analytics
  dashboard.
- **Dropped**: `.steiner/AGENTS.md` hierarchical overrides, session-scoped path grants.
- **Out of scope**: plugin marketplace, arbitrary custom agents, background agents,
  remote control, browser automation, IDE extensions, cloud tasks, agent teams, SDK,
  inline autocomplete, remote model serving/proxy.

---

## Already-present capabilities (2026 baseline)

These features ship in the current codebase and form the baseline.

### Core agent loop

| Feature | Status | Details |
|---------|--------|---------|
| Interactive TUI | Shipped | `/` commands, sidebar, overlays, mode badge, accent colour |
| `--exec` single-shot | Shipped | Non-interactive request-and-exit (`go run ./cmd/steiner --exec "..."`) |
| Plan / Build modes | Shipped | `plan` = read-only project; `build` = full mutation. Shift+Tab or `/mode` toggle |
| Approval-gated tools | Shipped | `mutate` and `bash` require user approval by default |
| Structured tool schemas | Shipped | All built-in tools use typed JSON input/output |
| Bounded sub-agents (7 types + follow_up) | Shipped | `explore`, `research`, `code`, `evaluate`, `sanity_check`, `review`, `vision` sub-agents plus `follow_up` continuation |
| Sub-agent context isolation | Shipped | Child conversation never enters parent context |
| Provider abstraction | Shipped | Same config shape for all providers |
| Context budgeting | Shipped | Per-source byte budgets cap each context source |
| Context compaction | Shipped | Automatic summarisation at ~70% context window |
| Skills | Shipped | Discoverable auxiliary context loaded from `skills/` |
| Persistent sessions | Shipped | Session saved to disk, resumable across restarts |
| Conversation forking | Shipped | `/fork` command — fork live or saved sessions |
| Image paste (Ctrl+V) | Shipped | Auto-resize, token accounting, vision sub-agent recall |

### Providers

| Feature | Status | Details |
|---------|--------|---------|
| Ollama | Shipped | `openai_compat` type at `http://localhost:11434/v1` |
| LM Studio | Shipped | Same `openai_compat` path, different base URL |
| Anthropic | Shipped | Native wire format with `cache_control` breakpoints |
| LiteLLM | Shipped | `openai_compat` wire with LiteLLM-specific 429 retry and budget-exhaustion handling |
| OpenAI | Shipped | Standard chat completions |
| Codex (Responses API) | Shipped | OAuth login, `session-id`/`thread-id` caching |
| OpenRouter | Shipped | `openai_compat` with routing |
| Generic OpenAI-compatible | Shipped | Any `base_url` |
| Gemini | Partial | Type defined in config (`gemini`) but no runtime factory implementation — use `openai_compat` with Gemini endpoint for now |
| Custom provider | Shipped | Configurable via `providers` block |

### Execution and sandboxing

| Feature | Status | Details |
|---------|--------|---------|
| Bubblewrap sandbox (Linux) | Shipped | Read-only root bind, env var allowlist, writable workspace |
| `--unsafe` mode | Shipped | Disables sandboxing entirely |
| Sandbox boundary prompts | Shipped | User prompted on writes outside workspace |
| Env var allowlist | Shipped | Credential-bearing vars blocked in sandbox |
| `host_mounts` config | Shipped | Additional writable paths for sandbox |
| `sandbox.enabled` toggle | Shipped | Config-level disable |
| Implicit sandbox fallback | Shipped | Sandbox bypassed when `bwrap` unavailable; no explicit OS detection |

### Oneshot mode

| Feature | Status | Details |
|---------|--------|---------|
| Plan / Implement / Review phases | Shipped | Three-phase autonomous pipeline |
| Git worktree isolation | Shipped | Each phase runs in dedicated worktree |
| Resumable runs | Shipped | `--resume` recovers interrupted runs |
| Manifest persistence | Shipped | JSON manifest tracks phase completion |
| Lock-based contention | Shipped | CAS lock prevents concurrent runs |
| Optional PR closeout | Shipped | `auto_pr` pushes branch and opens PR/MR |
| Phase-specific model overrides | Shipped | `models.profiles.<name>.oneshot.plan/implement/review` |

### Other shipped features

- Advisor (stronger-model steering pass)
- Desktop notifications (Linux)
- Cache hit rate tracking
- `cave_human` terse output mode
- 13 TUI accent colour presets with picker
- Web search (Google, Kagi, Brave, SearXNG backends — backend-configurable)
- Codex OAuth (browser login, token refresh)
- Self-update (dev/stable channels, checksum verification)
- `display_file` overlay tool
- `workflow_handoff` to transition between workflows
- Config file layers (`~/.config/steiner/`, `.steiner/config.yaml`, env vars, CLI flags)

---

## Feature matrix: Steiner vs. competitors

| Capability | Steiner (2026) | Codex CLI | Claude Code | OpenCode | Pi | Kilo Code |
|------------|:---:|:---:|:---:|:---:|:---:|:---:|
| **Architecture** | | | | | | |
| Local-first | Yes | Cloud+hybrid | Cloud | Local-first | Cloud | Local-first |
| Go implementation | Yes | Rust | TypeScript | TypeScript | TypeScript | TypeScript |
| Delegation model | Sub-agent tree | Prompt chain | Tool chain | Sub-agents | Tool chain | Tool chain |
| Structured tool schemas | Yes | Yes | Yes | Yes | Yes | Yes |
| Sandboxed execution | Linux Bubblewrap | Docker/host | Container | None | None | Host |
| MCP client | **Missing** | Built-in | Yes | Yes | Yes | Yes |
| **Context management** | | | | | | |
| Per-source byte budgets | Yes | – | – | – | – | – |
| Automatic compaction | Yes | Yes | Yes | Yes | – | Yes |
| Sub-agent isolation | Yes | – | – | – | – | – |
| Checkpoints/rewind | **Missing** | Yes | Yes | Yes | – | Yes |
| **Model & config** | | | | | | |
| Provider abstraction | 8 types (plus 1 partial) | Native | Native | 75+ providers | Anthropic only | OpenAI + OpenRouter |
| Named model profiles | **Missing** | Yes | Yes | – | – | Yes |
| In-session model switch | Yes (`/model`) | Yes | Yes | – | – | Yes |
| **Output & headless** | | | | | | |
| TUI | Yes | Yes | Yes | Yes | – | Yes |
| JSON/NDJSON output | **Missing** | Yes | Yes | – | – | Yes |
| **Sandbox & safety** | | | | | | |
| Linux sandbox | Yes (bwrap) | Docker | Container | – | – | – |
| macOS/Windows sandbox | **Missing** | Docker | Container | – | – | – |
| Deny/ask/allow rules | Approval only | **Missing** | **Missing** | Yes | – | **Missing** |
| Audit trail | **Missing** | **Missing** | **Missing** | **Missing** | – | **Missing** |
| **LSP integration** | **Missing** | Yes | Yes | Yes (experimental) | – | **Missing** |
| **Project instructions** | AGENTS.md (auto-loaded) | **Missing** | CLAUDE.md | SKILL.md | – | **Missing** |
| **Session management** | | | | | | |
| Persistent sessions | Yes | **Missing** | Yes | Yes | – | Yes |
| Session list/search/export | List (`--resume`), search/export **Missing** | Yes | Yes | Yes | – | **Missing** |
| Fork live session | Yes | Yes | Yes | Yes | – | **Missing** |
| **Oneshot/autonomous** | | | | | | |
| Plan/implement/review | Yes | **Missing** | **Missing** | Yes (Build/Plan) | – | **Missing** |
| Git worktree isolation | Yes | – | – | – | – | – |
| Resumable runs | Yes | – | – | – | – | – |
| **Other** | | | | | | |
| Image input | Yes | Yes | Yes | – | Yes | Yes |
| Web search (configurable) | Yes (4 backends) | Yes | Yes | – | – | Yes |
| Lifecycle hooks | **Missing** | – | Yes | Yes (plugins) | – | Yes |
| User templates/commands | **Missing** | Yes | Yes | – | – | Yes |
| Custom agents | **Missing** | **Missing** | Yes | Yes | – | **Missing** |
| IDE extension | **Missing** | VS Code | **Missing** | VS Code/Cursor/Windsurf/VSCodium | – | VS Code |
| Inline autocomplete | **Missing** | Yes | **Missing** | **Missing** | – | Yes |

Key: `Yes` = shipped; `–` = not applicable or unknown; **Missing** = gap identified.

### Competitor notes

**Codex CLI** (OpenAI, Rust, 2025+): OpenAI's terminal coding agent. Features deep GPT
integration, MCP client, named model profiles, checkpoints/rewind, JSON output, session
list/search/export, LSP, user templates. Cloud-dependent for best performance but runs
locally. Sandbox via Docker. Does not have autonomous oneshot phases or sub-agent
isolation.

**Claude Code** (Anthropic, TypeScript, 2025+): Anthropic's terminal agent. Strong
container sandbox, MCP client, lifecycle hooks, CLAUDE.md project instructions, session
management, checkpoints, LSP. Cloud-only. No delegation model comparable to Steiner's
sub-agents. Best-in-class for Anthropic model integration.

**OpenCode** ([anomalyco/opencode](https://github.com/anomalyco/opencode), MIT, TypeScript): Active
terminal-native coding agent with TUI, desktop app (BETA), web UI, IDE extensions
(VS Code, Cursor, Windsurf, VSCodium), 75+ provider support, MCP local/remote OAuth,
npm/local plugins with hooks and custom tools, SKILL.md project instructions, granular
wildcard allow/ask/deny permissions, headless `opencode run`, `opencode serve`
(OpenAPI 3.1), JS/TS SDK, session CRUD/fork/share/undo-redo/compaction, LSP diagnostics
(experimental), and Build/Plan agents with General/Explore/Scout subagents. No built-in
sandbox isolation beyond permission controls. No autonomous plan/implement/review pipeline.

**Pi** (2025+): Cloud reasoning agent with natural-language-first interaction. Strong
structured output, MCP client, image input. No local execution, no sandbox, no
autonomous modes. Least overlapping feature set with Steiner.

**Kilo Code** (TypeScript, 2025+): VS Code extension with terminal/headless modes. MCP
client, model profiles, checkpoints, lifecycle hooks, JSON output, user templates.
Sandbox-limited (host execution). Best agent-experience integration of the group.
No autonomous oneshot or sub-agent tree.

---

## Milestone decisions

Each entry represents a confirmed product decision about when a feature is targeted,
deferred, or dropped. Milestones beyond v1.0.0 are candidates — they may be
reprioritised as the project evolves.

### v1.0.0 (confirmed)

The v1.0.0 roadmap contains exactly these two additions alongside the already-shipped
baseline. This is the outcome of product discussion, not an objective market requirement
count.

#### 1. MCP client support

**Rationale**: The Model Context Protocol (MCP) is the de facto standard for connecting
coding agents to external tools (databases, APIs, filesystems, package registries).
Every major competitor includes an MCP client. Without it, Steiner cannot integrate
with the growing MCP ecosystem of servers.

**Scope**:
- Support `stdio` transport (subprocess with stdin/stdout JSON-RPC)
- Support Streamable HTTP transport (SSE-based)
- Tool discovery via `tools/list`, invocation via `tools/call`
- MCP tool definitions mapped to Steiner's `ToolDef` schema
- Configurable MCP server definitions in `.steiner/config.yaml`
- Per-server approval mode (auto/allow/deny) matching Steiner's existing permission model
- Error handling: server crashes, timeouts, malformed responses
- Not in scope: MCP server SDK, registry, or marketplace

**Design considerations**:
- MCP tools should appear alongside built-in tools in the registry, marked as external.
- Sub-agent tool allowlists should be able to include or exclude MCP tools.
- MCP servers configured via a new `mcp` config block.
- Consider session lifecycle: start MCP servers on session start, terminate on session end.

---

#### 2. Cross-platform sandbox safe-status UX

**Rationale**: Sandboxing is Linux-only (Bubblewrap). On macOS and Windows, `bash` runs
without any sandbox wrapper. For v1.0.0, expose sandbox state and warnings so users
understand their safety level. Native sandbox backends for macOS and Windows are
deferred to v2.0.0.

**Scope (v1.0.0)**:
- Expose sandbox configuration and status in `steiner config` output
- Display active/unavailable/bypassed sandbox state in the TUI (sidebar, mode badge)
- Surface warnings when sandbox is unavailable or bypassed
- Document per-platform sandbox behaviour and limitations
- Add `sandbox.warning_on_unsupported_platform` toggle (default: true)
- Safe fallback UX when sandbox is unavailable

**Not in scope (v1.0.0)** — deferred to v2.0.0:
- macOS `sandbox-exec` support
- Windows sandbox support (WHP / job objects)

---

### v1.1.0

#### 1. Project-defined prompt templates or commands

**Rationale**: Users want to define reusable prompt templates or custom commands
in `.steiner/config.yaml` (e.g., `/test` runs "Run tests and report failures").
Competitors support this pattern.

**Scope**:
- `commands` config block mapping custom names to prompt templates
- `/run <name>` or `/ <name>` invokes the template
- Template variables: `{{workspace}}`, `{{files}}`, `{{branch}}`, `{{selection}}`
- Templates support multi-line content with parameter interpolation
- Examples: `test`, `lint`, `deploy`, `review-last-commit`

#### 2. Configurable TUI keybindings

**Rationale**: Keybindings for existing actions (sidebar toggle, mode switch, session
controls, overlays, navigation) should be user-configurable.

**Scope**:
- Config block for keybinding overrides
- Rebinding existing actions only — no new action types
- Keybinding schema documents default bindings and allowed overrides

#### 3. Session search, export, and CLI management

**Rationale**: Session listing is available via `steiner --resume` (no argument) and
deletion is implemented in the session store, but there is no dedicated CLI for
managing sessions. Users cannot search session content, export to a file, or
prune stale sessions from the command line.

**Scope**:
- `steiner session search <query>` — search session content
- `steiner session export <id> <format>` — export to JSON, Markdown, or plain text
- `steiner session prune` — delete sessions older than N days
- `/sessions` TUI overlay or sidebar panel

---

### v2.0.0

1. **JSON/NDJSON headless output** — structured event output for CI/CD, scripting, and
   headless automation. `--output json` and `--output ndjson` flags. Compatible with
   `--exec` and oneshot modes. Include event type, timing metadata. Suppress TUI in
   structured mode.

2. **Optional LSP support** — LSP-backed `definitions`, `references`, and `diagnostics`
   tools. Language server lifecycle managed by Steiner (start on demand, idle timeout).
   Caching per session. Steiner must work without any language server installed.

3. **Mutation checkpoints and rewind** — automatic snapshot before each `mutate` call.
   `rewind` tool or `/rewind` command. Checkpoints stored under `.steiner/checkpoints/`
   (not git, to avoid polluting user history). Available only in `build` mode. Cleanup
   on session close.

4. **Named execution profiles** — a `profiles` config block mapping names to model +
   provider + parameter overrides. `/profile <name>` command to switch in-session.
   `--profile <name>` flag for headless runs. Profile switching invalidates prompt
   cache.

5. **Per-project deny/ask/allow permission rules** — a `permissions` config block with
   rules per tool or tool category. Rule types: `deny`, `ask`, `allow`. Matching by path
   glob, command pattern, or tool name. First match wins with a default action. Existing
   `approval_mode` remains as fallback.

6. **Lifecycle hooks** — configurable hooks for session and tool events
   (`pre_tool`, `post_tool`, `pre_model`, `post_model`, `session_start`, `session_end`,
   `on_error`). Actions: shell command, HTTP webhook, file append. Synchronous by
   default with `async: true` option. Configurable `fail_open`.

7. **Local audit logging** — append-only JSON Lines audit log at `.steiner/audit.log`.
   Entries: timestamp, tool name, parameters (sanitised), user decision, duration, exit
   status. Configurable rotation. `steiner audit` command to query/tail.

8. **Configurable tool hotkeys** — user-configurable hotkeys for commands and tools,
   building on the keybinding system introduced in v1.1.0.

9. **Multi-session TUI tabs** — session management UI improvement for the TUI.

10. **Native macOS sandbox backend** — `sandbox-exec` based sandbox profile with
    file-read/write isolation. Deny network access (configurable). Less capable than
    Bubblewrap; document limitations clearly.

11. **Native Windows sandbox backend** — Windows sandbox support (WHP / job objects
    or WSL2 recommendation).

**Tentative** (not a committed roadmap item):
- Git working-tree status and diff views — dedicated `git_status` and `git_diff` tools
  for structured, token-efficient git inspection. Respects execution mode (available in
  both `plan` and `build`). Output structured as JSON for the model, rendered as text
  for the user.

---

### v3.0.0

1. **Notebook-aware read and mutation support** — `read` tool detects `.ipynb` files and
   renders cell-by-cell output. `mutate` tool supports cell-level operations (insert,
   delete, edit source, clear output). Not in scope: notebook execution, kernel
   management, output streaming.

2. **Expanded prompt-cache analytics dashboard** — deeper observability into cache hit
   rates, prefix composition, and invalidation events.

---

### Dropped (no replacement)

These proposals were considered during the research phase and are dropped with no
replacement roadmap issue:

- **`.steiner/AGENTS.md` hierarchical overrides**: Current auto-loading of
  `~/.config/steiner/AGENTS.md` (global) and `<workspace>/AGENTS.md` (project) is
  sufficient. The AGENTS.md loading rules already match or exceed competitor behaviour
  (CLAUDE.md, SKILL.md). A `.steiner/AGENTS.md` override would add complexity with
  unclear marginal benefit.
- **Session-scoped path grants**: The existing `host_mounts` config and sandbox boundary
  prompts cover the use case. A session-scoped `/grant` command would duplicate
  existing mechanism without solving a distinct user need.

---

### Out of scope

The following capabilities are not planned for any milestone. They may be reconsidered
in the long term but are excluded from the current roadmap.

| Feature | Rationale |
|---------|-----------|
| Plugin marketplace | Requires registry, signing, distribution infrastructure. Breaks local-first principle. |
| Arbitrary custom agents | Undermines Steiner's bounded, purpose-built sub-agent design. |
| Background agents / daemon mode | Adds process lifecycle complexity, resource monitoring, IPC. |
| Remote control (SSH/HTTP API) | Expands attack surface, requires authn/authz, TLS. |
| Browser automation | Adds a full browser runtime dependency (Playwright/Puppeteer). |
| IDE extensions (VS Code, JetBrains) | Requires separate SDK, extension API knowledge, per-platform builds. |
| Cloud task orchestration | Out of scope for a local-first tool. |
| Agent teams / multi-agent planning | Requires agent communication protocol, shared state, consensus. |
| SDK / library API (embedding Steiner) | Would require public API surface, semantic versioning, compatibility docs. |
| Inline autocomplete | Entirely different product category (IDE autocomplete vs. agent). |
| Remote model serving / proxy | Infrastructure operation concern, not an agent feature. |

---

## Design recommendations

### 1. Prefix stability for prompt caching

All changes that touch the system preamble or tool definitions must preserve
prefix stability per the existing rules:

- Static sources (preamble, agents, project context, skills) before dynamic sources.
- Tool definition ordering must be deterministic.
- `CachedSystemPreamble` memoization must account for new content (MCP tool schemas,
  project instructions).

### 2. Tool schemas for MCP

MCP tools are discovered at runtime — their schemas cannot be statically defined.
The approach should be:

1. Load MCP server config at session start.
2. Call `tools/list` on each server to discover tools.
3. Convert each MCP tool to a Steiner `ToolDef` with:
   - Name prefixed (e.g., `mcp_<server>_<tool_name>`)
   - Input schema mapped from the MCP `inputSchema`
   - Approval mode from the MCP server's config or the tool's explicit rule
4. Register these tools in the main registry alongside built-in tools.

### 3. Checkpoint storage strategy

Rather than committing to git (which pollutes the user's history), use a directory
under `.steiner/checkpoints/`:

```
.steiner/checkpoints/
├── checkpoint-001/
│   ├── state.json          # tool name, params, timestamp
│   └── snapshot/           # file copies before mutation
├── checkpoint-002/
...
```

Rewind restores files from the snapshot and removes checkpoints after the target.

### 4. JSON/NDJSON output from `internal/output`

The existing `internal/output` package already has event streaming
(`internal/output/event.go`). Add a `json.NewEncoder` and `ndjson.NewEncoder`
alongside the terminal renderer. The selection mechanism can be a simple
`OutputFormat` field in `config.Output`:

```
type OutputConfig struct {
    Format string  // "terminal", "json", "ndjson"
}
```

The `--output` flag maps to this field. When not "terminal", suppress TUI and emit
structured events only.

### 5. Sandbox on macOS (v2.0.0+)

macOS sandbox-exec is limited but provides basic file-read/write isolation. A minimal
sandbox profile could:

- Allow read access to the workspace and system paths
- Allow write access only to the workspace
- Deny network access (optional, controllable)
- Deny process spawning outside the sandbox

This is less capable than Bubblewrap but better than no sandbox at all. Document
the limitations clearly.

---

## Sources and assumptions

### Local paths consulted

| Path | What it provides |
|------|------------------|
| `README.md` | Feature list, quickstart, tools table, configuration overview, optional features |
| `AGENTS.md` | Architecture constraints, work loop, Go conventions, documentation maintenance rules |
| `CLAUDE.md` | Duplicate of AGENTS.md (Claude-compatible project instructions) |
| `docs/configuration.md` | Full config field reference, provider types, model definitions |
| `docs/execution-modes.md` | Plan/build mode enforcement matrix |
| `docs/sub-agent-delegation.md` | Sub-agent tool descriptions, allowlists, safety rules |
| `docs/sub-agent-delegation-internals.md` | Conditional registration, child bootstrapping architecture |
| `docs/tool-sandboxing.md` | Sandbox mount layout, env var allowlist, platform support |
| `docs/context-management.md` | Delegation, budgets, compaction description |
| `docs/oneshot.md` | Oneshot invocation, configuration, resume behaviour |
| `docs/optional-features.md` | Web search, image paste, forking, Codex OAuth, cave_human |
| `internal/tool/registry.go` | Tool registry structure, definition fields |
| `internal/delegation/` | Sub-agent implementation, registration and handler deps |

### External references

- **MCP Specification**: https://spec.modelcontextprotocol.io (2025 specification
  for stdio and Streamable HTTP transports)
- **Codex CLI**: https://github.com/openai/codex (OpenAI's terminal agent, Rust)
- **Claude Code**: https://docs.anthropic.com/en/docs/claude-code/overview
  (Anthropic's terminal agent, TypeScript)
- **OpenCode**: https://github.com/anomalyco/opencode (Active terminal coding
  agent, MIT, TypeScript)
- **OpenCode docs**: https://opencode.ai/docs (Official docs: agents, MCP, plugins,
  permissions, LSP, CLI, server, IDE, skills)
- **Kilo Code**: https://github.com/kilocode/kilocode (VS Code extension with
  headless mode, TypeScript)
- **Pi**: https://pi.ai (Cloud reasoning agent)
- **Bubblewrap**: https://github.com/containers/bubblewrap (Linux sandboxing tool)
- **sandbox-exec (macOS)**: https://developer.apple.com/library/archive/documentation/Security/Conceptual/AppSandboxDesignGuide/AboutAppSandbox/AboutAppSandbox.html

### Assumptions

1. The v1.0.0 release targets the confirmed additions (MCP client, sandbox safe-status UX).
   Deeper integration (e.g., full LSP workspace editing, MCP server SDK) is deferred to
   later milestones.
2. The existing provider abstraction layer can accommodate MCP server communication
   without core loop changes.
3. Steiner's user base is comfortable with `git`-based workflows (the checkpoint and
   rewind design assumes `git` literacy).
4. Bubblewrap remains the primary sandbox on Linux; macOS and Windows get best-effort
   sandboxing in v2.0.0.
5. `fetch_url` is always available; `web_search` requires a configured search backend
   (Google, Kagi, Brave, SearXNG).
6. Effort estimates (S/M/L/XL) referenced in earlier draft sections are relative and
   assume a single experienced Go developer with codebase familiarity. S = hours,
   M = days, L = 1-2 weeks, XL = 3+ weeks.
7. No changes to the core agent-scheduler loop are required for any of the proposed
   features except lifecycle hooks (which add notification points).
8. Steiner's local-first design is preserved: all features work without internet, cloud
   accounts, or third-party services (except MCP servers and web search, which are
   inherently networked but self-hostable).

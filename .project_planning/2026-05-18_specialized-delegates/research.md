# Specialized Sub-Agent Patterns in Open Source Coding Agents

## Question
What specialized sub-agent patterns exist in open source coding agents, and what can steiner adopt?

---

## Findings

### OpenCode (github.com/opencode-ai/opencode) — Go

OpenCode is the closest structural peer to steiner. It defines four named agent types as string constants in `internal/config/config.go`:

```go
type AgentName string

const (
    AgentCoder      AgentName = "coder"
    AgentSummarizer AgentName = "summarizer"
    AgentTask       AgentName = "task"
    AgentTitle      AgentName = "title"
)
```

**Tool scoping (explicit allowlists per agent type).**
`internal/llm/agent/tools.go` defines two separate tool constructors:

- `CoderAgentTools(...)` — full set: bash, edit, fetch, glob, grep, ls, sourcegraph, view, patch, write, plus MCP tools, LSP diagnostics, and the `AgentTool` (which spawns a task sub-agent).
- `TaskAgentTools(...)` — read-only, narrow set: glob, grep, ls, sourcegraph, view. No bash, no write, no edit.

The `AgentTool` (in `agent-tool.go`) is how the coder agent spawns a task sub-agent: it calls `NewAgent(config.AgentTask, ...)` with `TaskAgentTools(...)`. The task sub-agent thus runs with a restricted read-only tool surface.

**System prompts per agent.** `internal/llm/prompt/` has separate files per agent type:
- `coder.go` — long system prompt with two variants (Anthropic vs OpenAI base), environment info, LSP information. Role: "interactive coding assistant."
- `task.go` — short, terse prompt: "concise, direct, to the point, since your responses will be displayed on a command line interface. One word answers are best."
- Title/summarizer prompts are similarly purpose-built.

The function `GetAgentPrompt(agentName, provider)` switches on agent name to return the right prompt.

**Model selection per agent type.**
`createAgentProvider(agentName)` reads `cfg.Agents[agentName]` to get the model and max-token config. Defaults are set by provider (examples):

| Agent      | OpenAI default   | OpenRouter default            | Max tokens |
|------------|-----------------|-------------------------------|------------|
| coder      | GPT-4.1          | claude-3.7-sonnet             | 5000       |
| task       | GPT-4.1-mini     | claude-3.7-sonnet             | 5000       |
| title      | GPT-4.1-mini     | claude-3.5-haiku              | 80         |
| summarizer | (same as coder)  | —                             | 5000       |

Reasoning (extended thinking) is only enabled for `AgentCoder` on Anthropic, not for task or title agents.

**Result formatting.** The task agent returns its last response text to the parent via `tools.NewTextResponse(response.Content().String())`. The parent agent's cost counter is incremented by the child's cost. No structured JSON schema — plain text bubbles up as a tool result.

**Token efficiency.** Title agent is capped at 80 max tokens. Task agent gets a weaker/cheaper model. Task agent's tool set is read-only and smaller, which reduces both token use and risk surface.

---

### Aider (github.com/paul-gauthier/aider) — Python

Aider's specialization is expressed through **edit-format-keyed coder classes** rather than a parent/child agent tree. Each `Coder` subclass owns a distinct `edit_format` string and a distinct `gpt_prompts` object.

**Coder types (from `aider/coders/__init__.py`):**

| Class | edit_format | Purpose |
|---|---|---|
| `AskCoder` | `ask` | Answer questions, no file edits |
| `ContextCoder` | `context` | Identify files to modify |
| `ArchitectCoder` | `architect` | High-level design directions, delegates edits to an editor coder |
| `EditBlockCoder` | `diff` | Search/replace block edits |
| `WholeFileCoder` | `whole` | Whole-file rewrites |
| `UnifiedDiffCoder` | `udiff` | Unified diff format |
| `PatchCoder` | `patch` | Patch format |
| `HelpCoder` | `help` | Documentation questions |
| `EditorEditBlockCoder` | `editor-diff` | Pure editor, no map, no shell |
| `EditorWholeFileCoder` | `editor-whole` | Pure editor variant |

**System prompts per coder.** Each class has a `gpt_prompts` object with a distinct `main_system`:
- `AskPrompts.main_system`: "Act as an expert code analyst. Answer questions... If you need to describe code changes, do so briefly."
- `ArchitectPrompts.main_system`: "Act as an expert architect engineer and provide direction to your editor engineer. Describe how to modify the code... The editor engineer will rely solely on your instructions."
- `ContextPrompts.main_system`: "Understand the user's question to determine ALL the existing source files which will need to be modified."

**Delegation (architect → editor).** `ArchitectCoder.reply_completed()` implements a two-pass pattern:
1. Architect coder generates a description of required changes (no file edits, no repo-map tokens, no shell suggestions).
2. On completion, it reads `main_model.editor_model` (configurable; falls back to same model) and `main_model.editor_edit_format` to select which editor coder class to use.
3. It instantiates the editor coder (`Coder.create(**new_kwargs)`) with `map_tokens=0`, `cache_prompts=False`, `suggest_shell_commands=False` — stripping everything not needed for mechanical editing.
4. The editor coder runs on the architect's output text.
5. The architect then calls `move_back_cur_messages()` to reframe the conversation.

**Model selection.** `main_model.editor_model` and `main_model.editor_edit_format` are per-model metadata, not global config. Different strong models configure different cheaper editors (e.g., o1 architect → GPT-4o editor).

**Tool scoping.** Not tool-call based; scoping is done by stripping prompt features. The editor coder gets `map_tokens=0` (no repo map), no shell suggestions, no cache warming — all token-heavy context is dropped for the execution pass.

**Result formatting.** No explicit parent/child protocol. The architect reasserts "I made those changes to the files." as the last message, then propagates `total_cost` and `aider_commit_hashes` back from the child.

---

### Goose (github.com/block/goose) — Rust

Goose has the most complete and explicit sub-agent architecture of any project surveyed.

**Sub-agent launch path.** The main agent can spawn a subagent via `run_subagent_task(SubagentRunParams)` in `agents/subagent_handler.rs`. The `SubagentRunParams` carries:
- `config: AgentConfig` — same config type as the parent (mode, permissions, scheduler).
- `recipe: Recipe` — task instructions, prompt, response schema, activities, retry config.
- `task_config: TaskConfig` — provider, parent session id, working dir, extension list, max turns.
- `return_last_only: bool` — whether to return only the last message or the full conversation.

**Tool scoping via extension lists.** `TaskConfig` carries `extensions: Vec<ExtensionConfig>`. The subagent starts with an empty extension list, then each extension is added via `agent.add_extension(extension, session_id)`. This means the caller explicitly controls which tool namespaces (MCP servers, built-ins) the subagent can see. No allowlist/denylist — it is a positive-only provisioning model.

**`GooseMode` controls approval flow.**
```rust
pub enum GooseMode {
    Auto,         // approve all tool calls automatically
    Approve,      // ask before every tool call
    SmartApprove, // ask only for sensitive tool calls
    Chat,         // no tool calls at all
}
```
Subagents inherit the mode from `AgentConfig`. The `permission_judge.md` prompt (a small single-purpose model invocation) classifies tool operations as read-only or not.

**System prompt construction.** `build_subagent_prompt()` renders `subagent_system.md` (a Minijinja template) with:
```rust
SubagentPromptContext {
    max_turns,
    subagent_id,
    task_instructions,   // from Recipe.instructions
    tool_count,
    available_tools,     // comma list of visible tool names
}
```
The template (`prompts/subagent_system.md`) opens: "You are a specialized subagent within the goose AI framework... You were spawned by the main goose agent to handle a specific task efficiently." It includes the task instructions, tool list, and turn budget in its context.

**Structured output via `recipe__final_output` tool.** When a `Recipe` carries a `response.json_schema`, the subagent is given a special tool `recipe__final_output` that validates the agent's final answer against the JSON schema before accepting it. If validation fails, the model gets specific error feedback and must retry. The tool carries `ToolAnnotations` marking it read-only, non-destructive, and idempotent.

**Result formatting.**
- If `response.json_schema` is set, the final output is extracted from the `FinalOutputTool` — a validated JSON string.
- Otherwise, `extract_response_text()` collects all assistant text and tool result text from the conversation.
- `return_last_only` further limits to just the last message when the caller only needs a summary.

**Turn budget.** Default is `GOOSE_SUBAGENT_MAX_TURNS = 25`, configurable via `GOOSE_SUBAGENT_MAX_TURNS` env var. The subagent gets a hard ceiling enforced by `SessionConfig.max_turns`.

**Specialized prompt templates (full list from `prompt_template.rs`):**
- `system.md` — main agent personality
- `subagent_system.md` — subagent behavior
- `compaction.md` — context summarization
- `permission_judge.md` — read-only classification
- `plan.md` — step-by-step planning (CLI only)
- `tiny_model_system.md` — shell command emulation for small local models
- `session_name.md` — short session name generation

Each template is a specialized agent role running in the same framework.

---

### Codex (github.com/openai/codex) — Rust

Codex does not define named "delegate types" in the same sense, but it does define several specialized internal sub-agent roles tracked through the `SubAgentSource` enum in `codex-rs/protocol/src/protocol.rs`:

```rust
pub enum SubAgentSource {
    Review,                                       // guardian approvals reviewer
    Compact,                                      // conversation compaction
    ThreadSpawn {
        parent_thread_id: ThreadId,
        depth: i32,
        agent_path: Option<AgentPath>,
        agent_nickname: Option<String>,
        agent_role: Option<String>,               // free-form role label
    },
    MemoryConsolidation,
    Other(String),
}
```

These values appear in HTTP headers sent to the OpenAI Responses API, so the backend can route/monitor by subagent type.

**Guardian subagent (`Review`).** The most interesting specialization is `ApprovalsReviewer`:
```rust
pub enum ApprovalsReviewer {
    User,                    // default: prompt the user
    AutoReview,              // guardian_subagent: invoke a sub-agent
}
```
When `ApprovalsReviewer::AutoReview` is configured, Codex invokes a "carefully prompted subagent" that "gathers relevant context and applies a risk-based decision framework before approving or denying the request." This is an approval/safety agent — not a coding agent.

**Compact subagent.** The `Compact` source is used when Codex calls the `/responses/compact` endpoint to summarize conversation history. This is treated as a distinct sub-agent invocation for telemetry purposes.

**`ThreadSpawn` with `agent_role`.** The `ThreadSpawn` variant carries an optional `agent_role` string, allowing future or external integrations to label sub-agents by type. This is extensible but not yet used to drive prompt or tool differentiation within the core.

**Tool scoping.** Tool scoping is not per-agent-type in Codex. It is controlled per turn by `AskForApproval` policy and `PermissionProfile` / `SandboxPolicy`. The same tool set is available to all agents; the sandbox restricts execution at the OS level.

**No separate per-agent system prompts.** Codex passes `base_instructions.text` through the request to the Responses API. The guardian sub-agent's distinct behavior comes from how the API routes the `guardian_subagent` header value, not from a locally crafted system prompt.

---

### Claude Code (github.com/anthropics/claude-code)

The Claude Code repository is not fully public (source returns 404 for raw file access). From the public documentation and observable behavior:

- Claude Code uses a `Task` tool that spawns a sub-agent with an isolated conversation. The sub-agent receives a prompt string and returns a result string.
- Tool access for the spawned sub-agent is inherited from the parent by default; there is no published allowlist mechanism at the source level.
- The pattern is consistent with the "task tool" model seen in OpenCode and Goose.

No source-level findings were possible for this project.

---

## Implications

These are the patterns most directly applicable to a Go-based agent adding specialized delegates:

**1. Named agent types with per-type tool allowlists (OpenCode model).**
The cleanest Go pattern: define an `AgentName` string type, a switch in a `ToolsFor(name AgentName)` function, and separate system prompt functions per name. The delegate tool creates the sub-agent with the restricted tool set. No delegation framework needed — just constructor composition.

**2. Strict tool reduction for read-only delegates (OpenCode + Aider).**
Task/explorer agents should get only read-only tools (glob, grep, ls, view/read). The coder agent gets mutation tools. This is an explicit allowlist, not a denylist. Aider's approach of stripping prompt features (repo map tokens, shell suggestions) is also worth noting — token reduction and risk reduction are the same action.

**3. Separate system prompts per agent role (all projects).**
Every project uses distinct system prompts per agent type. Prompts should be purpose-built: the task agent gets a brevity-first prompt; the coder agent gets a thoroughness-first prompt. Goose's template approach (Minijinja with injected tool list and turn budget) is the most flexible.

**4. Structured output via a sentinel tool (Goose).**
For delegates that produce structured results (e.g., a planning delegate returning a JSON plan), Goose's `recipe__final_output` pattern is the right model: inject a special tool whose schema matches the expected output structure, validate the response, and surface validation errors so the model can self-correct. This avoids brittle text parsing.

**5. Turn budget enforcement (Goose).**
Sub-agents should receive a hard turn ceiling (`MaxTurns`), separate from the parent's budget. Default 25 turns, configurable. This prevents runaway sub-agents from consuming the parent's context window.

**6. Model downsizing for cheap delegates (OpenCode + Aider).**
Title, summarizer, and exploration agents should use cheaper/faster models (e.g., haiku, mini) with low max-token caps. Reasoning/extended-thinking should be disabled for these agents.

**7. Cost aggregation (OpenCode).**
The sub-agent's cost should be propagated back to the parent session so totals are accurate. OpenCode adds `updatedSession.Cost` to `parentSession.Cost` after the sub-agent completes.

**8. Sub-agent source tagging (Codex).**
If steiner ever reports telemetry or logs, tagging which sub-agent type produced each request (via a header or log field) is valuable for debugging. Codex's `SubAgentSource` enum is a simple model.

---

## Risks and Uncertainties

- **Claude Code source unavailable.** The most directly comparable product (same underlying model) could not be examined at source level. Its tool-scoping approach for sub-agents is unknown.
- **Goose's extension model may not map cleanly to steiner.** Goose provisions tools via MCP extensions per sub-agent. Steiner uses a built-in tool registry. The equivalent is passing a filtered `[]tools.BaseTool` slice to the sub-agent constructor — simpler but less dynamic.
- **Aider's "coder type" pattern is not a parent/child pattern.** Aider switches between coder modes in a single session, not a nested invocation. The sub-agent delegation (architect → editor) is one specific case, not the general case. Conflating the two patterns could lead to over-engineering.
- **Structured output via a sentinel tool adds latency.** The `recipe__final_output` pattern requires an extra model turn when validation fails. For simple string-returning delegates this is unnecessary overhead.
- **Model selection per agent type requires multi-provider config.** If the delegate uses a different model family (e.g., haiku for title, sonnet for coder), the provider client must be re-instantiated per agent type. OpenCode handles this with `createAgentProvider(agentName)`. Steiner would need the same.

---

## Sources

| Project | Files Examined |
|---------|---------------|
| OpenCode | `internal/config/config.go`, `internal/llm/agent/agent.go`, `internal/llm/agent/tools.go`, `internal/llm/agent/agent-tool.go`, `internal/llm/prompt/coder.go`, `internal/llm/prompt/task.go`, `internal/llm/prompt/prompt.go` |
| Aider | `aider/coders/__init__.py`, `aider/coders/architect_coder.py`, `aider/coders/ask_coder.py`, `aider/coders/context_coder.py`, `aider/coders/editor_editblock_coder.py`, `aider/coders/architect_prompts.py`, `aider/coders/ask_prompts.py`, `aider/coders/context_prompts.py` |
| Goose | `crates/goose/src/agents/subagent_handler.rs`, `crates/goose/src/agents/subagent_task_config.rs`, `crates/goose/src/agents/agent.rs`, `crates/goose/src/agents/final_output_tool.rs`, `crates/goose/src/config/goose_mode.rs`, `crates/goose/src/prompt_template.rs`, `crates/goose/src/prompts/subagent_system.md`, `crates/goose/src/prompts/system.md`, `crates/goose/src/prompts/permission_judge.md` |
| Codex | `codex-rs/core/src/client.rs`, `codex-rs/core/src/safety.rs`, `codex-rs/core/src/lib.rs`, `codex-rs/core/src/client_common.rs`, `codex-rs/protocol/src/protocol.rs`, `codex-rs/protocol/src/config_types.rs`, `codex-rs/config/src/types.rs` |
| Claude Code | GitHub repo (source not publicly accessible) |

URLs:
- https://github.com/opencode-ai/opencode
- https://github.com/openai/codex
- https://github.com/anthropics/claude-code
- https://github.com/paul-gauthier/aider
- https://github.com/block/goose

---

## Open Questions

1. **What should the steiner delegation contract look like?** Should it be a `DelegateType` string constant (OpenCode model) or a richer config struct with tool list, system prompt, model, and turn budget all bundled (Goose model)? The OpenCode model is simpler and Go-idiomatic.

2. **Should delegates share the parent's provider or get their own?** OpenCode creates a fresh provider per agent type via `createAgentProvider`. This allows per-delegate model selection but increases startup cost. If steiner's delegates always use the same model, a shared provider with a swapped system prompt is cheaper.

3. **How should structured delegate output be returned?** The sentinel-tool pattern (Goose) is robust but complex. A simpler option: require delegates to emit a structured JSON block in their last message and parse it in the parent. Steiner already has `tool_summary` and `scratchpad` patterns that could extend this.

4. **Should the explorer/planner delegate be read-only at the OS level or only at the tool level?** OpenCode restricts at the tool level (no bash, no write tool). Codex restricts at the OS level (sandbox). For steiner, tool-level restriction is simpler and already implemented via the tool registry.

5. **How does the delegate's context budget relate to the parent's?** If steiner delegates to a sub-agent during a large parent conversation, the sub-agent needs its own context window budget, not a slice of the parent's. This needs explicit design when compaction is in play.

6. **Should steiner expose delegate types via config (like OpenCode's `agents` block) or hardcode them?** Config-driven lets users swap models per delegate without rebuilding, but adds validation surface. Hardcoded types with optional overrides is a reasonable first step.

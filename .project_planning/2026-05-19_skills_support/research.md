# Skills / Extensibility Systems in Open-Source Coding Agents

Research date: 2026-05-19

---

## Question

How do open-source coding agents implement "skills", "custom instructions", or "slash-command" extensibility?
Specifically: storage paths, discovery, invocation, prompt injection, persistence, skill catalogs, file format, and UI autocomplete.

---

## Findings

### 1. OpenCode (github.com/sst/opencode)

OpenCode has the richest skills-adjacent system of the agents surveyed, with three distinct extensibility layers:

#### 1a. Custom Commands

**Storage**

- User-scoped: `$XDG_CONFIG_HOME/opencode/commands/` (typically `~/.config/opencode/commands/`) or `$HOME/.opencode/commands/`
- Project-scoped: `<project>/.opencode/commands/`

**Discovery**

Commands are discovered by globbing `*.md` files under the above directories. The filename (without extension) becomes the command ID, prefixed by scope: `user:prime-context` or `project:deploy`.  See `packages/opencode/src/config/command.ts`.

**File format**

Plain Markdown. Frontmatter supported (via `gray-matter`). The body is the command template. `@file` syntax in the body expands to file contents, and `` !`shell` `` syntax runs a shell snippet inline. The Config schema is:

```
command: Record<string, ConfigCommand.Info>
```

**Invocation**

The TUI has a command dialog (`Ctrl+K`). Typing `/` in the input may autocomplete command names. On selection, the command body (after template expansion) replaces or prepends the user's input for the next turn.

**Context injection**

Command body is inserted as the user message for that turn. It is not a permanent system-prompt addition — it applies only to the invoked turn.

**Persistence**

Single-turn. The expanded command text is sent as the user message. No persistence across subsequent turns.

#### 1b. Instructions (always-on system context)

**Config field**

`instructions` is an array of strings in `opencode.json(c)` or `opencode.jsonc`. Each string is a path to a Markdown file or an inline text snippet.

**Discovery / loading**

Config is loaded in priority order:
1. Remote `.well-known/opencode` URLs (optional, team-shared)
2. Global config at `~/.config/opencode/opencode.jsonc`
3. Project config files found by walking up from the working directory looking for `.opencode/opencode.jsonc`
4. `OPENCODE_CONFIG` env override

The `instructions` array from all merged configs is **concatenated** (deduped via Set), not overwritten, so project and global instructions both apply. See `config.ts:mergeConfigConcatArrays`.

**Injection**

Instructions content is assembled into the `SystemPrompt` stage of the pipeline (stage 5 in the prompt processing pipeline). They are injected as part of the persistent system message that accompanies every LLM turn.

**Persistence**

Permanent for the session — instructions live in the system prompt and are present on every turn.

#### 1c. Agents (`.opencode/agents/*.md`)

**Storage**

Markdown files under `.opencode/agents/` in any config directory (global or project).

**Discovery**

Config agent loader globs `*.md` from those directories. The stem of the filename becomes the agent identifier. See `packages/opencode/src/config/agent.ts:110-140`.

**File format**

YAML frontmatter + Markdown body. Frontmatter fields:

- `model`: override model/provider
- `permission`: tool access policy
- `steps`: max agentic iterations

The body becomes the agent's system prompt.

**Invocation**

Agents are referenced by name in the TUI or via API. The active agent determines which system prompt, model, and tool set are used.

**Persistence**

The agent system prompt persists for the entire session the agent is active.

#### 1d. Skills (remote/external skill packs)

**Config**

```json
{ "skills": { "paths": ["./my-skills"], "urls": ["https://example.com/.well-known/skills/"] } }
```

`paths` lists additional local directories to scan; `urls` lists HTTP origins that expose a skills pack via a `.well-known/skills/` endpoint.

**Discovery**

`packages/opencode/src/config/skills.ts` — skills are fetched from remote URLs (supporting a standardised well-known skills protocol) or from local directories. This is an emerging/experimental layer.

**No skill index/catalog** injected into context automatically for auto-routing; invocation is explicit.

---

### 2. Goose (github.com/block/goose)

Goose's extensibility mechanism is called **Recipes**, with user-defined **Slash Commands** as the invocation surface.

#### 2a. Recipes

**File format**

YAML (or JSON). Key fields:

```yaml
version: "1.0.0"
title: "Title shown in UI"
description: "Short description"
prompt: "The user prompt that starts the recipe"
instructions: "System-level instructions for this recipe run"
extensions:
  - type: stdio
    name: tool_name
    cmd: binary
    args: [...]
parameters:
  - key: param_name
    input_type: string
    requirement: required
    description: "..."
sub_recipes:
  - name: sub
    path: sub_recipe.yaml
    values: { param: value }
```

`prompt` is the user message that kicks off the agent loop. `instructions` are injected into the system prompt for the recipe's session. `extensions` declare which MCP or stdio tools are enabled for this recipe.

**Storage**

Recipe files (`.yaml`) live anywhere on the filesystem. The path is stored in config under the slash command mapping.

**Discovery**

Slash command mappings are stored in Goose config under the key `slash_commands` as a list of `{ command, recipe_path }` pairs. See `crates/goose/src/slash_commands.rs:SLASH_COMMANDS_CONFIG_KEY`.

`list_commands()` reads this config key; `get_recipe_for_command(cmd)` looks up the recipe path for a given command string.

**Invocation**

When a user message starts with `/`, the agent checks `get_recipe_for_command()`. If a recipe is found, it loads the recipe and executes it. The recipe's `prompt` replaces or augments the user message. The `instructions` field is injected as a `recipe_prompt` via the prompt manager:

```rust
let recipe_prompt = prompt_manager.get_recipe_prompt().await;
```

This is merged into the system prompt builder alongside `frontend_instructions` (user-set always-on instructions) and extension/tool info.

**Injection**

Two-level:
- `instructions` → appended to system prompt via `prompt_manager.builder().with_frontend_instructions(...)`
- `prompt` → replaces/becomes the user turn message

**Persistence**

The recipe `instructions` persist as part of the system prompt for the entire recipe session. The recipe `prompt` is a single-turn message.

#### 2b. Frontend Instructions (always-on)

Goose also has `frontend_instructions` — user-set persistent instructions that the UI sends on every session. These are injected into the system prompt builder via `with_frontend_instructions(...)`.

**No skill catalog in context**. Invocation is purely explicit via `/command`.

---

### 3. Cline (github.com/cline/cline)

Cline is a VSCode extension with two complementary extensibility mechanisms.

#### 3a. `.clinerules` (project-level rules)

**Storage**

`.clinerules` at the project root — this can be either a single file or a **directory** of `.md` files:

```
.clinerules/
  general.md
  network.md
  storage.md
  hooks/
  workflows/
```

**Discovery**

Cline reads `.clinerules` from the workspace root automatically when a new task starts. If it's a directory, all `.md` files are concatenated in order.

**Injection**

Content is prepended into the system prompt for every turn in a session. It functions as always-on persistent context, not as a per-turn invocation.

**Persistence**

Persistent — present in every system prompt turn.

#### 3b. Custom Instructions (UI setting)

Users can set `customInstructions` in the Cline VS Code settings panel. This is stored in VS Code extension state (`ApiHandlerSettings`) and is similarly injected into the system prompt on every turn.

#### 3c. Deep Planning Slash Command

`src/core/prompts/commands/deep-planning/` contains Markdown templates for a "deep planning" command invocable via a slash command. This is invocation-based, single-turn injection.

**No skill index** or catalog injected into context for auto-routing.

---

### 4. Continue (github.com/continuedev/continue)

Continue (now rebranded as a CI-focused tool) has two layers:

#### 4a. Slash Commands (code-defined)

```typescript
export interface SlashCommand {
  name: string;
  description: string;
  params?: { [key: string]: any };
  run: (sdk: ContinueSDK) => AsyncGenerator<string | undefined>;
}
```

Slash commands are registered programmatically in `config.ts` (the user's Continue config file). The `run` function is an async generator that streams response text. It has full access to the `ContinueSDK` (LLM, IDE API, context items, history).

**Invocation**: User types `/name` in the chat input. Continue autocompletes available commands.

**Injection**: The generator yields text that becomes assistant message turns. It can also call `sdk.addContextItem()` to inject files/snippets.

**Persistence**: Single-turn execution; the `sdk.history` gives access to prior turns if needed.

#### 4b. Custom Commands (config-defined, simple)

```typescript
export interface CustomCommand {
  name: string;
  prompt: string;
  description: string;
}
```

Simpler variant stored in `continue.yaml` / `config.ts` under `customCommands`. On invocation, `prompt` is substituted as the user message.

**Config location**: `~/.continue/config.ts` (user) or `.continue/config.ts` (project).

**Invocation**: `/command-name` in chat.

**Persistence**: Single-turn.

**Autocomplete UI**: Continue renders available slash commands in a dropdown when the user types `/`.

---

### 5. Aider (github.com/Aider-AI/aider)

Aider does not have "skills" per se, but has two related conventions mechanisms.

#### 5a. Convention Files (`--read` / `read:`)

Users load read-only context files via:
- CLI: `aider --read CONVENTIONS.md`
- In-session: `/read CONVENTIONS.md`
- Config: `read: CONVENTIONS.md` in `.aider.conf.yml`

These files are added to the LLM context as read-only (not editable). With prompt caching enabled they are cached for efficiency.

**Storage**: Arbitrary filesystem paths.

**Discovery**: Manual / config. Not auto-discovered.

**Injection**: Added as read-only file content in the messages sent to the LLM. Persistent across turns within the session.

#### 5b. Built-in Slash Commands

Aider has a rich set of built-in `/commands` (implemented in `aider/commands.py`) — `/add`, `/read`, `/ask`, `/code`, `/architect`, `/help`, etc. — but these are hard-coded, not user-extensible. There is no user-defined slash command system.

---

## Implications for Steiner

1. **Two distinct extensibility patterns** are used across the ecosystem:
   - **Always-on context** (instructions / rules / conventions): loaded at session start, injected into system prompt, persistent across all turns. Used by OpenCode (`instructions`), Goose (`frontend_instructions`), Cline (`.clinerules`).
   - **On-demand invocations** (commands / recipes / slash-commands): triggered explicitly by the user, the content replaces or prepends a single user turn. Used by OpenCode (custom commands), Goose (recipes/slash commands), Continue (slash commands / custom commands).

2. **File formats converge on Markdown with optional YAML frontmatter**. Frontmatter carries metadata (model override, permissions, parameters); the body is the prompt text. Goose uses pure YAML for recipes since they need richer structured data (extensions, parameters, sub-recipes).

3. **Discovery is either filesystem glob or explicit config registration**:
   - Glob: scan a well-known directory for `*.md` (OpenCode agents, OpenCode commands, Cline rules)
   - Config registration: declare in a config file (Continue slash commands, Goose slash command → recipe mapping)

4. **Skill catalog in context**: No agent surveyed auto-injects a skill catalog into the system prompt for routing. Invocation is always explicit (slash command typing, command dialog, agent selector). OpenCode's `Ctrl+K` dialog provides UI discoverability without polluting context.

5. **Steiner's current system** (SKILL.md files in `~/.config/steiner/skills/<name>/`) is closer to OpenCode's custom-commands pattern (Markdown files, directory per skill, explicit invocation). Key gaps compared to peers:
   - No always-on "instructions" layer (separate from skills)
   - No remote/URL skill discovery
   - No project-local skill overrides
   - The skill index injected into context (for auto-routing) is a design choice unique to Steiner not seen in any peer — peers prefer explicit invocation

6. **Autocomplete / overlay UI**: Continue has `/` autocomplete. OpenCode has a command dialog (`Ctrl+K`). Goose has slash command detection at the agent loop level. None auto-detect intent from free-form user text to route to a skill.

---

## Risks and Uncertainties

- **OpenCode `instructions` field**: The exact schema of what strings in the `instructions` array represent (file paths vs. inline text vs. glob patterns) was not fully confirmed from the source — only that they are concatenated across config layers. The config loading deepwiki only confirms the merge strategy, not the content format.
- **Cline `.clinerules` loading code**: The exact Rust/TS loading code that reads `.clinerules` and injects it into the system prompt was not directly reviewed (the file path for the relevant source was 404'd on GitHub). The directory structure and usage was inferred from the repo's own `.clinerules` directory and search results.
- **Continue rebranding**: Continue has pivoted from IDE assistant to CI-enforcement tool. Their slash command / custom command system documented here reflects the `main` branch as of May 2026; the feature set may change significantly.
- **Goose recipe invocation flow**: The full `execute_command` / recipe loading path was not traced end-to-end in the source. The injection mechanism (recipe prompt → system vs. user message) was inferred from `agent.rs` and `prompt_template.rs` rather than confirmed line-by-line.
- **OpenCode remote skills (`.well-known/skills/`)**: This appears to be an emerging/experimental feature based on the config schema alone. No documentation or reference implementation was found.

---

## Sources

- OpenCode source: `https://github.com/sst/opencode` (branch `dev`)
  - `packages/opencode/src/config/command.ts`
  - `packages/opencode/src/config/agent.ts`
  - `packages/opencode/src/config/config.ts`
  - `packages/opencode/src/config/skills.ts`
  - `packages/opencode/src/config/paths.ts`
  - `packages/opencode/src/config/markdown.ts`
  - `packages/opencode/src/session/prompt.ts`
  - DeepWiki: `https://deepwiki.com/sst/opencode/3.2-agent-system`
  - DeepWiki: `https://deepwiki.com/sst/opencode/2.3-prompt-processing-pipeline`
  - DeepWiki: `https://deepwiki.com/sst/opencode/3.1-configuration-loading-and-merging`
  - OpenCode README: `https://github.com/opencode-ai/opencode` (custom commands section)
- Goose source: `https://github.com/block/goose` (branch `main`)
  - `crates/goose/src/slash_commands.rs`
  - `crates/goose/src/recipe/mod.rs`
  - `crates/goose/src/agents/agent.rs`
  - `crates/goose/src/prompt_template.rs`
  - `crates/goose/src/prompts/` (template directory)
- Cline source: `https://github.com/cline/cline` (branch `main`)
  - `src/core/prompts/commands.ts`
  - `src/shared/api.ts`
  - `.clinerules/` (project's own rules directory)
- Continue source: `https://github.com/continuedev/continue` (branch `main`)
  - `core/config/types.ts` (SlashCommand, CustomCommand interfaces)
- Aider: `https://aider.chat/docs/usage/conventions.html`

---

## Open Questions

1. **OpenCode `instructions` array format**: Are entries file paths, inline text, or glob patterns? Reviewing `packages/opencode/src/session/prompt.ts` around the SystemPrompt build step would confirm this.

2. **Steiner skill catalog injection**: Steiner currently injects a skill name list into the system prompt for auto-routing. No peer does this. Is the cost (tokens, noise) justified, or would explicit `/skill-name` invocation with TUI autocomplete be a better UX?

3. **Project-local skill overrides**: Should `<project>/.steiner/skills/` be supported, analogous to OpenCode's `.opencode/commands/`? All peers support project-local extensibility.

4. **Always-on instructions layer**: Steiner has no equivalent of OpenCode `instructions` / Cline `.clinerules`. Should a `~/.config/steiner/instructions.md` or `.steiner/instructions.md` always-on layer be added, separate from per-skill invocation?

5. **Remote skill distribution**: OpenCode's `skills.urls` supports fetching skill packs from a `.well-known/skills/` HTTP endpoint. Is this worth adding to Steiner for team-shared skill distribution?

6. **Skill parameters**: Goose recipes support typed parameters (`key`, `input_type`, `requirement`). Should Steiner skills support a parameter schema so skills can be parameterised at invocation time?

7. **Autocomplete / command dialog**: Continue has `/` autocomplete; OpenCode has `Ctrl+K`. Steiner TUI currently has no discovery UI for available skills. What is the right UX for skill selection?

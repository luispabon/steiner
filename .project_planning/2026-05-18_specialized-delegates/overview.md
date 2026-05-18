## Request

Add specialized sub-agent tools that are aliases to `delegate` but use focused, narrow system prompts and strict tool allowlists. Each tool (`explore`, `research`, `code`, `plan`, `verify`) has a single `task` parameter, a curated tool subset, and a role-specific system prompt. The generic `delegate` tool remains as an escape hatch. Agent guidance steers the parent model toward specific types first.

Additionally, add dummy `web_search` and `fetch_url` tools for the `research` delegate type (actual implementation is separate future work).

Model selection is config-driven per agent type via model aliases, not a tool parameter.

## Overview

### Architecture

Each specialized delegate is a separate tool registered alongside the existing `delegate` tool. Internally, each constructs a `DelegationSpec` with a baked-in system prompt, passes the type's tool allowlist to `buildChildRegistries`, and calls `SpawnDelegate`. No new execution machinery — thin wrappers over existing delegation infrastructure.

### Agent Types

| Type | Role | Tool Allowlist | Default Model Tier |
|---|---|---|---|
| `explore` | Read-only codebase navigation. Find files, symbols, patterns. Answer "where/what is X" | `read`, `glob`, `grep`, `ls`, `scratchpad` | cheap |
| `research` | Gather external information. Web search, fetch docs, synthesize findings | `read`, `glob`, `grep`, `ls`, `web_search`, `fetch_url`, `scratchpad` | cheap |
| `code` | Implement changes. Write code, run commands, fix errors | `read`, `glob`, `grep`, `ls`, `write`, `edit`, `apply_patch`, `bash`, `scratchpad` | default |
| `plan` | Analyze a specific sub-problem and produce structured analysis. Read-only | `read`, `glob`, `grep`, `ls`, `scratchpad` | default |
| `verify` | Run tests, linters, build checks. Report pass/fail results. No code mutation | `read`, `glob`, `grep`, `ls`, `bash`, `scratchpad` | cheap |

### Tool Schema (same for all types)

```json
{
  "type": "object",
  "properties": {
    "task": {
      "type": "string",
      "description": "Task description for the sub-agent"
    }
  },
  "required": ["task"]
}
```

### System Prompts

Each type gets a short (200-400 token), role-focused system prompt. Key properties:
- States what the agent is and what it can do
- Instructs result format appropriate to the role
- Tells the agent to use scratchpad for intermediate findings
- No generic boilerplate shared across types
- `explore`: brevity-first, return file paths + relevant context
- `research`: synthesize findings, cite sources, flag uncertainties
- `code`: thoroughness-first, test what you change, report what was done
- `plan`: structured analysis output (options, tradeoffs, recommendation), explicitly scoped to a sub-problem — not overall task planning
- `verify`: report pass/fail per check, quote exact errors, no fixes

### Model Selection

Per-agent-type config via model aliases:

```yaml
sub_agent:
  max_turns: 15
  max_tokens: 100000
  agents:
    explore:
      model: fast
    research:
      model: fast
    code:
      model: default
    plan:
      model: default
    verify:
      model: fast
```

Each type falls back to the global default model if no per-type override is configured. Turn and token budgets are inherited from `SubAgentConfig` defaults (not per-type).

### Dummy Tools: `web_search` and `fetch_url`

Registered as real tools with stub handlers that return a "not implemented" message. Only included in the `research` agent's allowlist. Actual implementation is separate future work. This lets the research agent's schema and system prompt be complete from day one, and models will see these tools exist even if they don't do anything yet.

### Parent Agent Guidance

The parent agent's system prompt will include guidance to:
1. Prefer a specific delegate type when the task fits
2. Use `explore` for navigation/lookup questions
3. Use `research` for external information gathering
4. Use `code` for implementation sub-tasks
5. Use `plan` for analyzing specific sub-problems (not overall planning)
6. Use `verify` for running checks and reporting results
7. Fall back to `delegate` only when no type fits

### Key Design Decisions

- **One tool per type, not a `type` param on `delegate`**: simpler for small models, clearer intent from tool name alone
- **Thin wrappers over existing infra**: `DelegationSpec` already supports `SystemPrompt` override; `buildChildRegistries` already takes an explicit `allowedTools` list
- **Scratchpad always included**: all sub-agents get scratchpad unconditionally for work survivability across compaction
- **No nesting**: specialized delegates cannot spawn further delegates (same as current `delegate` behavior — `delegate` tool is excluded from child registries)
- **Result formatting via system prompt, not sentinel tool**: each type's prompt includes result format instructions. Goose's `recipe__final_output` pattern is interesting but adds complexity not justified for v1

### Code Areas

- `internal/delegation/` — new agent type definitions, per-type tool allowlists, per-type system prompts, tool registration helpers
- `internal/config/` — `SubAgentConfig` extension with per-agent-type model config
- `cmd/steiner/tools.go` — register new specialized delegate tools
- `internal/tool/` — dummy `web_search` and `fetch_url` tool definitions
- `internal/prompt/` — parent agent guidance additions
- `docs/DELEGATION.md` — update documentation

## Verification Strategy

### Sources
- CLAUDE.md
- Makefile
- .github/workflows/checks.yml
- .golangci.yml

### Defaults
- execution_verification_timing: deferred_until_end_of_implementation
- reviewer_verification_timing: rerun_minimal_relevant_checks_first
- broad_expensive_checks_default: late_only
- repo_wide_formatting_allowed: true

### Commands

#### formatting
- preferred_mode: fix
- fix:
  - `gofmt -w <changed files>`
  - `goimports -w <changed files>`
- check:
  - `make fmt-check`
  - `make imports-check`
- use_check_only_when:
  - verifying without intent to auto-fix

#### build
- preferred_mode: check
- fix:
  - n/a
- check:
  - `make build-binaries`
- use_check_only_when:
  - always

#### unit-tests
- preferred_mode: check
- fix:
  - n/a
- check:
  - `go test ./path/to/pkg -run TestName`
  - `go test ./...`
- use_check_only_when:
  - always

#### vet
- preferred_mode: check
- fix:
  - n/a
- check:
  - `go vet ./...`
- use_check_only_when:
  - always

#### lint
- preferred_mode: check
- fix:
  - n/a
- check:
  - `golangci-lint run ./...`
- use_check_only_when:
  - always

#### vuln
- preferred_mode: check
- fix:
  - n/a
- check:
  - `govulncheck ./...`
- use_check_only_when:
  - always

### Tiers
- cheap:
  - formatting
  - build
  - vet
- medium:
  - unit-tests
  - lint
- expensive:
  - vuln

### Required Boundaries
- step_level_exceptions:
  - none
- stage_level_exceptions:
  - none
- end_of_implementation:
  - `make quick-check` at minimum
  - `make check` for larger changes
- reviewer_after_fix:
  - rerun failed checks after any fix

### Assumptions
- `golangci-lint`, `goimports`, and `govulncheck` are installed (via `make install-check-tools`)
- Go 1.25 toolchain available

### Uncertainties
- none

## Decision Log

| # | Decision | Rationale |
|---|----------|-----------|
| 1 | One tool per agent type (not a `type` param on `delegate`) | Simpler for small models; clearer intent from tool name |
| 2 | Single `task` parameter only | Minimal schema reduces model confusion; system prompt + allowlist are baked per type |
| 3 | Scratchpad unconditionally for all sub-agents | Work survivability — findings persist across compaction |
| 4 | Model selection config-driven, not a tool parameter | Prevents models from making bad model choices at runtime |
| 5 | Turn/token budgets inherited from SubAgentConfig defaults | Avoid arbitrary per-type budgets; single config knob |
| 6 | `delegate` stays as escape hatch | Guidance steers to specific types first; generic covers edge cases |
| 7 | Dummy `web_search`/`fetch_url` with stub handlers | Lets research agent schema be complete; implementation separate |
| 8 | `plan` delegate scoped to sub-problem analysis only | Guidance prevents over-delegation of planning from parent; risk of cheap parent spawning expensive planner noted for testing |
| 9 | Result formatting in system prompt, not sentinel tool | Simpler for v1; sentinel tool pattern (Goose) adds latency and complexity |

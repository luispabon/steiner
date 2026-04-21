# steiner

`steiner` is a minimal, local-first coding agent written in Go.

The project is aimed at real coding work against local repositories, with a strong bias toward bounded context, explicit context injection, and simple, inspectable execution. It is designed to work with OpenAI-compatible providers, including local model servers.

## Status

Current implementation is past the foundations work and through the first context-discipline milestones from the project roadmap.

Implemented today:

* single-agent loop with tool calling
* interactive terminal mode and single-shot `--exec`
* config loading, merging, validation, env overrides, and CLI overrides
* provider abstraction with central scheduler-enforced parallelism
* core tools binary with `read`, `glob`, `search`, `write`, `bash`, and `edit`
* approval prompts and safer mutation via `edit`
* project context gathering with bounded budgets
* skill discovery, explicit loading, and REPL skill toggling
* context diagnostics, output truncation, and conversation compaction foundations
* terminal event/log sinks and optional session log files

Still intentionally unfinished:

* richer console UX such as streaming, shell-like line editing, and markdown-aware rendering
* delegation and sub-agent execution
* later advanced extensions such as sandboxing, persistence, and MCP

If you are looking for the intended product shape rather than only the current implementation, start with [docs/PRD.md](docs/PRD.md).

## Project Goals

The product direction is defined by a few hard constraints:

* minimal prompting rather than framework-heavy orchestration
* local-first operation with OpenAI-compatible endpoints
* context hygiene as a first-class engineering concern
* plugin-first tool registration
* safe-by-default execution with approvals and bounded output
* console UX that keeps the agent understandable and controllable in terminal use
* future delegation through isolated sub-agents, but only after single-agent UX is strong

The architecture and package boundaries in this repo are intentionally stricter than the current amount of code might suggest. That is deliberate: the project is trying to avoid painting itself into a corner while the single-agent product matures.

## Repository Layout

The package layout is already in place:

```text
cmd/steiner/            CLI entry and subcommands
cmd/steiner-core-tools/ Core tools binary
internal/agent/         Loop orchestration, state, limits
internal/config/        Loading, merging, validation, defaults
internal/provider/      Model transport interfaces and scheduler
internal/tool/          Registry, schema, policy, executor, output shaping
internal/prompt/        Context gathering, budgeting, assembly, compaction
internal/skill/         Skill discovery and loading
internal/repl/          Interactive UX
internal/delegation/    Delegation contracts and execution scaffolding
internal/output/        Terminal and machine-readable event output
testdata/repos/         Fixture repos for integration and e2e tests
docs/                   PRD, roadmap, and implementation planning docs
```

The conventions that guide the codebase live in [AGENTS.md](AGENTS.md).

## What You Can Run Today

The current CLI surface is intentionally small:

```bash
steiner
steiner --exec "fix the failing test"
steiner version
steiner config
steiner tools
steiner skills
```

Useful flags:

* `--config` overrides the project config path
* `--model` overrides the configured provider model
* `--verbose` enables verbose logging in resolved config
* `--log-file` writes a full session event log to a file

At the moment:

* `steiner` starts the interactive REPL in the current project
* `steiner --exec "..."` runs a single prompt headlessly and prints the final assistant reply
* `steiner version` prints the build version
* `steiner config` prints the resolved configuration after defaults, files, env vars, and CLI overrides
* `steiner tools` lists configured tools
* `steiner skills` lists discovered skills

Current built-in REPL commands:

```text
/help
/tools
/skills
/history
/clear
/exit
```

Discovered skills can also be toggled from the REPL by typing `/<skill-name>`.

## Configuration

Configuration precedence is:

1. compiled defaults
2. `~/.config/steiner/config.yaml`
3. `.steiner/config.yaml`
4. environment variables with `STEINER_` prefix
5. CLI flags

Important environment variables:

* `STEINER_API_KEY`
* `STEINER_BASE_URL`
* `STEINER_MODEL`
* `STEINER_PROVIDER_PARALLELISM`
* `STEINER_MAX_TURNS`
* `STEINER_LOG_LEVEL`

The default provider configuration targets a local OpenAI-compatible endpoint:

```yaml
provider:
  type: openai_compat
  base_url: http://localhost:11434/v1
  model: qwen3-35b-a3b
  temperature: 0.2
  max_completion_tokens: 8192
  parallelism: 1
```

The scheduler for `provider.parallelism` already exists and is enforced centrally in `internal/provider`.

Approval defaults:

* `read`, `glob`, `search` -> `auto`
* `write`, `bash`, `edit` -> `prompt`

`edit` is the preferred mutation primitive for in-place changes. `write` remains available for full-file overwrites.

Project context assembly is configurable through `project_context`, including budget, extra files, and ignore files. Conversation/tool context is also bounded and emits diagnostics when budgets or compaction rules apply.

## Build And Test

Requirements:

* Go 1.24+

Build:

```bash
go build ./...
```

Run tests:

```bash
go test ./...
```

The current test coverage is focused on the implemented runtime and CLI surface:

* config loading and validation
* provider scheduler semantics
* prompt assembly, project context gathering, and compaction
* tool registry, execution, and approval behavior
* REPL commands and exec-mode behavior
* terminal output/event formatting

## Design Notes

Several architectural rules are already fixed:

* context source precedence is explicit and must not be reordered
* skills are auxiliary context, not system-authority instructions
* provider access must go through the scheduler
* prompt assembly and agent execution remain separate packages
* config merging belongs only in `internal/config`
* sub-agents must be isolated and must not nest once they exist

Those constraints are documented in [AGENTS.md](AGENTS.md) and elaborated in [docs/PRD.md](docs/PRD.md).

## Roadmap

The current codebase is at the point where the single-agent loop, safer mutation, and context-discipline groundwork exist. The next documentation and product work is focused on console UX before delegation returns to the front of the queue.

See:

* [docs/PRD.md](docs/PRD.md)
* [docs/ROADMAP.md](docs/ROADMAP.md)
* [docs/INITIAL_IMPLEMENTATION_PLAN.md](docs/INITIAL_IMPLEMENTATION_PLAN.md)

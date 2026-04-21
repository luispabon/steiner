# steiner

`steiner` is a minimal, local-first coding agent written in Go.

The project is aimed at real coding work against local repositories, with a strong bias toward bounded context, explicit context injection, and simple, inspectable execution. It is designed to work with OpenAI-compatible providers, including local model servers.

This repository is still in the early implementation stages. The current codebase is a foundations skeleton, not a usable end-to-end agent yet.

## Status

Current implementation state is roughly Stage 0 from the project roadmap:

* CLI skeleton in `cmd/steiner`
* config loading, merging, env overrides, and validation in `internal/config`
* provider abstraction and central scheduler in `internal/provider`
* agent state/message types in `internal/agent`
* tool registry and OpenAI-style schema generation in `internal/tool`
* basic output/logging types in `internal/output`

Not implemented yet:

* model provider transport
* agent execution loop
* REPL or working `--exec` task execution
* core tools binary in `cmd/steiner-core-tools`
* approvals UX
* project context assembly
* skills loading and injection
* context compaction
* delegated sub-agents

If you are looking for the intended product shape rather than the current implementation, start with [docs/PRD.md](docs/PRD.md) and [docs/ROADMAP.md](docs/ROADMAP.md).

## Project Goals

The product direction is defined by a few hard constraints:

* minimal prompting rather than framework-heavy orchestration
* local-first operation with OpenAI-compatible endpoints
* context hygiene as a first-class engineering concern
* plugin-first tool registration
* safe-by-default execution with approvals and bounded output
* future delegation through isolated sub-agents, but only after context discipline is solid

The architecture and package boundaries in this repo are intentionally stricter than the current amount of code might suggest. That is deliberate: the project is trying to avoid painting itself into a corner before Stage 1 and Stage 3 concerns land.

## Repository Layout

The intended package layout is already in place:

```text
cmd/steiner/            CLI entry, subcommands
cmd/steiner-core-tools/ Core tools binary (planned, not yet implemented)
internal/agent/         Loop orchestration, state, limits
internal/config/        Loading, merging, validation, defaults
internal/provider/      Model transport interfaces and scheduler
internal/tool/          Tool registry, schema, policy-facing types
internal/prompt/        Prompt/context assembly types
internal/skill/         Skill discovery and loading (planned)
internal/repl/          Interactive UX (planned)
internal/delegation/    Delegation scaffolding (planned)
internal/output/        Terminal and machine-readable event output
docs/                   PRD, roadmap, and implementation planning docs
testdata/repos/         Fixture repos for integration and e2e tests (planned)
```

The conventions that guide the codebase live in [AGENTS.md](AGENTS.md).

## What You Can Run Today

The current CLI surface is intentionally small:

```bash
steiner version
steiner config
steiner --config path/to/config.yaml config
steiner --model some-model config
```

At the moment:

* `steiner version` prints the build version
* `steiner config` prints the resolved configuration after defaults, files, env vars, and CLI overrides are applied
* `--exec` exists only as a stub flag and does not run an agent yet

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

The scheduler for `provider.parallelism` already exists and is enforced centrally in `internal/provider`. That matters later for delegated work, but the constraint is being established now rather than bolted on after the fact.

Approval defaults are also already defined in config, even though tool execution is not wired up yet:

* `read`, `glob`, `search` default to `auto`
* `edit`, `write`, `bash` default to `prompt`

Stage 2 adds `edit` as the preferred safer mutation primitive while keeping `write` for compatibility. The runtime surface and schemas should prefer `edit` for in-place file changes, and reserve `write` for full-file overwrites.

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

The current repository test coverage is focused on the implemented foundations:

* config loading and validation
* scheduler semantics
* tool registry and schema generation
* CLI config/version behaviour

## Design Notes

Several architectural rules are already fixed even though the feature set is still small:

* context source precedence is explicit and must not be reordered
* skills are auxiliary context, not system-authority instructions
* sub-agents must be isolated and must not nest
* provider access must go through the scheduler
* prompt assembly and agent execution remain separate packages
* config merging belongs only in `internal/config`

Those constraints are documented in [AGENTS.md](AGENTS.md) and elaborated in the PRD.

## Roadmap

Planned stages:

1. foundations skeleton
2. core single-agent loop
3. execution safety and safer mutation
4. context discipline and compaction
5. delegation scaffolding
6. sub-agent execution
7. hardening and advanced features

See:

* [docs/PRD.md](docs/PRD.md)
* [docs/ROADMAP.md](docs/ROADMAP.md)
* [docs/INITIAL_IMPLEMENTATION_PLAN.md](docs/INITIAL_IMPLEMENTATION_PLAN.md)

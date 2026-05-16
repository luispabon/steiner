## Request

Refactor Steiner's model configuration into a clean provider/model split with automatic metadata resolution. The current `ModelConfig` mixes transport (type, base_url, api_key), identity (model), request params (extra_params, thinking), and limits (context_size, max_completion_tokens, compaction) in one flat struct. The target design separates these into:

- **Providers**: endpoint, transport, auth — how Steiner talks to an API
- **Models**: stable alias, backend ID, request tuning — what Steiner asks for
- **ResolvedModel**: runtime object combining provider + model + discovered/derived limits

Additionally: fold ThinkingConfig into model-level config (behavioral controls become model fields, provider-specific thinking params go into extra_params). Implement all provider types (openai_compat, ollama, lmstudio, openrouter, openai, anthropic, gemini, litellm). Add metadata resolution chain (user override → provider discovery → models.dev cache → fallback). Add CLI inspection commands.

Full design spec: `project_planning/model_provider_refactoring.md`

## Overview

### Phase 1 — Config Split

Introduce top-level `providers` and `models` config sections. Each provider holds only transport/auth. Each model references a provider by alias and holds backend ID, params, extra_params, and behavioral controls (thinking). Remove provider fields from model config. Add `default_model`. Implement all provider types as config identifiers with sensible defaults (actual transport differences come later in Phase 5).

**ThinkingConfig fold:** Remove the separate `ThinkingConfig` struct. Model config gains:
- `thinking_enabled` (bool) — master switch
- `thinking_disable_marker` (string) — per-turn disable
- `thinking_scaffold_inference` (bool) — scaffold inference toggle
- Provider-specific thinking params live in `extra_params` (e.g. `extra_params.thinking.type: enabled`)

The runtime merge-at-request-time logic moves into the request payload builder.

### Phase 2 — ResolvedModel

Introduce `ResolvedModel` as the internal runtime object. Contains: alias, provider config, backend model ID, effective limits, tokenizer strategy, params, extra_params, thinking behavioral flags, metadata source, confidence, warnings. All runtime consumers (agent loop, prompt assembly, delegation) use `ResolvedModel` instead of raw config.

### Phase 3 — Limits Model Rewrite

Replace `context_size` as the primary internal concept. Introduce effective limits: `context_window`, `max_input_tokens`, `max_output_tokens`, `output_reserve_tokens`, `safety_margin_tokens`, `summary_max_tokens`, `compaction_threshold`. Update `ModelTokenBudget` and prompt fitting/compaction to consume effective limits from `ResolvedModel`. Derive defaults from `context_window` when only that is known.

### Phase 4 — Request Params Migration

Formalize the `params` / `extra_params` split. `params` = normalized Steiner-known generation params (temperature, top_p, etc.). `extra_params` = raw provider-specific passthrough merged into request body. Define merge order: generated defaults < params < extra_params. Thinking params injection becomes part of this merge when `thinking_enabled` is true.

### Phase 5 — Provider Discovery Interface

Add metadata discovery interface behind provider type. Each provider type can optionally implement discovery to resolve context_window and max_output_tokens for a given backend model ID. Implement for: OpenRouter (API), Ollama (show API), LM Studio (models endpoint), generic OpenAI-compat (/v1/models). Discovery failure is non-fatal. Discovery is for Steiner's budgeting, never mutates provider state.

### Phase 6 — models.dev Cache

Add models.dev as external metadata fallback. Local JSON cache at `$XDG_CACHE_HOME/steiner/model-metadata/models.dev.json` with 7-day TTL. Conditional HTTP refresh (ETag/Last-Modified). Atomic writes. Offline-safe (stale cache acceptable, missing cache acceptable). Lookup sits after provider discovery and before fallback in the resolution chain.

### Phase 7 — Startup Warnings and CLI Commands

Warn once per fallback-resolved model on startup. Add `steiner model inspect <alias>` showing resolved config, limits, sources, confidence. Add `steiner model-metadata status|refresh|clear` for cache management.

### Phase 8 — Token Counter Abstraction

Wrap current tiktoken estimator behind a strategy interface. Support: tiktoken, heuristic, provider-usage calibration. If provider returns usage in responses, maintain correction factor per model. Show tokenizer source/confidence in inspect output.

### Phase 9 — Cleanup and Documentation

Remove old `ModelConfig` flat fields. Remove direct uses of `context_size`. Ensure all prompt assembly and compaction uses `ResolvedModel`. Comprehensive test coverage for config parsing, resolution order, fallback warnings, params passthrough, cache behavior.

**README.md rewrite** — the current README (lines 70–170) documents the old flat config format with inline/alias model selection. Must be replaced with the new provider/model structure including these examples:

1. **Minimal local** — LM Studio or Ollama with one model, no params (3-line config)
2. **OpenRouter** — cloud provider with api_key_env
3. **Multiple providers** — local + cloud with multiple models referencing different providers
4. **Tuned model** — params, extra_params, thinking disabled
5. **Advanced overrides** — explicit `advanced.limits` for a model with unknown metadata
6. **Multi-model** — default_model plus additional models (future agent readiness)

Also update: project description to mention provider/model split, `steiner config` output description, and the "minimum useful config" summary at the bottom of the config section.

### Key Design Decisions

1. **No backwards compatibility** — old config shape removed, not dual-supported
2. **ThinkingConfig folded** — behavioral flags become model-level fields, provider params go to extra_params
3. **All provider types from day one** — config-level types with sensible defaults; transport differences in Phase 5
4. **Manual overrides always win** — `advanced.limits` in model config overrides any discovery/cache
5. **Discovery never mutates** — never sends inferred context back to providers (e.g. no Ollama num_ctx)
6. **Conservative fallback** — 32K context, 4K output when nothing is known, with clear warning

### Affected Code Areas

- `internal/config/` — struct rewrite, new validation, new parsing
- `internal/provider/` — provider type registry, discovery interface, request payload builder
- `internal/prompt/` — token budget, compaction, budget fitting consume ResolvedModel
- `internal/delegation/` — sub-agent model resolution via alias
- `cmd/steiner/` — new CLI subcommands, wiring
- New package: `internal/metadata/` — models.dev cache, resolution chain

### Risks

- **Large surface area**: 9 phases touching core paths. Mitigated by strict phase ordering.
- **ThinkingConfig fold**: behavioral controls moving location. Mitigated by preserving exact semantics.
- **Provider type explosion**: 8 types configured but only openai_compat has real transport. Others are config-level aliases until Phase 5 adds discovery.
- **models.dev dependency**: external data source could change format. Mitigated by treating as optional with graceful degradation.

## Verification Strategy

### Sources
- Makefile (primary task runner)
- .github/workflows/checks.yml (CI)
- .golangci.yml (lint config)
- CLAUDE.md (repo instructions)

### Defaults
- execution_verification_timing: deferred_until_end_of_implementation
- reviewer_verification_timing: rerun_minimal_relevant_checks_first
- broad_expensive_checks_default: late_only
- repo_wide_formatting_allowed: true

### Commands

#### formatting
- preferred_mode: fix
- fix:
  - `gofmt -w <files>`
  - `goimports -w <files>`
- check:
  - `make fmt-check`
  - `make imports-check`
- use_check_only_when:
  - verifying no formatting drift without modifying files
  - CI environment

#### build
- preferred_mode: check
- fix:
  - n/a
- check:
  - `make build-binaries`
- use_check_only_when:
  - always (build is inherently check-only)

#### unit-tests
- preferred_mode: check
- fix:
  - n/a
- check:
  - `go test ./...`
  - `go test ./path/to/pkg -run TestName` (targeted)
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

#### race-tests
- preferred_mode: check
- fix:
  - n/a
- check:
  - `go test -race ./...`
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

#### tidy
- preferred_mode: fix
- fix:
  - `go mod tidy`
- check:
  - `make tidy-check`
- use_check_only_when:
  - CI environment

### Tiers
- cheap:
  - formatting
  - build
  - vet
- medium:
  - unit-tests
  - lint
- expensive:
  - race-tests
  - vuln
  - tidy

### Required Boundaries
- step_level_exceptions:
  - none
- stage_level_exceptions:
  - none
- end_of_implementation:
  - `make quick-check` (minimum)
  - `make check` (recommended for large changes)
  - `make ci-check` (before merge)
- reviewer_after_fix:
  - run targeted tests for affected packages
  - run `make quick-check` after any fix

### Assumptions
- golangci-lint, goimports, govulncheck are installed (via `make install-check-tools`)
- Go 1.25 as per go.mod
- All formatting tools have safe fix modes

### Uncertainties
- Whether govulncheck will flag new dependencies added for HTTP/cache operations
- Whether race detector will surface issues in concurrent metadata resolution

## Decision Log

| # | Decision | Rationale |
|---|----------|-----------|
| 1 | All 9 phases in one plan | User confirmed full scope |
| 2 | All provider types from Phase 1 | User confirmed. Types are config identifiers initially; transport differences added in Phase 5 |
| 3 | ThinkingConfig folded into model config | User chose this. Behavioral controls (enabled, disable_marker, scaffold_inference) become model-level fields. Provider params go to extra_params. |
| 4 | No backwards compatibility | Per design doc. Old config shape removed entirely. |
| 5 | Research not needed | Design doc is exhaustive. Internal refactoring of own codebase. No unfamiliar external dependencies. |
| 6 | New package `internal/metadata/` | Resolution chain and models.dev cache are distinct concerns from config loading or provider transport |

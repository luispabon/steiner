## Request

Add a per-model system prompt suffix that appends to the system preamble without replacing it, enabling model-specific steering while preserving the default preamble, delegation instructions, identity, and caveman mode.

## Overview

Add `SystemSuffix` field to `ModelPrompts`. The suffix is appended as the very last thing in the system preamble (after caveman instructions). This mirrors the existing `PromptSuffix` pattern (which appends to user messages) but targets the system prompt instead.

The preamble already bypasses byte budgets — it is never truncated. The suffix inherits this behavior automatically since it becomes part of the preamble block. No budget system changes needed.

Flow: `ModelConfig.Prompts.SystemSuffix` → `ResolvedModel.Prompts` (automatic, struct embed) → `AssemblyOptions.PromptOverrides` → `SystemPreamble()` → appended last.

## Key Decisions

- **Field location**: `ModelPrompts.SystemSuffix` (not a top-level `ModelConfig` field) — groups with existing `System` override, makes the relationship clear.
- **Ordering**: Suffix goes after caveman instructions (absolute last position in preamble) for maximum positional steering weight. Confirmed with user.
- **No budget changes**: Preamble bypasses budgets entirely (`source_plan.go:81-84`). Suffix is part of preamble, so it inherits this.

## Tradeoffs

- **Suffix vs second system message**: A suffix keeps everything in one system block (simpler, better KV cache reuse for local inference). A separate system message could be independently cached but adds complexity. Chose suffix for simplicity.
- **Last position vs configurable position**: Could allow configuring where suffix inserts. Chose fixed last position — covers the steering use case without config complexity.

## Scope Boundaries

**In scope:**
- `SystemSuffix` field in `ModelPrompts` struct
- Patch support for YAML config merging
- Plumbing through `SystemPreamble()` function signature
- Update `source_plan.go` and `context_manager_base.go` to pass suffix
- Unit tests for config patching, preamble assembly ordering
- Documentation in `docs/CONFIGURATION.md`

**Out of scope:**
- No changes to `PromptSuffix` (user-message suffix)
- No budget system changes
- No CLI flags for suffix
- No sub-agent-specific suffix handling (sub-agents use their own model config)

## Verification Strategy

| Command | Cost | Mode |
|---------|------|------|
| `gofmt -w <files>` | Cheap | Fix |
| `goimports -w <files>` | Cheap | Fix |
| `go test ./path/to/pkg -run TestName` | Cheap | Targeted |
| `go test ./...` | Medium | Full suite |
| `go vet ./...` | Cheap | Check |
| `go build ./...` | Medium | Check |
| `make check` | Medium-Expensive | Repo-mandated, required before finalizing |

Prefer targeted tests during development, `make check` before finalizing.

## Decision Log

| Decision | Rationale |
|----------|-----------|
| `SystemSuffix` in `ModelPrompts` | Groups with existing `System` override |
| Append after caveman | User confirmed; max positional weight |
| No budget changes | Preamble already bypasses budgets |
| No research needed | Fully repo-local, no external deps |

## Request

Revise the armature of steiner's system prompt so that language flows naturally, sections are grouped properly, and each part is complementary rather than overriding. Make `code` and `delegate` sub-agent prompts share the main agent's system prompt source (DRY). Closes #193.

## Overview

The system prompt is restructured from ad-hoc string concatenation into a section-based architecture with configurable ordering. Each section is a named, self-contained block. Assembly iterates an ordered slice of section IDs, skipping sections that are absent (e.g. delegation when disabled). Style modifiers (caveman, humanizer) and system suffix always append at the tail, outside the ordering system.

### Section ordering (Option B)

```
1. Identity        — single sentence, line 1
2. Delegation      — context management philosophy (when enabled)
3. Core rules      — behavioral guardrails (numbered list)
4. Workflow         — editing, verification, skills, response format
--- tail (always last, not reorderable) ---
5. Caveman         — terse style (when enabled)
6. Humanizer       — anti-AI-writing (when enabled)
7. System suffix   — per-model dynamic content
```

When delegation is absent (sub-agents), collapses to: Identity → Core rules → Workflow → tail. Both critical sections land in high-attention zones.

### DRY for code and delegate sub-agents

Currently each agent type in `internal/delegation/agent_type.go` has a self-contained system prompt that duplicates rules from `defaultSystemPreamble`. The `code` and `delegate` agents will instead use the shared base preamble (identity + core rules + workflow) with a short role-specific addendum appended as a new section between workflow and the style tail.

The agent type prompts for `explore`, `research`, `plan`, and `verify` remain self-contained — their roles are sufficiently different that sharing the full preamble would add noise.

For `code` and `delegate`:
- `agentPrompts[AgentTypeCode]` becomes a short addendum (code-agent-specific workflow: use mutate, list files changed, report verification)
- `defaultChildSystemPrompt` becomes a short addendum (generic sub-agent framing)
- `buildChildPrompt` passes the addendum via a new mechanism (e.g. a field on `AssemblyOptions` or a section in the ordering) rather than as a full `PromptOverrides.System` override
- `SystemPreamble` receives the addendum and places it after workflow, before the style tail

### Delegation-agnostic core rules

The current `defaultSystemPreamble` contains delegation-specific lines ("For multi-file inspection, delegate to `explore`"). These move into the delegation section. Core rules become pure behavioral constraints that make sense with or without delegation enabled.

### Narrative flow

The current prompt reads as disconnected blocks. The rewrite groups related concerns under `##` headers with a natural reading order: who you are → how you manage context → what constraints to respect → how to do the work. Each section uses the formatting best suited to its content:
- Identity: one dense sentence
- Delegation: `##` header, tool table, numbered heuristics
- Core rules: numbered list (scannable from any attention position)
- Workflow: subsections for before/during/after editing

## Key Decisions

1. **Option B ordering (Delegation → Rules → Workflow)**: Delegation gets primacy position. Small models benefit most — they need the strongest steering toward delegation to keep context tight. Core rules in position 3 are mitigated by being a short numbered list (research shows small models parse explicit list structure reliably from any position).

2. **Configurable section ordering**: Sections defined as named blocks with a default order slice. Reordering is a one-line change for experimentation. Style tail stays fixed outside the ordering system.

3. **DRY scope limited to code and delegate**: Only these two agent types share enough behavioral overlap with the main agent to justify sharing the preamble. The other four types (explore, research, plan, verify) have distinct enough roles that sharing would add irrelevant noise.

4. **Addendum mechanism over full override**: code/delegate sub-agents get the shared base + a role-specific addendum, rather than a full system prompt override. This means they automatically inherit any future preamble improvements.

5. **Delegation-agnostic core rules**: All delegation-aware behavioral nudges move from core rules into the delegation section. Core rules work correctly for both main agent and sub-agents without conditional logic.

## Tradeoffs

1. **Core rules in attention valley (main agent)**: With Option B, core rules sit in position 3 — the middle of the U-curve. Mitigated by keeping rules as a short numbered list. Alternative was Option A (rules first, delegation middle) but delegation is steiner's core differentiator and the largest section — primacy is the only position that reliably carries a 60-line block on small models.

2. **Longer code sub-agent prompt**: Sharing the full preamble makes the code agent's system prompt longer than its current self-contained version. Tradeoff is worth it: the code agent inherits guardrails and future improvements automatically, and the extra tokens are in the system prompt (cached, not per-turn).

3. **Test churn**: Many existing tests assert specific substring presence/ordering in `SystemPreamble` output. These need rewriting. Not a risk, just work.

4. **Section ordering is code-level, not config-level**: Could be config-driven later, but adding config surface now for an experimental feature is premature. A code-level slice is sufficient for A/B testing.

## Scope Boundaries

**In scope:**
- `internal/prompt/system.go` — section architecture, `SystemPreamble()` rewrite, prompt text rewrite
- `internal/prompt/system_test.go` — test updates
- `internal/prompt/caveman_test.go` — test updates
- `internal/prompt/humanizer.go` — no text changes, but integration with new assembly
- `internal/prompt/humanizer_test.go` — test updates
- `internal/prompt/compaction.go` — if compaction instruction references overlap with preamble changes
- `internal/delegation/agent_type.go` — code/delegate prompt changes to addendum style
- `internal/delegation/bootstrap.go` — addendum passing mechanism
- `internal/delegation/bootstrap_support.go` — if signature changes needed
- `internal/delegation/bootstrap_test.go` — test updates
- `internal/delegation/specialized_tools.go` — if code agent wiring changes
- `internal/prompt/types.go` — `AssemblyOptions` if addendum field needed
- `internal/prompt/source_plan.go` — if preamble step needs addendum awareness

**Out of scope:**
- `explore`, `research`, `plan`, `verify` agent type prompts
- Assembly pipeline mechanics (budget, rendering, message construction)
- Config struct, CLI flags, TUI toggles
- Compaction prompt rewrite (separate concern, unless overlap discovered during implementation)
- Prose compression or economy rewrites of existing instruction text (separate follow-up after testing)
- Tool definition token optimization (separate follow-up)

## Verification Strategy

**Formatter**: `gofmt -w <files>` — cheap, auto-fix
**Imports**: `goimports -w <files>` — cheap, auto-fix
**Lint**: `golangci-lint run ./...` — medium cost
**Vet**: `go vet ./...` — cheap
**Tests**: `go test ./internal/prompt/ ./internal/delegation/ ./internal/agent/` — targeted, cheap
**Broad tests**: `go test ./...` — medium cost
**Build**: `go build ./...` — cheap
**Full check**: `make check` — runs all of the above, medium cost

## Decision Log

| Decision | Rationale | Date |
|---|---|---|
| Option B ordering (delegation first) | Delegation is steiner's core differentiator; primacy position maximizes adherence especially on small models | 2026-06-15 |
| Configurable section ordering via code-level slice | Enables experimentation without config surface; can promote to config later | 2026-06-15 |
| DRY limited to code and delegate agents | Other agent types are sufficiently distinct; sharing would add noise | 2026-06-15 |
| Delegation-agnostic core rules | Core rules must work for both main agent and sub-agents without conditional logic | 2026-06-15 |
| Research: prompt structure for LLM adherence | Informed ordering decision with evidence on attention curves, small model behavior, KV cache | 2026-06-15 |
| code/delegate sub-agents get no addendum | Shared base is sufficient; parent task message provides role framing | 2026-06-15 |
| Promote code-agent-specific rules to main preamble | "Read nearby tests", "don't report completion with failing checks", "quote exact errors", "per-file summary" are useful for main agent too | 2026-06-15 |
| No prose compression in this change | Restructure armature only; test thoroughly before optimizing language | 2026-06-15 |

# Canon drift checks

Go tests catch a downstream instruction file drifting away from the orchestration canon — `delegationInstructions` in `internal/prompt/system.go`. This doc explains what the checks do, what counts as canon and as a "consumer," and what to do when one fails.

Canon prose lives in `internal/prompt/templates/delegation.md.tmpl`, a `text/template` embedded into the binary via `embed.FS`. `delegationInstructions(advisorEnabled)` in `internal/prompt/system.go` executes it: prose is edited in the template file, never in Go string constants.

The specialist roster itself is not hand-written markdown. `internal/prompt/specialists.go` holds it as a typed `[]specialist` slice, and the `## Your sub-agents` table is rendered from that slice — `specialistViews()` projects it into the template data and `delegation.md.tmpl` ranges over it to emit the table rows. The `## Your workflow` advisor step renders only when `advisorEnabled`, which is session-fixed, so output stays deterministic within a session. The table therefore cannot diverge from the roster source, and neither check needs to parse markdown to recover it.

## What the checks are

- **Roster vocabulary check** (`internal/prompt/canon_roster_drift_test.go`, `TestConsumersNameOnlyCurrentSpecialists`). Addresses GitHub issue #445 §3: `verify` was renamed to `sanity_check`, `plan` to `evaluate`, and the generic `delegate` tool was removed, but three skills kept instructing the model to call tools that no longer existed. This check takes the current specialist roster from `specialists` and scans consumer files for backticked tokens in a handful of framed patterns (`` `X` sub-agent(s) ``, `` delegated `X` ``, `` `X` delegation ``, `` `X(` `` tool call). Any framed token not in the current roster is a finding. Runs as part of `go test ./internal/prompt/`.

- **Preamble roster match check** (`internal/delegation/preamble_roster_test.go`, `TestPreambleSpecialistRosterMatchesAgentTypes`). Asserts `prompt.SpecialistNames()` is exactly the registered `AgentType` constants, minus an explicit exclusion list (`vision` is internal-only; `follow_up` is not an `AgentType`) — so a Go rename or addition/removal of a sub-agent type that is not reflected in the roster fails immediately. This direction is detected rather than derived: `internal/prompt` cannot import `internal/delegation` to build the roster from `AgentType` directly, because `internal/delegation/bootstrap.go` imports `internal/prompt`. Runs as part of `go test ./internal/delegation/`.

- **Gated-tool reference check** (`internal/prompt/system_test.go`, `TestDelegationCanonDoesNotNameAdvisorWhenDisabled`). The delegation and advisor preamble sections are gated on separate config flags, so the canon must not name `advisor` in its advisor-disabled variant: the `## Your workflow` advisor step renders only when the advisor is enabled, and the test asserts the disabled variant contains no backticked `advisor` in either the normal or override preamble path. Canon would otherwise point the orchestrator at a tool that is not registered in a `delegation.enabled` + `advisor.enabled: false` session. Runs as part of `go test ./internal/prompt/`.

- **Shared skill block check** (`skills/shared_blocks_test.go`, `TestSharedBlocksAreByteIdenticalAcrossSkills`). The `### Worktree Handling` and `### Pre-Commit Checklist` sections are deliberately duplicated verbatim across `skills/{implement,review,simplify}/SKILL.md`. The fix-delegation bullet list is likewise duplicated between `skills/review/SKILL.md` and `skills/simplify/SKILL.md` (only the bullets — the lead-in and trailing sentence deliberately differ in wording). This is workflow machinery, not canon, so it does not belong in the preamble — but it is the same missed-migration risk, so the test asserts each block still occurs exactly once and byte-identically in every skill that carries it. Each block declares its own skill list; editing one copy means editing every copy plus the literal in the test. Runs as part of `go test ./skills/`.

- **Oneshot/skill shared block check** (`internal/oneshot/prompts_shared_test.go`, `TestSharedBlocksMatchSkillCounterparts`). Pins the four spans that `internal/oneshot/prompts/{plan,review}.md` share byte-identically with `skills/{plan,review}/SKILL.md`: the research triggers, the `plan.yaml` step schema, the review input list, and the review status mapping. Runs as part of `go test ./internal/oneshot/`.

- **Bundled skill budget check** (`internal/prompt/assemble_test.go`, `TestAssembleAllBundledSkillsFit`). Discovers every real embedded skill with `skill.Loader` and `skills.FS`, then enables all discovered names through production `Assemble` with its default policy. Every `ContextSourceSkill` block must fit the shared skill budget without truncation. This check covers the configure skill too, without making it a delegation-canon consumer.

All of them run as part of `make check`.

## Oneshot prompts versus interactive skills

The three `internal/oneshot/prompts/*.md` phase prompts and the interactive skills of the same name were measured paragraph-by-paragraph for identical text. The result:

| Pair | Identical paragraphs | Substantive |
|------|----------------------|-------------|
| `implement` | 2 | 0 — both are bare headings |
| `plan` | 3 | 2 |
| `review` | 10 | 4, in two contiguous runs |

The substantive overlap is pinned by the oneshot/skill shared block check above. Everything else is deliberately divergent: oneshot runs unattended against a plan manifest and writes phase artifacts, while the skills run interactively with a user in the loop, so the two describe genuinely different procedures even where they share vocabulary. This was measured rather than assumed, and the question is considered settled — do not re-derive a fuzzy or approximate duplication matcher over these files. If a new verbatim block appears, add it to the shared block table; if a pinned block should legitimately diverge, remove it from the table and note the reason here.

## What counts as canon

Only `delegationInstructions` in `internal/prompt/system.go`, and the template it renders, `internal/prompt/templates/delegation.md.tmpl`. The other preamble templates — `core_rules.md.tmpl`, `advisor.md.tmpl`, `execution_modes.md.tmpl`, `workflow_approval.md.tmpl`, `sandbox.md.tmpl` — and the agent-type prompt templates in `internal/delegation/templates/` are out of scope. The boundary is drawn at `delegationInstructions` because that's where the observed drift in #445 occurred, and because it has the most distinctive vocabulary (specialist names, routing rules, tool names) to check against.

The compact configuration reference lives only in `skills/configure/SKILL.md`, which owns the bundled skill's self-contained copy. `skills/configure_test.go` verifies real embedded discovery, exactly one reference heading, the 12,288-byte limit, and coverage of reflected `config.Config` schema paths. The bundled skill budget check above ensures every bundled skill fits the shared assembly budget without truncation.

## Consumer files

The roster vocabulary check scans:

```
skills/implement/SKILL.md
skills/review/SKILL.md
skills/simplify/SKILL.md
skills/plan/SKILL.md
skills/pull-request/SKILL.md
internal/oneshot/prompts/*.md
```

## This check just failed — now what

A consumer file references a stale tool or sub-agent name (roster vocabulary check), the roster and the registered `AgentType` constants have diverged (preamble roster match check), or canon names a tool that is not always registered (gated-tool reference check). Fix the consumer file, fix the `specialists` slice, or move the gated tool's mention into that tool's own preamble section — there is no waiver mechanism for any of these checks.

# Canon drift checks

Go tests catch a downstream instruction file drifting away from the orchestration canon — `delegationInstructions` in `internal/prompt/system.go`. This doc explains what the checks do, what counts as canon and as a "consumer," and what to do when one fails.

The specialist roster itself is not hand-written markdown. `internal/prompt/specialists.go` holds it as a typed `[]specialist` slice, and the `## Your specialists` table is rendered from that slice as part of `delegationInstructions` — a flag-aware renderer (`delegationInstructions(advisorEnabled)`) assembled per call from `delegationRole`, the roster table, and `delegationRouting`; `advisorEnabled` is session-fixed, so output stays deterministic within a session. The table therefore cannot diverge from the roster source, and neither check needs to parse markdown to recover it.

## What the checks are

- **Roster vocabulary check** (`internal/prompt/canon_roster_drift_test.go`, `TestConsumersNameOnlyCurrentSpecialists`). Addresses GitHub issue #445 §3: `verify` was renamed to `sanity_check`, `plan` to `evaluate`, and the generic `delegate` tool was removed, but three skills kept instructing the model to call tools that no longer existed. This check takes the current specialist roster from `specialists` and scans consumer files for backticked tokens in a handful of framed patterns (`` `X` sub-agent(s) ``, `` delegated `X` ``, `` `X` delegation ``, `` `X(` `` tool call). Any framed token not in the current roster is a finding. Runs as part of `go test ./internal/prompt/`.

- **Preamble roster match check** (`internal/delegation/preamble_roster_test.go`, `TestPreambleSpecialistRosterMatchesAgentTypes`). Asserts `prompt.SpecialistNames()` is exactly the registered `AgentType` constants, minus an explicit exclusion list (`vision` is internal-only; `follow_up` is not an `AgentType`) — so a Go rename or addition/removal of a sub-agent type that is not reflected in the roster fails immediately. This direction is detected rather than derived: `internal/prompt` cannot import `internal/delegation` to build the roster from `AgentType` directly, because `internal/delegation/bootstrap.go` imports `internal/prompt`. Runs as part of `go test ./internal/delegation/`.

- **Gated-tool reference check** (`internal/prompt/system_test.go`, `TestDelegationCanonDoesNotNameAdvisorWhenDisabled`). The delegation and advisor preamble sections are gated on separate config flags, so the canon must not name `advisor` in its advisor-disabled variant: the `## Your workflow` advisor step renders only when the advisor is enabled, and the test asserts the disabled variant contains no backticked `advisor` in either the normal or override preamble path. Canon would otherwise point the orchestrator at a tool that is not registered in a `delegation.enabled` + `advisor.enabled: false` session. Runs as part of `go test ./internal/prompt/`.

- **Shared skill block check** (`skills/shared_blocks_test.go`, `TestSharedBlocksAreByteIdenticalAcrossSkills`). The `### Worktree Provisioning` and `### Pre-Commit Checklist` sections are deliberately duplicated verbatim across `skills/{implement,review,simplify}/SKILL.md`. The fix-delegation bullet list is likewise duplicated between `skills/review/SKILL.md` and `skills/simplify/SKILL.md` (only the bullets — the lead-in and trailing sentence deliberately differ in wording). This is workflow machinery, not canon, so it does not belong in the preamble — but it is the same missed-migration risk, so the test asserts each block still occurs exactly once and byte-identically in every skill that carries it. Each block declares its own skill list; editing one copy means editing every copy plus the literal in the test. Runs as part of `go test ./skills/`.

- **Oneshot/skill shared block check** (`internal/oneshot/prompts_shared_test.go`, `TestSharedBlocksMatchSkillCounterparts`). Pins the four spans that `internal/oneshot/prompts/{plan,review}.md` share byte-identically with `skills/{plan,review}/SKILL.md`: the research triggers, the `plan.yaml` step schema, the review input list, and the review status mapping. Runs as part of `go test ./internal/oneshot/`.

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

Only `delegationInstructions` in `internal/prompt/system.go` (assembled from `delegationRole`, the rendered roster table, and `delegationRouting`). Other preamble consts — `coreRules`, `advisorInstructions`, `executionModeInstructions`, workflow instructions, `agentPrompts` — are out of scope. The boundary is drawn at `delegationInstructions` because that's where the observed drift in #445 occurred, and because it has the most distinctive vocabulary (specialist names, routing rules, tool names) to check against.

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

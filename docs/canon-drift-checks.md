# Canon drift checks

Go tests catch a downstream instruction file drifting away from the orchestration canon — the `delegationInstructions` const in `internal/prompt/system.go`. This doc explains what the checks do, what counts as canon and as a "consumer," and what to do when one fails.

## What the checks are

- **Roster vocabulary check** (`internal/prompt/canon_roster_drift_test.go`, `TestConsumersNameOnlyCurrentSpecialists`). Addresses GitHub issue #445 §3: `verify` was renamed to `sanity_check`, `plan` to `evaluate`, and the generic `delegate` tool was removed, but three skills kept instructing the model to call tools that no longer existed. This check parses the current specialist/tool roster out of canon and scans consumer files for backticked tokens in a handful of framed patterns (`` `X` sub-agent(s) ``, `` delegated `X` ``, `` `X` delegation ``, `` `X(` `` tool call). Any framed token not in the current roster is a finding. Runs as part of `go test ./internal/prompt/`.

- **Preamble roster match check** (`internal/delegation/preamble_roster_test.go`, `TestPreambleSpecialistRosterMatchesAgentTypes`). Parses the same roster table out of canon and asserts it is exactly the set of registered `AgentType` constants — so a Go rename or addition/removal of a sub-agent type that is not reflected in the canon roster table fails immediately. Runs as part of `go test ./internal/delegation/`.

- **Shared skill block check** (`skills/shared_blocks_test.go`, `TestSharedBlocksAreByteIdenticalAcrossSkills`). The `### Worktree Provisioning` and `### Pre-Commit Checklist` sections are deliberately duplicated verbatim across `skills/{implement,review,simplify}/SKILL.md`. This is workflow machinery, not canon, so it does not belong in the preamble — but it is the same missed-migration risk, so the test asserts each block still occurs exactly once and byte-identically in all three skills. Editing one copy means editing all three plus the literals in the test. Runs as part of `go test ./skills/`.

All three run as part of `make check`.

## What counts as canon

Only `delegationInstructions` in `internal/prompt/system.go`. Other preamble consts — `coreRules`, `advisorInstructions`, `executionModeInstructions`, workflow instructions, `agentPrompts` — are out of scope. The boundary is drawn at `delegationInstructions` because that's where the observed drift in #445 occurred, and because it has the most distinctive vocabulary (specialist names, routing rules, tool names) to check against.

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

A consumer file references a stale tool or sub-agent name (roster vocabulary check), or the canon roster table and the registered `AgentType` constants have diverged (preamble roster match check). Fix the consumer file, or fix the canon roster table, so the two stay in sync — there is no waiver mechanism for either check.

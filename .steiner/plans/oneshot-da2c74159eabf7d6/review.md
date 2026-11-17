# Review

## Status: pass_with_notes

Reviewed `overview.md`, `plan.yaml`, `execution.md`, and the committed changes from `origin/main` (`011ba381`) through implementation head `23bfdba9`. Coverage included #997 retry events, stream state and child isolation; #1050 approval path collection, state, rendering and documentation; and #1001 tool and role-specific prompt guidance. Direct callers, regression tests, policy normalization, and the recorded correction loops were examined. No material review coverage gaps remain.

## Blocking findings

- Long first outside paths could be omitted from approval cards entirely. Fixed in `62b23c4d` with a visible truncated prefix and marker, bounded display, and tests.
- Retry isolation could leave superseded thinking blocks streaming. Fixed in `ffa448e2` and re-reviewed. A resulting test-helper lint failure was fixed in `23bfdba9`.
- No blocking findings remain.

## Non-blocking notes and residual risks

- #997 covers provider retry resets in the root transcript. Runner-level retries can retain abandoned partial text; child transcripts are outside the approved scope. A pre-existing thinking, text, thinking sequence can leave a thinking block streaming. Unbound children share attempt zero.
- #1050 shows normalized requested paths, not symlink-resolved destinations. A symlink can make the actual target differ from the displayed path, including in the existing exec renderer. A follow-up should show both requested and resolved destinations where they differ. Long paths can hide their tail; very narrow terminals can show only a count. Some invisible Unicode format characters survive the existing sanitizer, and exec and TUI differ for paths that sanitize to empty. Decision-only pill path updates are not reached by current producers.
- #1001 shell scope and destination restrictions are prompt rules, not path enforcement by the bash tool. The shared tool description remains conditional for documentors, whose prompt explicitly forbids bash writes. The first-class copy operation and measurement are deferred; bounded `mv` was added by user direction. Parent and code-child test coverage includes a vacuous negative assertion, and the documentor `mkdir -p` allowance is not pinned by a dedicated test.
- The cancelled run's `execution.md` step-5 status and pre-rebase hashes, and its `final-report.json`, are stale. They do not reflect this review. Engine closeout owns final reporting. No PR was found for this branch; no push or PR was attempted in review. The three selected issues remain open and labeled `in-progress`. Review child worktrees were removed; unrelated worktrees were left alone.

## Verification

- `go test ./internal/tui ./internal/agent ./internal/output ./internal/prompt ./internal/tool/builtin`: passed on the combined fixes.
- `go test ./internal/tui`: passed after the thinking fix.
- `GOLANGCI_LINT_CACHE=$(mktemp -d) make check`: passed at `23bfdba9`, including tidy, formatting, imports, build, race tests, vet, lint and vulnerability checks.
- An earlier ordinary `make check` reported stale lint results from deleted worktrees. A fresh lint cache passed. An intermediate check found an actual `unparam` error in the new test helper, fixed before the final pass.

## Advisor sanity check

The final advisor check found no remaining blocker after verifying path normalization. Paths are cleaned and absolute but are not symlink-resolved in the approval payload; the advisor classified this existing data limitation and explicitly marked truncation as non-blocking for the approved display scope. Record both in closeout.

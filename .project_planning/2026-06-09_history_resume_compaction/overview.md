## Request

Plan the fix for GitHub issues:

- `#153` "Compaction result is not stored when it's the last interaction"
- `#154` "Conversation history doesn't preserve tool calls, delegation calls etc"

The user wants an execution-ready implementation plan for making resumed Steiner sessions preserve the durable conversation state correctly. In particular:

- A manual `/compact` result must be persisted immediately, even when it is the final interaction before exiting.
- Resumed conversations must preserve tool-call turns, tool-result turns, and delegation-style turns/results when those turns are valid persisted conversation state.
- Fixes should be minimal, keep package boundaries intact, and include nearby unit/functional tests.

## Overview

This is repo-local work. No external research is required because the bugs are about Steiner's own session persistence, compaction, replay sanitization, and interactive wiring.

The likely implementation surface is:

- `internal/interactive`: manual compaction flow, prompt submit flow, session save/load wiring, and tests proving compaction persists without a subsequent prompt.
- `internal/session`: JSON persistence for `agent.ConversationLineage`, with tests proving tool calls and tool results round-trip through saved sessions.
- `internal/agent`: replay-safe conversation handling and message conversion tests, only if the current sanitization path is stripping valid tool/delegation exchanges during resume.

Initial code inspection found:

- `internal/history.Writer` is only prompt readline history and is not the primary resume path for these issues.
- Saved sessions are represented by `session.Session` with `Lineage agent.ConversationLineage`.
- `interactive.submitPrompt` saves the session after successful model runs, but manual compaction needs the same persistence boundary after it mutates session conversation/lineage.
- `agent.ReplaySafeConversation` intentionally drops orphan tool messages and clears incomplete assistant tool calls, while preserving immediately paired assistant/tool exchanges. The implementation should preserve this safety rule and only change behavior if valid persisted exchanges are being malformed before replay.

High-level approach:

1. Add failing tests that reproduce both issues at the persistence boundary before changing behavior.
2. Ensure manual compaction updates the saved session immediately after successful compaction.
3. Ensure saved and resumed session lineage keeps complete assistant tool calls, tool result messages, and delegation-style messages/results without cosmetic loss.
4. Keep provider replay safety intact: invalid orphan/incomplete tool transcripts should still be sanitized before model replay, but saved conversation state should not lose valid tool/delegation records.
5. Update documentation only if implementation changes user-visible session/history behavior. These issues do not appear to require README or docs maintenance under the repo's current maintenance rules unless a new top-level behavior or config/tool/delegation contract changes.

## Verification Strategy

Repository instructions require Go changes to be formatted and checked before finalizing.

Likely commands:

- Cheap targeted formatter after Go edits: `gofmt -w <files>`
- Cheap targeted imports after Go edits: `goimports -w <files>`
- Cheap targeted tests while iterating: `go test ./internal/interactive ./internal/session ./internal/agent`
- Targeted single-test runs where useful: `go test ./internal/interactive -run TestName`, `go test ./internal/session -run TestName`, `go test ./internal/agent -run TestName`
- Medium build check: `go build ./...`
- Expensive required final check: `make check`

`make check` expands to tidy check, format check, imports check, binary build, full tests, race tests, vet, lint, and vulnerability check. It may require installed tools: `goimports`, `golangci-lint`, and `govulncheck`; if missing, run or report `make install-check-tools`.

Safe fix mode:

- Prefer `gofmt -w` and `goimports -w` for edited Go files rather than check-only format commands during implementation.
- Do not run destructive git commands.
- Do not touch the existing untracked `.project_planning/2026-06-09-12-28-slop-detector/progress.yaml`.

## Decision Log

- Treat issues `#153` and `#154` as one planning bundle because both concern saved session durability across compaction/resume.
- Skip external research because the behavior is owned by this repo and can be understood from nearby code and tests.
- Do not plan changes to `internal/history.Writer` unless implementation proves prompt history is incorrectly involved; current evidence points to `internal/session` and `internal/interactive`.
- Preserve replay sanitization for provider validity; the fix should distinguish durable saved conversation fidelity from replay-safe provider normalization.
- Require tests before implementation changes where practical so regressions are pinned to the reported bugs.

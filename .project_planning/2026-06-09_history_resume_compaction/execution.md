active_branch: cl/2026-06-09_history_resume_compaction

verification_strategy:
- gofmt -w <edited-go-files>
- goimports -w <edited-go-files>
- go test ./internal/interactive ./internal/session ./internal/agent
- make check

repo_state:
- tracked_worktree_clean: true
- untracked_exception: .project_planning/2026-06-09-12-28-slop-detector/ left untouched per planner note

steps:
- id: step-1
  status: implemented
- id: step-2
  status: complete
- id: step-3
  status: complete
- id: step-4
  status: complete

sub_agents:
- step_id: step-1
  profile: gpt-5.4-mini
  tier: cheaper_than_current_runtime
  delegate_profile: none
- step_id: step-2
  profile: gpt-5.4-mini
  tier: cheaper_than_current_runtime
  delegate_profile: none
- step_id: step-3
  profile: gpt-5.4
  tier: more_capable_than_previous_worker_tier
  delegate_profile: none
verification_results:
- step: step-1
  command: go test ./internal/interactive ./internal/session ./internal/agent
  result: internal/session and internal/agent passed; internal/interactive failed in new regression tests for compaction persistence and resumed assistant tool-call display
- step: step-2
  command: go test ./internal/interactive -run 'Compaction|Session'
  result: passed
- step: step-2
  command: go test ./internal/interactive ./internal/session
  result: passed
- step: step-3
  command: go test ./internal/session ./internal/interactive ./internal/agent
  result: passed
- step: step-4
  command: gofmt -w internal/interactive/compaction.go internal/interactive/compaction_test.go internal/interactive/session.go internal/interactive/session_test.go internal/session/store.go internal/session/store_test.go internal/agent/message_convert_test.go
  result: passed
- step: step-4
  command: goimports -w internal/interactive/compaction.go internal/interactive/compaction_test.go internal/interactive/session.go internal/interactive/session_test.go internal/session/store.go internal/session/store_test.go internal/agent/message_convert_test.go
  result: passed
- step: step-4
  command: go test ./internal/interactive ./internal/session ./internal/agent
  result: passed
- step: step-4
  command: make check
  result: passed
deviations:
- treated unrelated untracked planning directory as out-of-scope executor exception while keeping feature branch and tracked files clean
 - documentation left unchanged because the implementation only fixes session durability and legacy resume fidelity without changing documented top-level behavior or maintenance-triggered interfaces
blockers: []
manual_verification: []
review_handoff: ready

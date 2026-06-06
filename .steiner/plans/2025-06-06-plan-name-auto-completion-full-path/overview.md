## Request

Issue #91: Plan name auto-completion in the TUI should display and insert the full relative path (e.g., `.steiner/plans/2025-06-06-my-plan`) instead of only the final folder name (e.g., `2025-06-06-my-plan`).

## Overview

The plan picker overlay (`internal/tui/plan_picker.go`) reads subdirectory entries from `.steiner/plans/` and currently stores only the directory basename in `allNames`. When the user selects a plan, the basename is appended to the input value. The model (and the user) therefore sees only `2025-06-06-my-plan` instead of the complete `.steiner/plans/2025-06-06-my-plan` path.

The fix is to prefix each discovered plan directory with `.steiner/plans/` when populating `allNames` in `Open()`. This single change propagates through filtering, display, and selection insertion automatically because the rest of the overlay operates on the string values in `allNames`/`candidates` without further path manipulation.

The test file `internal/tui/plan_picker_test.go` will need updates to expect full relative paths in assertions.

## Verification Strategy

| Command | Scope | Cost |
|---------|-------|------|
| `go test ./internal/tui/... -run TestPlanPicker` | Plan picker unit tests | Cheap |
| `go build ./...` | Compile all packages | Cheap |
| `go vet ./...` | Static analysis | Cheap |
| `make check` | Full CI suite (tidy, fmt, imports, build, test, race, vet, lint, vuln) | Expensive |

Targeted verification should pass before running the full `make check`.

## Decision Log

1. **No research needed.** The change is entirely repo-local, bounded to one TUI overlay, with no external dependencies or APIs.
2. **No docs update needed.** This is a UX bug fix inside an existing TUI feature; no user-facing documentation references the current broken behaviour.

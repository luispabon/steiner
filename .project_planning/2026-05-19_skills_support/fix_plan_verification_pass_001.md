# Fix Plan — Verification Pass 001

## Failures from `golangci-lint run ./...`

### 1. `internal/skill/loader.go:33` — gocyclo
```
cyclomatic complexity 16 of func (Loader).Discover is high (> 15)
```
**Fix:** Extract the inner per-entry scanning logic into a helper function, e.g. `discoverSkillEntry(root string, name string, seen map[string]bool, source string) (Skill, bool, error)` or similar. This keeps the outer loop clean and reduces the cyclomatic complexity of `Discover` itself.

### 2. `internal/tui/input.go:32` — gocyclo
```
cyclomatic complexity 16 of func parseInputWithSkills is high (> 15)
```
**Fix:** Extract one or more sub-cases from `parseInputWithSkills` into helpers. For example, extract the `/skill` toggle handling into `parseSkillToggle(trimmed string, enabledSkills map[string]bool) (inputAction, bool)`, and the `/skillname` invocation detection into `parseSkillInvocation(trimmed string, skillNames []string) (inputAction, bool)`.

### 3. `internal/tui/input.go:28` — unparam
```
parseInput - enabledSkills always receives nil
```
**Fix:** Read all call sites of `parseInput` in `internal/tui/`. If all callers always pass nil, change `parseInput(value string) inputAction` (remove the parameter), and call `parseInputWithSkills(value, nil, nil)` internally. If callers do pass a real map, keep the parameter but verify that the linter is not confused by type. Do NOT remove the `enabledSkills` parameter from `parseInputWithSkills` — it's used for `/skill -name` deactivation.

## Scope
Only touch:
- `internal/skill/loader.go`
- `internal/tui/input.go`

Do NOT change signatures of `Discover`, `parseInputWithSkills`, or any public API unless the `unparam` fix explicitly requires it.

## Verification
After fixing:
1. `golangci-lint run ./internal/skill/... ./internal/tui/...` must pass (no lint errors)
2. `go test ./internal/skill/... ./internal/tui/...` must pass
3. `go build ./...` must compile

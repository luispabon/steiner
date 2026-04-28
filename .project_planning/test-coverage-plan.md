# Test Coverage Audit & Plan: steiner

## Executive Summary

`steiner` is a Go 1.24 local-first coding agent with ~147 source files and 35 test files (~24% test-file ratio). Current overall line coverage is **56.4%** across 707 functions. The most significant gaps are in the core tools binary (`cmd/steiner-core-tools`, 7 files, 0 tests), the history writer, provider HTTP layer, config patch/validation logic, and the TUI theme system. **208 functions (29%) have zero coverage**, and another **192 functions (27%) are below the 80% threshold**. The recommended mix is 65% unit/unit-with-mock, 25% functional, and 10% integration. Estimated effort is **medium-large** (roughly 15-20 discrete tasks).

## Coverage Baseline

| File / Module | Current Status | Risk | Recommended Test Type(s) |
|---|---|---|---|
| `cmd/steiner-core-tools/` (7 files) | untested | critical | unit, functional |
| `internal/history/writer.go` | untested | critical | unit |
| `internal/config/validate.go` | partial (50%) | critical | unit |
| `internal/config/patch.go` | partial (0-70%) | critical | unit |
| `internal/provider/openai_compat.go` | partial (0-69%) | critical | unit-with-mock |
| `internal/provider/openai_stream.go` | partial (21-78%) | critical | unit |
| `internal/delegation/tool.go` | partial (0-17%) | critical | unit-with-mock |
| `internal/tool/policy.go` | partial (0-77%) | critical | unit |
| `internal/tool/executor.go` | partial (65%) | critical | unit-with-mock |
| `cmd/steiner/exec.go` | untested | critical | functional |
| `cmd/steiner/runner.go` | untested | critical | functional |
| `cmd/steiner/runtime.go` | untested | critical | functional |
| `internal/agent/message_convert.go` | untested | high | unit |
| `internal/agent/text_util.go` | partial (0-22%) | high | unit |
| `internal/provider/token_estimator.go` | partial (0-87%) | high | unit |
| `internal/output/stream.go` | partial (0-82%) | high | unit |
| `internal/output/log.go` | partial (0-75%) | high | unit |
| `internal/output/file_log.go` | partial (0-86%) | high | unit |
| `internal/config/load.go` | partial (60-77%) | high | unit |
| `internal/config/env.go` | partial (64%) | high | unit |
| `internal/config/duration.go` | partial (0-75%) | high | unit |
| `internal/skill/loader.go` | partial (61-86%) | high | unit |
| `internal/prompt/context.go` | partial (18-74%) | high | unit |
| `internal/tui/theme/` (4 files) | untested | medium | unit |
| `internal/tui/prefs/prefs.go` | untested | medium | unit |
| `internal/tui/help_test.go` | stub-only (15 lines) | low | unit |
| `internal/tui/input_test.go` | stub-only (23 lines) | low | unit |

## Key Findings

- **The core tools binary is entirely untested.** `cmd/steiner-core-tools/` contains the read, write, edit, glob, search, and bash handlers that the agent invokes on every run. Bugs here directly affect file-system safety and command execution.
- **Config validation and patch application have large uncovered branches.** `validate()` and the `apply*Patch` family cover sub-agent, tool, project context, limits, and approval overrides that are rarely hit in existing tests.
- **Provider streaming logic lacks edge-case coverage.** `extractThinkingDelta`, SSE event parsing, and tool-call finalization in the stream decoder are mostly uncovered, yet they handle live model output.
- **No functional tests exist for the CLI entry points.** `exec.go`, `runner.go`, and `runtime.go` wire the entire application together but have zero coverage.
- **Two TUI test files are stubs.** `help_test.go` (15 lines) and `input_test.go` (23 lines) contain test function signatures but no meaningful assertions.

## Test Infrastructure Assessment

**Existing:**
- Standard Go `testing` package is used throughout.
- Table-driven tests exist in `internal/agent`, `internal/config`, `internal/output`, and `internal/delegation`.
- `testdata/` directory is used for fixtures.
- No external mock/assert library is in `go.mod`; tests rely on manual `if got != want` patterns.

**Needed:**
- A small `httptest` harness for provider integration tests.
- Temp-directory helpers for history, file-log, and core-tools tests (can be inline `t.TempDir()`).
- A mock `provider.Provider` implementation for functional CLI tests (define a minimal interface-fulfilling struct in `cmd/steiner` tests).
- No new dependencies are required; the standard library covers mocks via interfaces and `httptest`.

## Implementation Plan

### Task 1: Core tools main orchestration and envelope helpers (S)

**Test type**: unit
**Risk**: critical
**Target**: `cmd/steiner-core-tools/main.go`
**New test file**: `cmd/steiner-core-tools/main_test.go`
**Depends on**: none

**What is untested**: `run()`, `decodeRequest()`, `writeEnvelope()`, and `toEnvelopeError()` have 0% coverage. This is the dispatch layer for every core tool invocation.

**What to test**:
- Missing subcommand returns usage error envelope and exit code 1.
- Unknown subcommand returns usage error with the command name.
- Stdin read failure returns stdin error envelope.
- Handler success returns `OK: true` envelope and exit code 0.
- Handler error returns `OK: false` envelope with the correct error kind.
- `decodeRequest` with empty payload returns zero value.
- `decodeRequest` with invalid JSON returns `invalid_input` error.
- `toEnvelopeError` wraps plain errors as `kind: "internal"` and passes through `JSONEnvelopeError` unchanged.

**Mocking / test infrastructure needed**: None; use `bytes.Buffer` for stdin/stdout/stderr.

**Verification**: `go test ./cmd/steiner-core-tools/... -coverprofile=coverage.out && go tool cover -func=coverage.out`

---

### Task 2: Core tools bash handler and working-directory resolution (M)

**Test type**: unit-with-mock
**Risk**: critical
**Target**: `cmd/steiner-core-tools/bash.go`
**New test file**: `cmd/steiner-core-tools/bash_test.go`
**Depends on**: none

**What is untested**: `runBash` and `resolveWorkingDir` have 0% coverage. This executes arbitrary shell commands.

**What to test**:
- Empty command returns `invalid_input` error.
- Command that succeeds returns stdout, stderr, exit code 0.
- Command that fails returns exit code, stdout, stderr, and `command_failed` error.
- `resolveWorkingDir` with empty cwd returns current working directory.
- `resolveWorkingDir` with relative path joins to base.
- `resolveWorkingDir` with absolute path returns cleaned path.

**Mocking / test infrastructure needed**: For `runBash`, use safe built-in commands (`echo`, `false`) in a temp dir. Do not test destructive commands.

**Verification**: `go test ./cmd/steiner-core-tools/... -run TestBash`

---

### Task 3: Core tools edit handler and path resolution (M)

**Test type**: unit
**Risk**: critical
**Target**: `cmd/steiner-core-tools/edit.go`
**New test file**: `cmd/steiner-core-tools/edit_test.go`
**Depends on**: none

**What is untested**: `runEdit` and `resolveEditablePath` have 0% coverage. This performs string replacement on files.

**What to test**:
- Missing path or old snippet returns `invalid_input`.
- File not found returns `edit_error`.
- Old snippet not found returns `edit_error` with `occurrences: 0`.
- Multiple occurrences returns `edit_error` with `occurrences > 1`.
- Successful replacement writes updated content and returns metadata.
- `resolveEditablePath` rejects paths outside the current working directory (`..` prefix).
- `resolveEditablePath` resolves relative paths against cwd.

**Mocking / test infrastructure needed**: `t.TempDir()` for temporary files.

**Verification**: `go test ./cmd/steiner-core-tools/... -run TestEdit`

---

### Task 4: Core tools read, write, glob, and search handlers (M)

**Test type**: unit-with-mock
**Risk**: critical
**Target**: `cmd/steiner-core-tools/read.go`, `write.go`, `glob.go`, `search.go`
**New test files**: `read_test.go`, `write_test.go`, `glob_test.go`, `search_test.go`
**Depends on**: none

**What is untested**: All four handlers are 0% covered. They are the read-only and file-mutation surface of the agent.

**What to test**:
- **read**: Missing path returns `invalid_input`; missing file returns `read_error`; successful read returns contents.
- **write**: Missing path returns `invalid_input`; success creates directories and writes file; returns bytes written.
- **glob**: Missing pattern returns `invalid_input`; successful glob returns sorted matches.
- **search**: Missing query returns `invalid_input`; successful search returns matches with path/line/text; respects `maxSearchResults` limit; skips binary files.

**Mocking / test infrastructure needed**: `t.TempDir()` with fixture files for read/write/glob/search.

**Verification**: `go test ./cmd/steiner-core-tools/... -run 'TestRead|TestWrite|TestGlob|TestSearch'`

---

### Task 5: History writer persistence and trimming (M)

**Test type**: unit
**Risk**: critical
**Target**: `internal/history/writer.go`
**New test file**: `internal/history/writer_test.go`
**Depends on**: none

**What is untested**: `NewWriter`, `Record`, `TrimAfterAppend`, `Load`, and `Close` have 0% coverage. This persists user prompts to disk.

**What to test**:
- `NewWriter` creates directories and file.
- `Record` ignores empty prompts.
- `Record` writes RFC3339-tab-escaped lines and syncs.
- `TrimAfterAppend` with `max <= 0` is a no-op.
- `TrimAfterAppend` truncates file to last N lines.
- `Load` reads prompts and unescapes `\t` and `\n`.
- `Load` skips malformed lines.
- `Close` closes the file and is idempotent.

**Mocking / test infrastructure needed**: `t.TempDir()`.

**Verification**: `go test ./internal/history/... -coverprofile=coverage.out`

---

### Task 6: Config validation exhaustive branches (M)

**Test type**: unit
**Risk**: critical
**Target**: `internal/config/validate.go`
**New test file**: `internal/config/validate_test.go`
**Depends on**: none

**What is untested**: `validate()` is only 50% covered. Many error branches (model type checks, compaction bounds, sub-agent limits, tool timeouts, logging level) are not exercised.

**What to test**:
- Valid default config passes.
- Empty model, bad parallelism, missing models fail.
- Model with unsupported type, empty base_url, empty model identifier, bad token counts fail.
- Compaction safety margin < 0, summary max < 1, summary > completion tokens fail.
- Limits with zero/negative values fail.
- Approval mode invalid or empty fails.
- Sub-agent enabled with bad limits fails.
- Project context max tokens < 1 fails.
- Logging level invalid or empty file path fails.
- Tool with empty name, empty exec, zero timeout, or invalid approval mode fails.

**Mocking / test infrastructure needed**: None.

**Verification**: `go test ./internal/config/... -run TestValidate -coverprofile=coverage.out`

---

### Task 7: Config patch application for limits, approval, sub-agent, tools, and project context (M)

**Test type**: unit
**Risk**: critical
**Target**: `internal/config/patch.go`
**New test file**: `internal/config/patch_test.go`
**Depends on**: none

**What is untested**: `applyLimitsPatch` (46%), `applyApprovalPatch` (43%), `applySubAgentPatch` (0%), `applyToolPatch` (0%), and `applyProjectContextPatch` (0%) are uncovered.

**What to test**:
- Each patch function applies non-nil fields and leaves existing fields untouched.
- `applyLimitsPatch` handles `ToolTimeouts` map creation and merge.
- `applyApprovalPatch` handles `Overrides` map creation and merge.
- `applySubAgentPatch` copies `AllowedTools` slice (no shared backing array).
- `applyToolPatch` copies `Parameters` and `Constraints` maps.
- `applyProjectContextPatch` copies `ExtraFiles` and `IgnoreFiles` slices.

**Mocking / test infrastructure needed**: None.

**Verification**: `go test ./internal/config/... -run TestPatch -coverprofile=coverage.out`

---

### Task 8: Tool policy path validation and preview (M)

**Test type**: unit
**Risk**: critical
**Target**: `internal/tool/policy.go`
**New test file**: `internal/tool/policy_test.go`
**Depends on**: none

**What is untested**: `ensureAllowed` (61.5%), `previewToolInput` (0%), `pathWithinRoot` (66.7%), and `normalizePolicyPath` (75%) have gaps. This is the security boundary for file access.

**What to test**:
- `ResolvePath` blocks paths outside project root when `project_root_only` is true.
- `ResolvePath` allows additional `writable_paths`.
- `ResolvePath` blocks `blocked_paths` even if otherwise allowed.
- `ensureAllowed` rejects paths resolving to `..` outside root.
- `previewToolInput` extracts path from known tool inputs.
- `pathWithinRoot` correctly handles relative vs absolute paths.
- `normalizePolicyPath` cleans and expands paths.

**Mocking / test infrastructure needed**: None.

**Verification**: `go test ./internal/tool/... -run TestPolicy -coverprofile=coverage.out`

---

### Task 9: Tool executor error paths and subprocess handling (M)

**Test type**: unit-with-mock
**Risk**: critical
**Target**: `internal/tool/executor.go`
**New test file**: `internal/tool/executor_test.go` (extend existing)
**Depends on**: Task 8

**What is untested**: `Execute` is 64.8% covered. Context cancellation, approval denial, timeout expiry, policy violations, and subprocess error paths are largely missing.

**What to test**:
- Approval denied returns immediate error.
- Context cancellation during execution returns context error.
- Timeout exceeded returns timeout error.
- Policy violation (bad path) is caught before execution.
- Subprocess non-zero exit code is captured correctly.
- `exitCodeFromError` extracts exit codes from `*exec.ExitError`.
- `normalizeExecutionRoot` handles empty and relative roots.

**Mocking / test infrastructure needed**: Mock `ApprovalResponder` (simple struct implementing the interface). Use `context.WithTimeout` and `context.WithCancel` for timeout/cancellation tests.

**Verification**: `go test ./internal/tool/... -run TestExecutor -coverprofile=coverage.out`

---

### Task 10: Provider OpenAI compatibility validation and request building (M)

**Test type**: unit-with-mock
**Risk**: critical
**Target**: `internal/provider/openai_compat.go`
**New test file**: `internal/provider/openai_compat_test.go`
**Depends on**: none

**What is untested**: `NewOpenAICompat` (69%), `SupportsUsageStats` (0%), `readErrorResponse` (66.7%), `marshalRequest` (75%), and `chatCompletionsURL` (100% but worth regression tests) have gaps.

**What to test**:
- `NewOpenAICompat` returns errors for empty base URL, invalid URL, empty model, nil scheduler.
- `NewOpenAICompat` uses default HTTP client when none provided.
- `readErrorResponse` returns status-only error when body is empty.
- `readErrorResponse` includes body content when present.
- `marshalRequest` produces correct JSON structure and handles `stream` flag.
- `chatCompletionsURL` appends `/chat/completions` correctly with/without trailing slash.

**Mocking / test infrastructure needed**: None.

**Verification**: `go test ./internal/provider/... -run TestOpenAICompat -coverprofile=coverage.out`

---

### Task 11: Provider streaming SSE parser and thinking extraction (M)

**Test type**: unit
**Risk**: critical
**Target**: `internal/provider/openai_stream.go`
**New test file**: `internal/provider/openai_stream_test.go`
**Depends on**: none

**What is untested**: `extractThinkingDelta` (21.4%), `readSSEEvent` (60%), `flushStreamState` (78.6%), and `finalizeToolCalls` (78.6%) have uncovered branches.

**What to test**:
- `readSSEEvent` parses single and multi-line data events.
- `readSSEEvent` handles `[DONE]` event.
- `readSSEEvent` handles EOF after empty stream.
- `readSSEEvent` ignores non-data lines.
- `extractThinkingDelta` returns thinking text from structured content arrays (`thinking` and `thinking_delta` types).
- `extractThinkingDelta` returns empty string for plain string content.
- `finalizeToolCalls` parses valid JSON arguments.
- `finalizeToolCalls` returns error for malformed JSON arguments.
- `finalizeToolCalls` handles empty argument strings.
- `flushStreamState` emits done chunk with usage and finish reason.
- `flushStreamState` returns nil when nothing was seen.

**Mocking / test infrastructure needed**: None; feed string readers to `decodeChatStream` and `readSSEEvent`.

**Verification**: `go test ./internal/provider/... -run TestOpenAIStream -coverprofile=coverage.out`

---

### Task 12: Delegation tool handler and limit parsing (M)

**Test type**: unit-with-mock
**Risk**: critical
**Target**: `internal/delegation/tool.go`
**New test file**: `internal/delegation/tool_test.go`
**Depends on**: none

**What is untested**: `DelegateToolDef` (0%) and `NewDelegateHandler` (16.7%) are barely covered. This spawns sub-agents.

**What to test**:
- `DelegateToolDef` returns a `ToolDef` with correct name, description, and schema.
- Handler returns error when `task` is empty.
- Handler parses `max_turns` from float64 input.
- Handler parses `timeout` duration string correctly and ignores invalid values.
- Handler applies limit overrides via `ApplyOverrides`.
- Handler generates a unique `agentID`.

**Mocking / test infrastructure needed**: Mock `AgentRunner` and `output.EventSink` interfaces for the handler closure.

**Verification**: `go test ./internal/delegation/... -run TestTool -coverprofile=coverage.out`

---

### Task 13: Agent message conversion and context assembly (M)

**Test type**: unit
**Risk**: high
**Target**: `internal/agent/message_convert.go`
**New test file**: `internal/agent/message_convert_test.go`
**Depends on**: none

**What is untested**: `toProviderMessages`, `fromProviderMessages`, `toProviderMessage`, `fromProviderMessage`, `assemblyOptions`, `toPromptContext`, `fromPromptContext`, and `LastAssistantMessage` have 0% coverage.

**What to test**:
- `toProviderMessages` maps `MessageRoleSummary` to `provider.MessageRoleSystem`.
- `toProviderMessage` clones tool calls and arguments deeply.
- `fromProviderMessage` reverses the mapping correctly.
- `LastAssistantMessage` finds the last assistant message iterating from the end.
- `LastAssistantMessage` returns `false` when no assistant message exists.
- `assemblyOptions` prefers `Lineage.SummaryPrefixStrippedMessages` over `Conversation`.
- `toPromptContext` and `fromPromptContext` round-trip all fields including `ActiveFocus` pointer.

**Mocking / test infrastructure needed**: None.

**Verification**: `go test ./internal/agent/... -run TestMessageConvert -coverprofile=coverage.out`

---

### Task 14: Agent text utilities (S)

**Test type**: unit
**Risk**: high
**Target**: `internal/agent/text_util.go`
**New test file**: `internal/agent/text_util_test.go`
**Depends on**: none

**What is untested**: `summarizeConversationMessages` (0%), `firstMessageContentByRole` (0%), and `countTurns` (22%) are uncovered.

**What to test**:
- `summarizeConversationMessages` returns `"none recorded"` for empty slice.
- `summarizeConversationMessages` respects `maxMessages` and joins with `" | "`.
- `firstMessageContentByRole` skips empty content and returns first match.
- `countTurns` returns 1 when there are no user messages.
- `countTurns` counts only `MessageRoleUser` messages.

**Mocking / test infrastructure needed**: None.

**Verification**: `go test ./internal/agent/... -run TestTextUtil -coverprofile=coverage.out`

---

### Task 15: Token estimator untested entry points (M)

**Test type**: unit
**Risk**: high
**Target**: `internal/provider/token_estimator.go`
**New test file**: `internal/provider/token_estimator_test.go` (extend existing)
**Depends on**: none

**What is untested**: `EstimateMessageTokens` (0%), `EstimateToolSpecTokens` (0%), `RequestOverheadTokens` (0%), `UsageTokenCount` (0%), and `encodingNameForModel` (0%) have no coverage.

**What to test**:
- `EstimateMessageTokens` returns a positive count for a message with content.
- `EstimateToolSpecTokens` returns a positive count for a tool spec.
- `RequestOverheadTokens` returns a baseline overhead.
- `UsageTokenCount` extracts prompt and completion tokens from usage stats.
- `encodingNameForModel` maps known models to correct encoders and falls back to a default.

**Mocking / test infrastructure needed**: None.

**Verification**: `go test ./internal/provider/... -run TestTokenEstimator -coverprofile=coverage.out`

---

### Task 16: Output infrastructure gaps (M)

**Test type**: unit
**Risk**: high
**Target**: `internal/output/stream.go`, `log.go`, `file_log.go`
**New test files**: extend `stream_test.go`, `log_test.go`, `file_log_test.go`
**Depends on**: none

**What is untested**:
- `stream.go`: `NewStream` (0%), `Println` (0%), `Printf` (0%), `Render` (0%), `WriteAssistant` (0%), `Themed` (0%).
- `log.go`: `SetupLogger` (0%), `parseLevel` (0%).
- `file_log.go`: `NewMultiSink` (0%), `Emit` on `MultiSink` (0%).

**What to test**:
- `NewStream` creates a stream with a renderer attached.
- `Println`, `Printf`, `Render`, `WriteAssistant`, `WriteAssistantChunk`, `FinishAssistant`, `Themed` delegate to the renderer.
- `SetupLogger` sets the correct `slog.Level` for trace/debug/info/warn/error.
- `parseLevel` defaults to info for unknown levels.
- `NewFileLogSink` rejects empty path and creates directories.
- `NewMultiSink` filters nil sinks, returns single sink directly, and fans out events.
- `FileLogSink.Emit` handles `UserInputEvent` and default payload marshaling.

**Mocking / test infrastructure needed**: `bytes.Buffer` as `io.Writer` for stream tests. `t.TempDir()` for file log tests.

**Verification**: `go test ./internal/output/... -coverprofile=coverage.out`

---

### Task 17: Config loading, environment expansion, and duration serialization (M)

**Test type**: unit
**Risk**: high
**Target**: `internal/config/load.go`, `env.go`, `duration.go`
**New test files**: extend existing tests
**Depends on**: none

**What is untested**:
- `load.go`: `normalizePaths` (60%), `environMap` (0%).
- `env.go`: `expandEnvText` edge cases (63.6%), `applyEnvOverrides` error paths (63.3%).
- `duration.go`: `Duration` (0%), `String` (0%), `MarshalYAML` (0%), `UnmarshalYAML` (0%).

**What to test**:
- `expandEnvText` handles `$VAR`, `${VAR}`, `${VAR:-default}`, `$$` escape, invalid `${` unclosed, and non-identifier names.
- `applyEnvOverrides` returns error for invalid integer env vars.
- `environMap` skips malformed entries without `=`.
- `normalizePaths` expands `~` to home dir, leaves absolute paths alone, and ignores empty paths.
- `Duration` MarshalYAML/UnmarshalYAML round-trip.
- `Duration.String` produces human-readable output.

**Mocking / test infrastructure needed**: None.

**Verification**: `go test ./internal/config/... -coverprofile=coverage.out`

---

### Task 18: Skill loader edge cases (S)

**Test type**: unit
**Risk**: high
**Target**: `internal/skill/loader.go`
**New test file**: `internal/skill/loader_test.go` (extend existing)
**Depends on**: none

**What is untested**: `Discover` (61.5%) and `Load` (64.3%) have uncovered error branches.

**What to test**:
- `Discover` returns error when root dir does not exist.
- `Discover` skips files without `.md` extension.
- `Load` returns error for missing file.
- `Load` extracts name from frontmatter correctly.
- `validateSkillName` rejects empty and invalid names.

**Mocking / test infrastructure needed**: `t.TempDir()` with fixture markdown files.

**Verification**: `go test ./internal/skill/... -run TestLoader -coverprofile=coverage.out`

---

### Task 19: TUI theme color math, registry, and preferences (M)

**Test type**: unit
**Risk**: medium
**Target**: `internal/tui/theme/oklch.go`, `registry.go`, `steiner.go`, `theme.go`; `internal/tui/prefs/prefs.go`
**New test files**: `internal/tui/theme/oklch_test.go`, `registry_test.go`, `internal/tui/prefs/prefs_test.go`
**Depends on**: none

**What is untested**: The entire `theme` package (4 files, 0 tests) and `prefs` package (1 file, 0 tests).

**What to test**:
- `OklchToHex` produces expected hex for known OKLCH values (e.g., white, black, red).
- `blendHex` blends two known hex colors correctly.
- `hexToRGB` parses 6-digit hex and returns 0,0,0 for invalid lengths.
- `Register` and `Get` store and retrieve themes; `Get` falls back to `"steiner"`.
- `Default` returns the first registered theme.
- `DefaultPrefs` returns amber accent and `ShowThinking: true`.
- `Load` returns defaults when file is missing.
- `Load` returns defaults and error when YAML is malformed.
- `Save` creates directories and writes YAML.
- `Save`/`Load` round-trip preserves values.

**Mocking / test infrastructure needed**: `t.TempDir()` for prefs tests.

**Verification**: `go test ./internal/tui/... -coverprofile=coverage.out`

---

### Task 20: Functional test - exec mode end-to-end (L)

**Test type**: functional
**Risk**: critical
**Target**: `cmd/steiner/exec.go`, `cmd/steiner/runner.go`, `cmd/steiner/runtime.go`
**New test file**: `cmd/steiner/exec_test.go`
**Depends on**: Tasks 5, 6, 7, 9, 10

**What is untested**: The `runExecMode` and `cliRunner.Run` functions have 0% coverage. They wire provider, executor, runner, and output together.

**What to test**:
- Exec mode with a prompt argument runs a single turn and returns the assistant reply.
- Exec mode with no arguments reads prompt from stdin.
- Exec mode with empty prompt returns error.
- Max turns flag overrides config default.
- Signal interruption cancels the run context.
- `cliRunner.Run` returns diagnostics for retained events.

**Mocking / test infrastructure needed**:
- Mock `provider.Provider` that returns a fixed `ChatResponse`.
- Temp directory for config, working dir, and history.
- `cobra.Command` with overridden stdin/stdout/stderr.

**Verification**: `go test ./cmd/steiner/... -run TestExecMode -coverprofile=coverage.out`

---

### Task 21: Functional test - config and tools commands (M)

**Test type**: functional
**Risk**: high
**Target**: `cmd/steiner/commands.go`
**New test file**: `cmd/steiner/commands_test.go`
**Depends on**: Task 17

**What is untested**: `newConfigCommand`, `newToolsCommand`, `newSkillsCommand` are only exercised through `main_test.go` smoke tests.

**What to test**:
- `config` command prints resolved YAML config to stdout.
- `tools` command lists configured tool names.
- `skills` command lists discovered skill names.
- `version` command prints the version string.
- `renderNames` handles empty and non-empty name slices.

**Mocking / test infrastructure needed**:
- `cobra.Command` with `bytes.Buffer` for OutOrStdout.
- Temp config file for `config` command.

**Verification**: `go test ./cmd/steiner/... -run TestCommands -coverprofile=coverage.out`

---

### Task 22: Integration test - provider HTTP round-trip (M)

**Test type**: integration
**Risk**: high
**Target**: `internal/provider/openai_compat.go`
**New test file**: `internal/provider/integration_test.go`
**Depends on**: Task 11

**What is untested**: `ChatCompletion` and `StreamChatCompletion` make real HTTP calls; no integration tests exist.

**What to test**:
- `ChatCompletion` sends correct JSON body and Authorization header, parses response correctly.
- `ChatCompletion` returns error on 4xx/5xx status.
- `StreamChatCompletion` parses SSE stream and emits chunks correctly.
- `StreamChatCompletion` propagates HTTP errors through the chunk channel.

**Mocking / test infrastructure needed**:
- `httptest.Server` with handlers that return valid OpenAI JSON and SSE responses.
- `httptest.Server` handler that returns 500 error.

**Verification**: `go test ./internal/provider/... -run TestIntegration -coverprofile=coverage.out`

---

## Coverage Target

- **Business logic packages** (`internal/agent`, `internal/config`, `internal/tool`, `internal/provider`, `internal/delegation`): **80% line coverage minimum**. These handle security boundaries, config correctness, and model communication.
- **CLI entry points** (`cmd/steiner`, `cmd/steiner-core-tools`): **70% line coverage minimum**. Functional tests should cover the happy path and main error paths.
- **Output and TUI** (`internal/output`, `internal/tui`): **60% line coverage minimum**. UI-only code has lower risk, but event streaming and theme math still deserve baseline coverage.
- **Generated / boilerplate**: No target. No generated code was found in this repo.

## Notes

- **No CI pipeline exists** (no `.github/workflows/`). Consider adding a GitHub Actions workflow that runs `go test ./... -race -coverprofile=coverage.out` and gates merges on the coverage targets above.
- **The core tools binary (`cmd/steiner-core-tools`) executes real shell commands and file I/O.** Unit tests should use safe commands (`echo`, `false`) in temp directories. Avoid testing destructive operations.
- **Provider tests should not hit real endpoints.** Use `httptest.Server` for integration tests; the project already has `defaultHTTPClient` that can be overridden in tests.
- **Config loading touches the real filesystem and environment.** Use `LoadOptions` with explicit `WorkingDir`, `HomeDir`, and `Env` maps to isolate tests.
- **The TUI package (`internal/tui`) uses Bubble Tea.** Full TUI functional tests are expensive; focus on pure functions (theme math, input parsing, content rendering) and leave bubble-tea framework integration to manual QA.
- **Two stub tests were found:** `internal/tui/help_test.go` and `internal/tui/input_test.go`. These should either be filled with assertions or removed to avoid giving a false sense of coverage.

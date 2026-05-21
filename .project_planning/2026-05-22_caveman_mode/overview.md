## Request

Add "caveman mode" to Steiner: a prompt-level style change that makes the model (and compaction summaries) speak tersely like a caveman to reduce token usage. The mode is enabled by default via config, and can be toggled at runtime with `/caveman-toggle`.

## Overview

Caveman mode is a purely prompt-based feature. It does **not** disable scratchpad, delegation, or any agent mechanics. When enabled, the system prompt, compaction prompt, and sub-agent system prompt are prepended with terse-style instructions. The model is told to drop articles, filler words, pleasantries, and hedging; use short synonyms; keep code blocks and errors exact.

### Integration points

1. **Config layer**: `CavemanMode bool` added to `Config` (default `true`), `CLIOverrides`, `configPatch`, env var `STEINER_CAVEMAN_MODE`, validation.
2. **CLI layer**: `--caveman` persistent flag wired into `cliRunner` via a `cavemanMode func() bool` closure (same pattern as `currentAlias`).
3. **Agent layer**: `RunRequest.CavemanMode bool` carries the flag. `Runner.Run()` passes it through `turnInput` to `prepareTurn`.
4. **Prompt layer**: `SystemPreamble()` conditionally prepends caveman style instructions. `BuildConversationCompactionPrompt()` conditionally uses a caveman compaction prompt. `AssemblyOptions.CavemanMode` field controls this.
5. **Delegation layer**: `BuildChildRun()` copies parent's `CavemanMode` into child `RunRequest` and child prompt assembly.
6. **Interactive layer**: `ToggleCavemanMode` action flips `Session.deps.Config.CavemanMode`. TUI adds `/caveman-toggle` command with help and slash-overlay entry.
7. **Manual compaction**: `manualCompaction()` reads `s.deps.Config.CavemanMode` when building its `RunRequest`.

## Verification Strategy

| Command | Cost | When |
|---|---|---|
| `gofmt -w <files>` | cheap | after every file edit |
| `goimports -w <files>` | cheap | after every file edit |
| `go test ./internal/config/...` | cheap | after config changes |
| `go test ./internal/prompt/...` | cheap | after prompt changes |
| `go test ./internal/agent/...` | medium | after agent changes |
| `go test ./internal/interactive/...` | medium | after interactive changes |
| `go test ./cmd/steiner/...` | medium | after CLI changes |
| `go test ./...` | medium | before finalising |
| `make check` | very expensive | final gate |

## Decision Log

1. **Default enabled**: User explicitly requested "Config file setting enables it by default." We set `CavemanMode: true` in `internal/config/defaults.go`.
2. **Purely prompt-based**: Does not disable scratchpad, delegation, or any agent features. Only affects language style of system/compaction/child prompts.
3. **Runtime toggle follows `SwitchModel` pattern**: `Session.deps.Config` is mutated in-place. `cliRunner` reads live state via a closure (`cavemanMode func() bool`) because `buildRunRequest` uses `cliRuntime.cfg`, not the Session copy.
4. **Compaction prompt override**: When caveman mode is on, `BuildConversationCompactionPrompt` replaces the detailed instruction body with a terse caveman variant instead of the normal `compactionPromptInstructionBody`.
5. **Sub-agent inheritance**: Child runs copy the parent's caveman mode flag so sub-agents also speak tersely.

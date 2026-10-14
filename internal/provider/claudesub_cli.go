package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

// claudeSubMinCLIVersion is the oldest claude CLI build every claude_subscription
// behaviour was verified on (D17).
const claudeSubMinCLIVersion = "2.1.294"

// claudeSubCLI is a located, version-checked and signed-in claude CLI.
type claudeSubCLI struct {
	Path    string
	Version string
}

// claudeSubRunner runs the located claude CLI and returns its output. The
// concrete implementation in step-3 uses exec; tests inject a fake so no real
// CLI is ever started.
type claudeSubRunner func(ctx context.Context, path string, args ...string) ([]byte, error)

var claudeSubVersionRe = regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`)

// locateClaudeSubCLI finds the claude CLI on PATH, checks it is new enough and
// confirms the user is signed in to a Claude subscription (D3, D17). It runs
// only `--version` and `auth status`; it never reads a credential file (D4).
func locateClaudeSubCLI(ctx context.Context, lookPath func(string) (string, error), run claudeSubRunner) (claudeSubCLI, error) {
	path, err := lookPath("claude")
	if err != nil {
		return claudeSubCLI{}, errors.New("claude CLI not found on PATH: install Claude Code, then run `claude auth login`")
	}

	out, err := run(ctx, path, "--version")
	if err != nil {
		return claudeSubCLI{}, fmt.Errorf("run claude --version: %w", err)
	}
	m := claudeSubVersionRe.FindStringSubmatch(string(out))
	if m == nil {
		return claudeSubCLI{}, fmt.Errorf("could not parse claude CLI version from %q", strings.TrimSpace(string(out)))
	}
	version := m[1] + "." + m[2] + "." + m[3]
	if claudeSubVersionLess(version, claudeSubMinCLIVersion) {
		return claudeSubCLI{}, fmt.Errorf("claude CLI %s is too old: claude_subscription needs %s or newer (run `claude update`)", version, claudeSubMinCLIVersion)
	}

	authOut, err := run(ctx, path, "auth", "status")
	if err != nil {
		return claudeSubCLI{}, fmt.Errorf("run claude auth status: %w", err)
	}
	var status claudeSubAuthStatus
	if err := json.Unmarshal(authOut, &status); err != nil {
		return claudeSubCLI{}, fmt.Errorf("parse claude auth status: %w", err)
	}
	if !status.LoggedIn {
		return claudeSubCLI{}, errors.New("claude CLI is not signed in: run `claude auth login` in a terminal")
	}
	if status.AuthMethod != "claude.ai" || status.APIProvider != "firstParty" {
		return claudeSubCLI{}, fmt.Errorf("claude CLI is signed in with %s/%s, not a Claude subscription: use the anthropic provider with an API key instead, or run `claude auth login`", status.AuthMethod, status.APIProvider)
	}
	return claudeSubCLI{Path: path, Version: version}, nil
}

// claudeSubAuthStatus is the JSON shape of `claude auth status` (D3).
type claudeSubAuthStatus struct {
	LoggedIn         bool   `json:"loggedIn"`
	AuthMethod       string `json:"authMethod"`
	APIProvider      string `json:"apiProvider"`
	SubscriptionType string `json:"subscriptionType"`
}

// claudeSubVersionLess reports whether version a is older than b by comparing
// the three dot-separated numeric components.
func claudeSubVersionLess(a, b string) bool {
	ap, bp := claudeSubVersionParts(a), claudeSubVersionParts(b)
	for i := range ap {
		if ap[i] != bp[i] {
			return ap[i] < bp[i]
		}
	}
	return false
}

func claudeSubVersionParts(v string) [3]int {
	var parts [3]int
	for i, s := range strings.SplitN(v, ".", 3) {
		n, _ := strconv.Atoi(s)
		parts[i] = n
	}
	return parts
}

// claudeSubStrippedEnvKeys are the credential and entrypoint variables steiner
// never forwards to the child CLI (D4).
var claudeSubStrippedEnvKeys = map[string]struct{}{
	"ANTHROPIC_API_KEY":       {},
	"ANTHROPIC_AUTH_TOKEN":    {},
	"CLAUDE_CODE_OAUTH_TOKEN": {},
	"CLAUDE_CODE_ENTRYPOINT":  {},
}

// claudeSubMaxMCPOutputTokensEnv is the CLI variable that caps MCP tool output
// in tokens.
const claudeSubMaxMCPOutputTokensEnv = "MAX_MCP_OUTPUT_TOKENS"

// claudeSubFixedEnv are the KEY=value overrides steiner always sets on the
// child, ahead of MAX_MCP_OUTPUT_TOKENS.
var claudeSubFixedEnv = []string{
	"CLAUDE_CODE_DISABLE_FAST_MODE=1",
	"CLAUDE_CODE_RETRY_WATCHDOG=1",
}

// claudeSubLaunchEnv are the CLI context-injection gates (#905), appended after
// MAX_MCP_OUTPUT_TOKENS. Each turns off context the CLI would add that steiner
// did not supply. None of them breaks the CLI's stored subscription OAuth.
var claudeSubLaunchEnv = []string{
	"CLAUDE_CODE_DISABLE_AUTO_MEMORY=1",
	"CLAUDE_CODE_DISABLE_CLAUDE_MDS=1",
	"CLAUDE_CODE_SILENT_TURN_REMINDER=0",
	"CLAUDE_CODE_TOTAL_TOKENS_REMINDER=off",
	"CLAUDE_CODE_TODO_REMINDER_MODE=off",
	"CLAUDE_CODE_DISABLE_GIT_INSTRUCTIONS=1",
}

// claudeSubForcedEnvKeys are the variables steiner always sets on the child,
// derived from claudeSubFixedEnv, claudeSubLaunchEnv and MAX_MCP_OUTPUT_TOKENS.
// Any inherited entry with one of these names is dropped before the override is
// appended, so the child never sees a second value.
var claudeSubForcedEnvKeys = claudeSubEnvNames([]string{claudeSubMaxMCPOutputTokensEnv}, claudeSubFixedEnv, claudeSubLaunchEnv)

// claudeSubEnvNames returns the names in extra plus the names of the KEY=value
// entries in groups.
func claudeSubEnvNames(extra []string, groups ...[]string) map[string]struct{} {
	names := make(map[string]struct{}, len(extra))
	for _, name := range extra {
		names[name] = struct{}{}
	}
	for _, group := range groups {
		for _, kv := range group {
			key, _, _ := strings.Cut(kv, "=")
			names[key] = struct{}{}
		}
	}
	return names
}

// claudeSubChildEnv builds the child environment: base minus the credential and
// entrypoint variables, plus the steiner-owned overrides (D4, D12).
// Windows environment names are case-insensitive, so there every case variant
// of those names is removed; elsewhere names match exactly.
// MAX_MCP_OUTPUT_TOKENS over-estimates the byte budget as tokens so the CLI
// never truncates steiner's already-bounded tool output.
func claudeSubChildEnv(base []string, toolOutputMaxBytes int) []string {
	return claudeSubChildEnvFold(base, toolOutputMaxBytes, runtime.GOOS == "windows")
}

// claudeSubChildEnvFold is claudeSubChildEnv with the name matching mode passed
// in, so both modes are testable on any OS. foldCase compares names
// case-insensitively.
func claudeSubChildEnvFold(base []string, toolOutputMaxBytes int, foldCase bool) []string {
	out := make([]string, 0, len(base)+len(claudeSubFixedEnv)+len(claudeSubLaunchEnv)+1)
	for _, kv := range base {
		key, _, _ := strings.Cut(kv, "=")
		if claudeSubEnvNameIn(key, claudeSubStrippedEnvKeys, foldCase) || claudeSubEnvNameIn(key, claudeSubForcedEnvKeys, foldCase) {
			continue
		}
		out = append(out, kv)
	}
	out = append(out, claudeSubFixedEnv...)
	out = append(out, fmt.Sprintf(claudeSubMaxMCPOutputTokensEnv+"=%d", claudeSubMaxMCPOutputTokens(toolOutputMaxBytes)))
	return append(out, claudeSubLaunchEnv...)
}

// claudeSubEnvNameIn reports whether key is a member of names. With foldCase the
// comparison is case-insensitive.
func claudeSubEnvNameIn(key string, names map[string]struct{}, foldCase bool) bool {
	for name := range names {
		if key == name || foldCase && strings.EqualFold(key, name) {
			return true
		}
	}
	return false
}

// claudeSubMaxMCPOutputTokens converts a tool-output byte budget into the CLI's
// token budget: half the bytes, floored at 25000 tokens.
func claudeSubMaxMCPOutputTokens(toolOutputMaxBytes int) int {
	const minTokens = 25000
	tokens := (toolOutputMaxBytes + 1) / 2
	if tokens < minTokens {
		return minTokens
	}
	return tokens
}

// claudeSubResume names the stored CLI session a new process restores (D3).
// AtUUID stops the restore at that transcript message. Fork starts a new
// session ID from it instead of continuing the stored one.
type claudeSubResume struct {
	SessionID string
	AtUUID    string
	Fork      bool
}

// claudeSubSpawnOptions are the per-process knobs that shape the child argv.
type claudeSubSpawnOptions struct {
	Model            string
	Effort           string
	ThinkingDisabled bool
	SystemPromptFile string
	MCPConfigFile    string
	// Persist keeps the CLI session on disk. Only parent calls persist (D4).
	Persist bool
	// Resume restores a stored CLI session instead of starting an empty one.
	Resume claudeSubResume
	// MaxTurns caps the CLI turns of the process when positive. Summary helpers
	// use 1 (D-compaction).
	MaxTurns int
}

// claudeSubArgs returns the child argv in the fixed order the CLI is driven
// with (D7, D12). `--tools ""` is deliberately two argv entries.
func claudeSubArgs(o claudeSubSpawnOptions) []string {
	args := []string{
		"-p",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--verbose",
		"--include-partial-messages",
	}
	// An empty model omits --model so the CLI uses its default model.
	if o.Model != "" {
		args = append(args, "--model", o.Model)
	}
	args = append(args,
		"--tools", "",
		"--strict-mcp-config",
		"--setting-sources=",
		"--disable-slash-commands",
	)
	if !o.Persist {
		args = append(args, "--no-session-persistence")
	}
	args = append(args,
		"--settings", `{"autoCompactEnabled":false,"fastMode":false}`,
		"--permission-mode", "dontAsk",
	)
	if o.Resume.SessionID != "" {
		args = append(args, "--resume", o.Resume.SessionID)
		if o.Resume.AtUUID != "" {
			args = append(args, "--resume-session-at", o.Resume.AtUUID)
		}
		if o.Resume.Fork {
			args = append(args, "--fork-session")
		}
	}
	if o.MCPConfigFile != "" {
		args = append(args, "--mcp-config", o.MCPConfigFile, "--allowedTools", claudeSubToolPrefix+"*")
	}
	if o.SystemPromptFile != "" {
		args = append(args, "--system-prompt-file", o.SystemPromptFile)
	}
	effort, effortDisablesThinking := claudeSubEffort(o.Effort)
	if o.ThinkingDisabled || effortDisablesThinking {
		args = append(args, "--thinking", "disabled")
	} else {
		args = append(args, "--thinking", "adaptive", "--thinking-display", "summarized")
	}
	if effort != "" {
		args = append(args, "--effort", effort)
	}
	if o.MaxTurns > 0 {
		args = append(args, "--max-turns", strconv.Itoa(o.MaxTurns))
	}
	return args
}

// claudeSubEffortLevels are the effort levels the CLI accepts on --effort.
var claudeSubEffortLevels = map[string]struct{}{
	"low": {}, "medium": {}, "high": {}, "xhigh": {}, "max": {},
}

// claudeSubEffort maps a steiner reasoning effort to the CLI's --effort value
// and to whether thinking should be disabled. "none" disables thinking; an
// unknown value yields neither.
func claudeSubEffort(effort string) (cliEffort string, thinkingDisabled bool) {
	e := strings.ToLower(strings.TrimSpace(effort))
	if e == "none" {
		return "", true
	}
	if _, ok := claudeSubEffortLevels[e]; ok {
		return e, false
	}
	return "", false
}

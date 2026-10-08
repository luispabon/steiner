package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
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

// claudeSubChildEnv builds the child environment: base minus the credential and
// entrypoint variables, plus the three steiner-owned overrides (D4, D12).
// MAX_MCP_OUTPUT_TOKENS over-estimates the byte budget as tokens so the CLI
// never truncates steiner's already-bounded tool output.
func claudeSubChildEnv(base []string, toolOutputMaxBytes int) []string {
	out := make([]string, 0, len(base)+3)
	for _, kv := range base {
		key, _, _ := strings.Cut(kv, "=")
		if _, drop := claudeSubStrippedEnvKeys[key]; drop {
			continue
		}
		out = append(out, kv)
	}
	return append(out,
		"CLAUDE_CODE_DISABLE_FAST_MODE=1",
		"CLAUDE_CODE_RETRY_WATCHDOG=1",
		fmt.Sprintf("MAX_MCP_OUTPUT_TOKENS=%d", claudeSubMaxMCPOutputTokens(toolOutputMaxBytes)),
	)
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

// claudeSubSpawnOptions are the per-process knobs that shape the child argv.
type claudeSubSpawnOptions struct {
	Model            string
	Effort           string
	ThinkingDisabled bool
	SystemPromptFile string
	MCPConfigFile    string
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
		"--model", o.Model,
		"--tools", "",
		"--strict-mcp-config",
		"--setting-sources=",
		"--no-session-persistence",
		"--settings", `{"autoCompactEnabled":false,"fastMode":false}`,
		"--permission-mode", "dontAsk",
	}
	if o.MCPConfigFile != "" {
		args = append(args, "--mcp-config", o.MCPConfigFile, "--allowedTools", "mcp__steiner__*")
	}
	if o.SystemPromptFile != "" {
		args = append(args, "--system-prompt-file", o.SystemPromptFile)
	}
	if o.ThinkingDisabled {
		args = append(args, "--thinking", "disabled")
	} else {
		args = append(args, "--thinking", "adaptive", "--thinking-display", "summarized")
	}
	if effort, _ := claudeSubEffort(o.Effort); effort != "" {
		args = append(args, "--effort", effort)
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

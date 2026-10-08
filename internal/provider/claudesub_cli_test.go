package provider

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func claudeSubLookPathOK(string) (string, error) { return "/usr/local/bin/claude", nil }

func claudeSubFixedRunner(version, auth string) claudeSubRunner {
	return func(_ context.Context, _ string, args ...string) ([]byte, error) {
		switch {
		case len(args) == 1 && args[0] == "--version":
			return []byte(version), nil
		case len(args) == 2 && args[0] == "auth" && args[1] == "status":
			return []byte(auth), nil
		default:
			return nil, errors.New("unexpected args: " + strings.Join(args, " "))
		}
	}
}

func TestClaudeSubLocateCLI(t *testing.T) {
	const authOK = `{"loggedIn":true,"authMethod":"claude.ai","apiProvider":"firstParty","subscriptionType":"pro"}`
	tests := []struct {
		name     string
		lookPath func(string) (string, error)
		run      claudeSubRunner
		version  string
		auth     string
		wantErr  string
		wantPath string
		wantVer  string
	}{
		{name: "missing CLI", lookPath: func(string) (string, error) { return "", errors.New("executable file not found in $PATH") }, wantErr: "claude CLI not found on PATH: install Claude Code, then run `claude auth login`"},
		{name: "version below minimum", version: "2.1.200 (Claude Code)", wantErr: "claude CLI 2.1.200 is too old: claude_subscription needs 2.1.294 or newer (run `claude update`)"},
		{name: "version equal to minimum", version: "2.1.294 (Claude Code)", auth: authOK, wantPath: "/usr/local/bin/claude", wantVer: "2.1.294"},
		{name: "version above minimum", version: "2.2.0", auth: authOK, wantPath: "/usr/local/bin/claude", wantVer: "2.2.0"},
		{name: "version unparsable", version: "unknown build", wantErr: "could not parse claude CLI version"},
		{name: "version command fails", run: func(context.Context, string, ...string) ([]byte, error) { return nil, errors.New("boom") }, wantErr: "run claude --version"},
		{name: "auth status command fails", run: func(_ context.Context, _ string, args ...string) ([]byte, error) {
			if args[0] == "--version" {
				return []byte("2.1.294"), nil
			}
			return nil, errors.New("auth boom")
		}, wantErr: "run claude auth status"},
		{name: "not signed in", version: "2.1.294", auth: `{"loggedIn":false}`, wantErr: "claude CLI is not signed in: run `claude auth login` in a terminal"},
		{name: "API key auth", version: "2.1.294", auth: `{"loggedIn":true,"authMethod":"api_key","apiProvider":"firstParty"}`, wantErr: "claude CLI is signed in with api_key/firstParty, not a Claude subscription: use the anthropic provider with an API key instead, or run `claude auth login`"},
		{name: "bedrock api provider", version: "2.1.294", auth: `{"loggedIn":true,"authMethod":"claude.ai","apiProvider":"bedrock"}`, wantErr: "claude CLI is signed in with claude.ai/bedrock, not a Claude subscription: use the anthropic provider with an API key instead, or run `claude auth login`"},
		{name: "auth status not json", version: "2.1.294", auth: "not json", wantErr: "parse claude auth status"},
		{name: "valid claude.ai firstParty login", version: "2.3.10", auth: authOK, wantPath: "/usr/local/bin/claude", wantVer: "2.3.10"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			lookPath := tc.lookPath
			if lookPath == nil {
				lookPath = claudeSubLookPathOK
			}
			run := tc.run
			if run == nil {
				run = claudeSubFixedRunner(tc.version, tc.auth)
			}
			cli, err := locateClaudeSubCLI(context.Background(), lookPath, run)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("locateClaudeSubCLI() error = %v, want nil", err)
				}
				if cli.Path != tc.wantPath || cli.Version != tc.wantVer {
					t.Errorf("locateClaudeSubCLI() = %+v, want path %q version %q", cli, tc.wantPath, tc.wantVer)
				}
				return
			}
			if err == nil {
				t.Fatalf("locateClaudeSubCLI() error = nil, want substring %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("locateClaudeSubCLI() error = %q, want substring %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestClaudeSubChildEnv(t *testing.T) {
	base := []string{"PATH=/usr/bin:/bin", "ANTHROPIC_API_KEY=sk-secret", "ANTHROPIC_AUTH_TOKEN=auth-token", "CLAUDE_CODE_OAUTH_TOKEN=oauth-token", "CLAUDE_CODE_ENTRYPOINT=cli", "HOME=/home/user"}
	want := []string{"PATH=/usr/bin:/bin", "HOME=/home/user", "CLAUDE_CODE_DISABLE_FAST_MODE=1", "CLAUDE_CODE_RETRY_WATCHDOG=1", "MAX_MCP_OUTPUT_TOKENS=32768"}
	if got := claudeSubChildEnv(base, 65536); !reflect.DeepEqual(got, want) {
		t.Fatalf("claudeSubChildEnv() = %#v, want %#v", got, want)
	}
	for _, kv := range claudeSubChildEnv(base, 65536) {
		key, _, _ := strings.Cut(kv, "=")
		if _, leaked := claudeSubStrippedEnvKeys[key]; leaked {
			t.Errorf("stripped env key %q still present in child env", key)
		}
	}
	for _, tc := range []struct {
		budget int
		want   string
	}{
		{65536, "MAX_MCP_OUTPUT_TOKENS=32768"},
		{10000, "MAX_MCP_OUTPUT_TOKENS=25000"},
		{200000, "MAX_MCP_OUTPUT_TOKENS=100000"},
	} {
		env := claudeSubChildEnv(nil, tc.budget)
		if last := env[len(env)-1]; last != tc.want {
			t.Errorf("claudeSubChildEnv(nil, %d) last entry = %q, want %q", tc.budget, last, tc.want)
		}
	}
}

func TestClaudeSubMaxMCPOutputTokens(t *testing.T) {
	tests := []struct {
		name  string
		bytes int
		want  int
	}{
		{"default 65536", 65536, 32768},
		{"small 10000 floors", 10000, 25000},
		{"large 200000", 200000, 100000},
		{"zero floors", 0, 25000},
		{"just below floor", 49998, 25000},
		{"at floor", 49999, 25000},
		{"floor plus one", 50000, 25000},
		{"first above floor", 50002, 25001},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := claudeSubMaxMCPOutputTokens(tc.bytes); got != tc.want {
				t.Errorf("claudeSubMaxMCPOutputTokens(%d) = %d, want %d", tc.bytes, got, tc.want)
			}
		})
	}
}

func TestClaudeSubArgs(t *testing.T) {
	base := []string{"-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose", "--include-partial-messages", "--model", "claude-haiku-5-5", "--tools", "", "--strict-mcp-config", "--setting-sources=", "--no-session-persistence", "--settings", `{"autoCompactEnabled":false,"fastMode":false}`, "--permission-mode", "dontAsk"}
	adaptive := []string{"--thinking", "adaptive", "--thinking-display", "summarized"}
	withBase := func(extra ...string) []string {
		out := make([]string, 0, len(base)+len(extra))
		out = append(out, base...)
		return append(out, extra...)
	}
	tests := []struct {
		name string
		opts claudeSubSpawnOptions
		want []string
	}{
		{
			name: "mcp, system prompt, adaptive thinking and effort",
			opts: claudeSubSpawnOptions{Model: "claude-haiku-5-5", Effort: "high", SystemPromptFile: "/tmp/session/system.md", MCPConfigFile: "/tmp/session/mcp.json"},
			want: withBase("--mcp-config", "/tmp/session/mcp.json", "--allowedTools", "mcp__steiner__*", "--system-prompt-file", "/tmp/session/system.md", "--thinking", "adaptive", "--thinking-display", "summarized", "--effort", "high"),
		},
		{
			name: "mcp only, default thinking, effort medium",
			opts: claudeSubSpawnOptions{Model: "claude-haiku-5-5", Effort: "medium", MCPConfigFile: "/tmp/mcp.json"},
			want: withBase("--mcp-config", "/tmp/mcp.json", "--allowedTools", "mcp__steiner__*", "--thinking", "adaptive", "--thinking-display", "summarized", "--effort", "medium"),
		},
		{
			name: "system prompt only",
			opts: claudeSubSpawnOptions{Model: "claude-haiku-5-5", SystemPromptFile: "/tmp/system.md"},
			want: withBase(append([]string{"--system-prompt-file", "/tmp/system.md"}, adaptive...)...),
		},
		{
			name: "thinking disabled, no effort",
			opts: claudeSubSpawnOptions{Model: "claude-haiku-5-5", ThinkingDisabled: true},
			want: withBase("--thinking", "disabled"),
		},
		{name: "invalid effort omitted", opts: claudeSubSpawnOptions{Model: "claude-haiku-5-5", Effort: "turbo"}, want: withBase(adaptive...)},
		{name: "effort none omitted", opts: claudeSubSpawnOptions{Model: "claude-haiku-5-5", Effort: "none"}, want: withBase(adaptive...)},
		{name: "model only", opts: claudeSubSpawnOptions{Model: "claude-haiku-5-5"}, want: withBase(adaptive...)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := claudeSubArgs(tc.opts); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("claudeSubArgs() = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestClaudeSubEffort(t *testing.T) {
	tests := []struct {
		in      string
		wantCLI string
		wantDis bool
	}{
		{"", "", false},
		{"none", "", true},
		{"NONE", "", true},
		{"  None  ", "", true},
		{"low", "low", false},
		{"medium", "medium", false},
		{"high", "high", false},
		{"xhigh", "xhigh", false},
		{"max", "max", false},
		{" HIGH ", "high", false},
		{"minimal", "", false},
		{"turbo", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			cli, disabled := claudeSubEffort(tc.in)
			if cli != tc.wantCLI || disabled != tc.wantDis {
				t.Errorf("claudeSubEffort(%q) = (%q, %v), want (%q, %v)", tc.in, cli, disabled, tc.wantCLI, tc.wantDis)
			}
		})
	}
}

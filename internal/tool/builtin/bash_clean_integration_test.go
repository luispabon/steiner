package builtin

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/tool"
)

func runBashForTest(t *testing.T, input map[string]any) *BashResult {
	t.Helper()
	dir := t.TempDir()
	policy := tool.NewPathPolicy(dir, config.PathsConfig{})
	toolDef := NewBashTool(Env{WorkDir: dir, PathPolicy: &policy})
	resI, err := toolDef.Handler(withUnsandboxedWrapper(context.Background()), input)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	return resI.(*BashResult)
}

func TestBashToolCleansANSIAndCRLFUnlessRaw(t *testing.T) {
	t.Parallel()
	cmd := `printf '\033[31mred\033[0m\r\nnext\r\n'`
	cleaned := runBashForTest(t, map[string]any{"command": cmd})
	if cleaned.Output != "red\nnext\n" {
		t.Errorf("cleaned Output = %q, want %q", cleaned.Output, "red\nnext\n")
	}
	raw := runBashForTest(t, map[string]any{"command": cmd, "raw_output": true})
	want := "\x1b[31mred\x1b[0m\r\nnext\r\n"
	if raw.Output != want {
		t.Errorf("raw Output = %q, want %q", raw.Output, want)
	}
}

func TestBashToolPreservesStderrWhitespaceAndExit(t *testing.T) {
	t.Parallel()
	res := runBashForTest(t, map[string]any{"command": "echo out; printf '  err  \\n\\n' >&2; (exit 3)"})
	want := "out\n[stderr]\n  err  \n\n"
	if res.Output != want || res.ExitCode != 3 || res.Truncated {
		t.Errorf("got (%q, %d, %v), want (%q, 3, false)", res.Output, res.ExitCode, res.Truncated, want)
	}
}

func TestBashToolBudgetKeepsTailAndMarker(t *testing.T) {
	t.Parallel()
	const budget = 500
	for _, raw := range []bool{false, true} {
		res := runBashForTest(t, map[string]any{
			"command":          "seq 1 400000; echo LASTLINE",
			"max_output_bytes": budget,
			"raw_output":       raw,
		})
		if len(res.Output) > budget || !res.Truncated || !utf8.ValidString(res.Output) {
			t.Errorf("raw=%v: len=%d truncated=%v valid=%v", raw, len(res.Output), res.Truncated, utf8.ValidString(res.Output))
		}
		if !strings.HasSuffix(res.Output, "LASTLINE\n") || !strings.Contains(res.Output, "bytes omitted ...]") {
			t.Errorf("raw=%v: Output missing tail or markers: %q", raw, res.Output)
		}
		if res.ExitCode != 0 {
			t.Errorf("raw=%v: ExitCode = %d", raw, res.ExitCode)
		}
	}
}

func TestBashToolBudgetMultibyteAndTinyBudget(t *testing.T) {
	t.Parallel()
	for _, budget := range []int{1, 5, 40, 101} {
		res := runBashForTest(t, map[string]any{
			"command":          "yes 世界 | head -c 3000000",
			"max_output_bytes": budget,
		})
		if len(res.Output) > budget || !utf8.ValidString(res.Output) || !res.Truncated {
			t.Errorf("budget %d: len=%d valid=%v truncated=%v", budget, len(res.Output), utf8.ValidString(res.Output), res.Truncated)
		}
	}
}

func TestBashToolTimeoutKeepsPartialOutput(t *testing.T) {
	t.Parallel()
	res := runBashForTest(t, map[string]any{"command": "echo partial; echo perr >&2; sleep 30", "timeout_seconds": 1})
	if res.ExitCode == 0 || !res.TimedOut {
		t.Errorf("ExitCode = %d, TimedOut = %v, want non-zero, true", res.ExitCode, res.TimedOut)
	}
	for _, want := range []string{"partial", "[stderr]\nperr", "context deadline exceeded"} {
		if !strings.Contains(res.Output, want) {
			t.Errorf("Output = %q, want to contain %q", res.Output, want)
		}
	}
}

func TestBashToolRawAndCleanedKeepLoneAndTrailingLF(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, command string
		raw, cleaned  string
	}{
		{"lone LF", `printf '\n'`, "\n", "\n"},
		{"trailing LF", `printf 'a\n'`, "a\n", "a\n"},
		{"CRLF", `printf 'a\r\nb\r\n'`, "a\r\nb\r\n", "a\nb\n"},
	}
	for _, tt := range tests {
		raw := runBashForTest(t, map[string]any{"command": tt.command, "raw_output": true})
		if raw.Output != tt.raw {
			t.Errorf("%s raw = %q, want %q", tt.name, raw.Output, tt.raw)
		}
		cleaned := runBashForTest(t, map[string]any{"command": tt.command})
		if cleaned.Output != tt.cleaned {
			t.Errorf("%s cleaned = %q, want %q", tt.name, cleaned.Output, tt.cleaned)
		}
	}
}

func TestBashToolShellFailureKeepsPartials(t *testing.T) {
	t.Parallel()
	res := runBashForTest(t, map[string]any{"command": "echo partial; echo perr >&2; exit 3"})
	if res.ExitCode != -1 || !res.Truncated || res.TimedOut {
		t.Errorf("got exit=%d truncated=%v timedOut=%v, want -1, true, false", res.ExitCode, res.Truncated, res.TimedOut)
	}
	for _, want := range []string{"partial", "[stderr]\nperr", "[bash session error: ", "output above is partial]"} {
		if !strings.Contains(res.Output, want) {
			t.Errorf("Output = %q, want to contain %q", res.Output, want)
		}
	}
	if strings.Count(res.Output, "partial") != 2 { // stdout text and the notice wording
		t.Errorf("Output = %q: captured text duplicated from error message", res.Output)
	}
}

func TestBashToolTinyBudgetTimeoutMetadata(t *testing.T) {
	t.Parallel()
	for _, budget := range []int{1, 8, 60} {
		res := runBashForTest(t, map[string]any{
			"command": "echo partial; sleep 30", "timeout_seconds": 1, "max_output_bytes": budget,
		})
		if !res.TimedOut || res.ExitCode == 0 || res.Message == "" || len(res.Output) > budget {
			t.Errorf("budget %d: timedOut=%v exit=%d truncated=%v len=%d", budget, res.TimedOut, res.ExitCode, res.Truncated, len(res.Output))
		}
	}
}

func TestBashToolMessageMetadataSurvivesTinyBudget(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		input   map[string]any
		timeout bool
		want    string
	}{
		{"shell failure", map[string]any{"command": "echo partial; exit 3", "max_output_bytes": 1}, false, "bash session error: "},
		{"timeout", map[string]any{"command": "sleep 30", "timeout_seconds": 1, "max_output_bytes": 1}, true, "context deadline exceeded"},
	}
	for _, tc := range cases {
		res := runBashForTest(t, tc.input)
		if len(res.Output) > 1 || !res.Truncated && !tc.timeout || res.TimedOut != tc.timeout || !strings.Contains(res.Message, tc.want) {
			t.Errorf("%s: output=%q truncated=%v timedOut=%v message=%q", tc.name, res.Output, res.Truncated, res.TimedOut, res.Message)
		}
		data, err := json.Marshal(res)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]any
		if err := json.Unmarshal(data, &fields); err != nil {
			t.Fatal(err)
		}
		if _, ok := fields["message"]; !ok {
			t.Errorf("%s: JSON %s lacks message", tc.name, data)
		}
		if _, ok := fields["timed_out"]; ok != tc.timeout {
			t.Errorf("%s: JSON %s timed_out presence = %v, want %v", tc.name, data, ok, tc.timeout)
		}
	}
}

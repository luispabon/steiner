package builtin

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/tool"
)

func runBashForTest(t *testing.T, input map[string]any) *BashResult {
	t.Helper()
	policy := tool.NewPathPolicy(t.TempDir(), config.PathsConfig{})
	toolDef := NewBashTool(Env{PathPolicy: &policy})
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
		if !strings.HasSuffix(res.Output, "LASTLINE\n") || !strings.Contains(res.Output, "<truncated output") {
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

var completeTruncMarkerRE = regexp.MustCompile(`<truncated output shown=\d+ total=\d+>`)

func TestFitBashOutputBudget(t *testing.T) {
	t.Parallel()
	bodies := []string{
		strings.Repeat("é", 500),
		"head\n" + strings.Repeat("x", 300) + "\n[123456 bytes omitted]\n" + strings.Repeat("y", 300),
	}
	for _, body := range bodies {
		for budget := 1; budget < 1200; budget += 7 {
			out, cut := fitBashOutput(body, "\n[end]", budget)
			if len(out) > budget || !utf8.ValidString(out) {
				t.Fatalf("budget %d: len=%d valid=%v", budget, len(out), utf8.ValidString(out))
			}
			if len(body)+6 > budget && !cut {
				t.Fatalf("budget %d: cut = false for oversized body", budget)
			}
			// A marker is either complete or absent; labels likewise.
			if strings.Count(out, "<truncated") != len(completeTruncMarkerRE.FindAllString(out, -1)) {
				t.Fatalf("budget %d: partial truncation marker in %q", budget, out)
			}
			if strings.Count(out, "bytes omitted") != len(bashOmittedLabelRE.FindAllString(out, -1)) {
				t.Fatalf("budget %d: partial omission label in %q", budget, out)
			}
			if strings.Contains(out, "<truncated") {
				m := regexp.MustCompile(`shown=(\d+) total=(\d+)`).FindStringSubmatch(out)
				if m[2] != strconv.Itoa(len(body)) {
					t.Fatalf("budget %d: marker total %s, want %d", budget, m[2], len(body))
				}
			}
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
		if !res.TimedOut || res.ExitCode == 0 || !res.Truncated || len(res.Output) > budget {
			t.Errorf("budget %d: timedOut=%v exit=%d truncated=%v len=%d", budget, res.TimedOut, res.ExitCode, res.Truncated, len(res.Output))
		}
		if strings.Contains(res.Output, "<truncated") && !completeTruncMarkerRE.MatchString(res.Output) {
			t.Errorf("budget %d: partial marker %q", budget, res.Output)
		}
	}
}

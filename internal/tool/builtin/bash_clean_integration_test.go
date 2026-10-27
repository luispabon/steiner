package builtin

import (
	"context"
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
	if cleaned.Output != "red\nnext" {
		t.Errorf("cleaned Output = %q, want %q", cleaned.Output, "red\nnext")
	}
	raw := runBashForTest(t, map[string]any{"command": cmd, "raw_output": true})
	want := "\x1b[31mred\x1b[0m\r\nnext\r"
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
		if !strings.HasSuffix(res.Output, "LASTLINE") || !strings.Contains(res.Output, "<truncated output") {
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
	if res.ExitCode == 0 {
		t.Error("ExitCode = 0, want non-zero")
	}
	for _, want := range []string{"partial", "[stderr]\nperr", "context deadline exceeded", "partial"} {
		if !strings.Contains(res.Output, want) {
			t.Errorf("Output = %q, want to contain %q", res.Output, want)
		}
	}
}

func TestFitBashOutputBudget(t *testing.T) {
	t.Parallel()
	body := strings.Repeat("é", 500)
	for budget := 1; budget < 1200; budget += 7 {
		out, cut := fitBashOutput(body, "\n[end]", budget, 0)
		if len(out) > budget || !utf8.ValidString(out) {
			t.Fatalf("budget %d: len=%d valid=%v", budget, len(out), utf8.ValidString(out))
		}
		if len(body)+6 > budget && !cut {
			t.Fatalf("budget %d: cut = false for oversized body", budget)
		}
	}
}

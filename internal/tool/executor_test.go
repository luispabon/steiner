package tool

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/config"
)

func TestExecutorCapturesTruncatedOutputAndMetadata(t *testing.T) {
	helper := mustBuildHelperBinary(t)
	reg := NewRegistry(ToolDef{Name: "probe", ExecPath: helper, Subcommand: "stderr"})
	cfg := config.Config{
		Limits: config.LimitsConfig{
			ToolOutputMaxBytes: 48,
		},
		Approval: config.ApprovalConfig{Default: config.ApprovalModeAuto},
	}

	result, err := NewExecutor(reg, cfg, nil, t.TempDir()).Execute(context.Background(), "probe", nil)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	execResult, ok := result.(ExecutionResult)
	if !ok {
		t.Fatalf("result type = %T, want tool.ExecutionResult", result)
	}
	if execResult.Metadata.Stdout.Truncated {
		t.Fatalf("stdout truncated = true, want false")
	}
	if !execResult.Metadata.Stderr.Truncated {
		t.Fatalf("stderr truncated = false, want true")
	}
	if execResult.Metadata.Stderr.Preview == "" {
		t.Fatal("stderr preview = empty, want captured preview")
	}
	if execResult.Metadata.Stderr.Binary {
		t.Fatal("stderr binary = true, want false")
	}
}

func TestExecutorMarksBinaryOutputSafely(t *testing.T) {
	helper := mustBuildHelperBinary(t)
	reg := NewRegistry(ToolDef{Name: "probe", ExecPath: helper, Subcommand: "binary"})
	cfg := config.Config{
		Limits: config.LimitsConfig{
			ToolOutputMaxBytes: 8,
		},
		Approval: config.ApprovalConfig{Default: config.ApprovalModeAuto},
	}

	_, err := NewExecutor(reg, cfg, nil, t.TempDir()).Execute(context.Background(), "probe", nil)
	if err == nil {
		t.Fatal("Execute() error = nil, want binary-output failure")
	}
	var toolErr *ToolExecutionError
	if !errors.As(err, &toolErr) {
		t.Fatalf("error type = %T, want *ToolExecutionError", err)
	}
	if toolErr.Kind != "invalid_json" {
		t.Fatalf("error kind = %q, want invalid_json", toolErr.Kind)
	}
	if !toolErr.Output.Stdout.Binary {
		t.Fatal("stdout binary = false, want true")
	}
	if toolErr.Output.Stdout.Preview == "" {
		t.Fatal("stdout preview = empty, want safe binary preview")
	}
	if strings.Contains(toolErr.Error(), "000") || strings.Contains(toolErr.Error(), "\x00") {
		t.Fatalf("error string leaked raw binary data: %q", toolErr.Error())
	}
}

func mustBuildHelperBinary(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	source := filepath.Join(dir, "main.go")
	if err := os.WriteFile(source, []byte(helperSource), 0o644); err != nil {
		t.Fatalf("write helper source: %v", err)
	}
	bin := filepath.Join(dir, "helper")
	cmd := exec.Command("go", "build", "-o", bin, source)
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build helper binary: %v: %s", err, strings.TrimSpace(string(output)))
	}
	return bin
}

const helperSource = `package main

import (
	"fmt"
	"os"
	"strings"
	"time"
)

func main() {
	mode := "stderr"
	if len(os.Args) > 1 {
		mode = os.Args[1]
	}
	switch mode {
	case "stderr":
		fmt.Fprint(os.Stdout, "{\"ok\":true,\"result\":{\"status\":\"ok\"}}")
		fmt.Fprint(os.Stderr, strings.Repeat("e", 128))
	case "binary":
		_, _ = os.Stdout.Write([]byte{0x00, 0xff, 0x42})
		_, _ = os.Stderr.Write([]byte{0x00, 0x01, 0x02})
		os.Exit(1)
	case "sleep":
		time.Sleep(10 * time.Second)
		fmt.Fprint(os.Stdout, "{\"ok\":true,\"result\":null}")
	case "fail":
		fmt.Fprint(os.Stdout, "{\"ok\":false,\"error\":{\"kind\":\"tool_failed\",\"message\":\"something went wrong\"}}")
		os.Exit(1)
	default:
		fmt.Fprint(os.Stdout, "{\"ok\":true,\"result\":null}")
	}
}
`

func TestExecutorApprovalDenied(t *testing.T) {
	helper := mustBuildHelperBinary(t)
	reg := NewRegistry(ToolDef{
		Name:     "probe",
		ExecPath: helper,
		Approval: config.ApprovalModeDeny,
	})
	cfg := config.Config{
		Approval: config.ApprovalConfig{
			Default: config.ApprovalModeAuto,
		},
	}

	executor := NewExecutor(reg, cfg, nil, t.TempDir())
	_, err := executor.Execute(context.Background(), "probe", nil)
	if err == nil {
		t.Fatal("Execute() error = nil, want approval_denied")
	}
	var toolErr *ToolExecutionError
	if !errors.As(err, &toolErr) {
		t.Fatalf("error type = %T, want *ToolExecutionError", err)
	}
	if toolErr.Kind != "approval_denied" {
		t.Fatalf("error kind = %q, want approval_denied", toolErr.Kind)
	}
}

func TestExecutorContextCancellation(t *testing.T) {
	helper := mustBuildHelperBinary(t)
	reg := NewRegistry(ToolDef{
		Name:     "probe",
		ExecPath: helper,
	})
	cfg := config.Config{
		Approval: config.ApprovalConfig{
			Default: config.ApprovalModePrompt,
		},
	}

	executor := NewExecutor(reg, cfg, ApprovalResponderFunc(func(ctx context.Context, req ApprovalRequest) error {
		return nil
	}), t.TempDir())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := executor.Execute(ctx, "probe", nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func TestExecutorTimeoutExceeded(t *testing.T) {
	helper := mustBuildHelperBinary(t)
	reg := NewRegistry(ToolDef{
		Name:       "probe",
		ExecPath:   helper,
		Subcommand: "sleep",
		Timeout:    100 * time.Millisecond,
	})
	cfg := config.Config{
		Approval: config.ApprovalConfig{
			Default: config.ApprovalModeAuto,
		},
	}

	executor := NewExecutor(reg, cfg, nil, t.TempDir())
	_, err := executor.Execute(context.Background(), "probe", nil)
	if err == nil {
		t.Fatal("Execute() error = nil, want DeadlineExceeded")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context.DeadlineExceeded", err)
	}
}

func TestExecutorNonZeroExitCode(t *testing.T) {
	helper := mustBuildHelperBinary(t)
	reg := NewRegistry(ToolDef{
		Name:       "probe",
		ExecPath:   helper,
		Subcommand: "fail",
	})
	cfg := config.Config{
		Approval: config.ApprovalConfig{
			Default: config.ApprovalModeAuto,
		},
	}

	executor := NewExecutor(reg, cfg, nil, t.TempDir())
	_, err := executor.Execute(context.Background(), "probe", nil)
	if err == nil {
		t.Fatal("Execute() error = nil, want non-zero exit error")
	}
	var toolErr *ToolExecutionError
	if !errors.As(err, &toolErr) {
		t.Fatalf("error type = %T, want *ToolExecutionError", err)
	}
	if toolErr.ExitCode == 0 {
		t.Fatalf("error exit code = %d, want non-zero", toolErr.ExitCode)
	}
	if toolErr.Kind != "tool_failed" {
		t.Fatalf("error kind = %q, want tool_failed", toolErr.Kind)
	}
}

func TestExitCodeFromError(t *testing.T) {
	if got := exitCodeFromError(nil); got != 0 {
		t.Fatalf("exitCodeFromError(nil) = %d, want 0", got)
	}
	if got := exitCodeFromError(errors.New("random error")); got != 1 {
		t.Fatalf("exitCodeFromError(errors.New) = %d, want 1", got)
	}

	err := exec.Command("sh", "-c", "exit 42").Run()
	if got := exitCodeFromError(err); got != 42 {
		t.Fatalf("exitCodeFromError(exit 42) = %d, want 42", got)
	}
}

func TestNormalizeExecutionRoot(t *testing.T) {
	if got := normalizeExecutionRoot(""); got != "" {
		t.Fatalf("normalizeExecutionRoot(\"\") = %q, want \"\"", got)
	}
	if got := normalizeExecutionRoot("  "); got != "" {
		t.Fatalf("normalizeExecutionRoot(\"  \") = %q, want \"\"", got)
	}
	cwd, _ := filepath.Abs(".")
	want := filepath.Clean(cwd)
	if got := normalizeExecutionRoot("."); got != want {
		t.Fatalf("normalizeExecutionRoot(\".\") = %q, want %q", got, want)
	}
}

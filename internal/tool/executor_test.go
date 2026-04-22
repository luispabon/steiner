package tool

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/config"
)

var (
	coreToolsBinaryOnce sync.Once
	coreToolsBinaryPath string
	coreToolsBinaryErr  error
)

func TestExecutorRunsCoreToolsAgainstTempRepo(t *testing.T) {
	bin := mustBuildCoreToolsBinary(t)
	tempRepo := t.TempDir()

	mustWriteFile(t, filepath.Join(tempRepo, "notes.txt"), "hello\nworld\n")
	mustWriteFile(t, filepath.Join(tempRepo, "docs", "readme.md"), "alpha\nbeta\n")

	reg := newCoreToolsRegistry(bin)
	cfg := config.Config{
		Approval: config.ApprovalConfig{
			Default: config.ApprovalModePrompt,
			Overrides: map[string]config.ApprovalMode{
				"read":   config.ApprovalModeAuto,
				"glob":   config.ApprovalModeAuto,
				"search": config.ApprovalModeAuto,
				"edit":   config.ApprovalModePrompt,
				"write":  config.ApprovalModePrompt,
				"bash":   config.ApprovalModePrompt,
			},
		},
	}

	var approvals []string
	var previews []ApprovalPreview
	executor := NewExecutor(reg, cfg, ApprovalResponderFunc(func(ctx context.Context, req ApprovalRequest) error {
		approvals = append(approvals, req.Tool.Name+":"+string(req.Mode))
		previews = append(previews, req.Preview)
		req.Response <- ApprovalResponse{Allow: true}
		return nil
	}), tempRepo)

	readResult, err := executor.Execute(context.Background(), "read", map[string]any{"path": "notes.txt"})
	if err != nil {
		t.Fatalf("read execute: %v", err)
	}
	readEnvelope, ok := readResult.(ExecutionResult)
	if !ok {
		t.Fatalf("read result type = %T, want tool.ExecutionResult", readResult)
	}
	readMap, ok := readEnvelope.Value.(map[string]any)
	if !ok {
		t.Fatalf("read result value type = %T, want map[string]any", readEnvelope.Value)
	}
	if got := readMap["contents"]; got != "hello\nworld\n" {
		t.Fatalf("read contents = %v, want hello\\nworld\\n", got)
	}

	globResult, err := executor.Execute(context.Background(), "glob", map[string]any{"pattern": "*.txt"})
	if err != nil {
		t.Fatalf("glob execute: %v", err)
	}
	globEnvelope := globResult.(ExecutionResult)
	globMap := globEnvelope.Value.(map[string]any)
	matches := globMap["matches"].([]any)
	if len(matches) != 1 || matches[0] != "notes.txt" {
		t.Fatalf("glob matches = %#v, want [notes.txt]", matches)
	}

	searchResult, err := executor.Execute(context.Background(), "search", map[string]any{"query": "world"})
	if err != nil {
		t.Fatalf("search execute: %v", err)
	}
	searchEnvelope := searchResult.(ExecutionResult)
	searchMap := searchEnvelope.Value.(map[string]any)
	searchMatches := searchMap["matches"].([]any)
	if len(searchMatches) != 1 {
		t.Fatalf("search matches length = %d, want 1", len(searchMatches))
	}

	writeResult, err := executor.Execute(context.Background(), "write", map[string]any{
		"path":     "generated.txt",
		"contents": "done\n",
	})
	if err != nil {
		t.Fatalf("write execute: %v", err)
	}
	writeEnvelope := writeResult.(ExecutionResult)
	writeMap := writeEnvelope.Value.(map[string]any)
	if got := writeMap["path"]; got != filepath.Join(tempRepo, "generated.txt") {
		t.Fatalf("write path = %v, want %s", got, filepath.Join(tempRepo, "generated.txt"))
	}

	editResult, err := executor.Execute(context.Background(), "edit", map[string]any{
		"path": "generated.txt",
		"old":  "done\n",
		"new":  "updated\n",
	})
	if err != nil {
		t.Fatalf("edit execute: %v", err)
	}
	editEnvelope := editResult.(ExecutionResult)
	editMap := editEnvelope.Value.(map[string]any)
	if got := editMap["path"]; got != filepath.Join(tempRepo, "generated.txt") {
		t.Fatalf("edit path = %v, want %s", got, filepath.Join(tempRepo, "generated.txt"))
	}
	if got := editMap["replacements"]; got != float64(1) {
		t.Fatalf("edit replacements = %v, want 1", got)
	}

	bashResult, err := executor.Execute(context.Background(), "bash", map[string]any{
		"command": "pwd",
	})
	if err != nil {
		t.Fatalf("bash execute: %v", err)
	}
	bashEnvelope := bashResult.(ExecutionResult)
	bashMap := bashEnvelope.Value.(map[string]any)
	if got := strings.TrimSpace(bashMap["stdout"].(string)); got != tempRepo {
		t.Fatalf("bash stdout = %q, want repo root %q", got, tempRepo)
	}

	if got := string(mustReadFile(t, filepath.Join(tempRepo, "generated.txt"))); got != "updated\n" {
		t.Fatalf("generated file contents = %q, want updated\\n", got)
	}

	wantApprovals := []string{"write:prompt", "edit:prompt", "bash:prompt"}
	if len(approvals) != len(wantApprovals) {
		t.Fatalf("approvals = %#v, want %#v", approvals, wantApprovals)
	}
	for i, want := range wantApprovals {
		if approvals[i] != want {
			t.Fatalf("approvals[%d] = %q, want %q", i, approvals[i], want)
		}
	}
	if len(previews) != 3 {
		t.Fatalf("previews len = %d, want 3", len(previews))
	}
	if got, want := previews[0].Tool, "write"; got != want {
		t.Fatalf("write preview tool = %q, want %q", got, want)
	}
	if got, want := previews[0].WorkDir, tempRepo; got != want {
		t.Fatalf("write preview workdir = %q, want %q", got, want)
	}
	if got, want := previews[0].Fields[0].Name, "path"; got != want {
		t.Fatalf("write preview field name = %q, want %q", got, want)
	}
	if got, want := previews[0].Fields[0].Value, filepath.Join(tempRepo, "generated.txt"); got != want {
		t.Fatalf("write preview path = %q, want %q", got, want)
	}
	if got, want := previews[1].Tool, "edit"; got != want {
		t.Fatalf("edit preview tool = %q, want %q", got, want)
	}
	if got, want := previews[1].Fields[0].Name, "new"; got != want {
		t.Fatalf("edit preview first field name = %q, want %q", got, want)
	}
	if got, want := previews[1].Fields[1].Name, "old"; got != want {
		t.Fatalf("edit preview second field name = %q, want %q", got, want)
	}
	if got, want := previews[1].Fields[2].Name, "path"; got != want {
		t.Fatalf("edit preview third field name = %q, want %q", got, want)
	}
	if got, want := previews[2].Tool, "bash"; got != want {
		t.Fatalf("bash preview tool = %q, want %q", got, want)
	}
	if got, want := previews[2].Fields[0].Name, "cwd"; got != want {
		t.Fatalf("bash preview field name = %q, want %q", got, want)
	}
	if got, want := previews[2].Fields[0].Value, tempRepo; got != want {
		t.Fatalf("bash preview cwd = %q, want %q", got, want)
	}
	if got, want := previews[2].Fields[1].Name, "command"; got != want {
		t.Fatalf("bash preview second field name = %q, want %q", got, want)
	}
	if got, want := previews[2].Fields[1].Value, "pwd"; got != want {
		t.Fatalf("bash preview command = %q, want %q", got, want)
	}
	if got := string(mustReadFile(t, filepath.Join(tempRepo, "generated.txt"))); got != "updated\n" {
		t.Fatalf("edited file contents = %q, want updated\\n", got)
	}
}

func TestExecutorWaitsForApprovalResponseChannel(t *testing.T) {
	bin := mustBuildCoreToolsBinary(t)
	tempRepo := t.TempDir()

	reg := newCoreToolsRegistry(bin)
	cfg := config.Config{
		Approval: config.ApprovalConfig{
			Default: config.ApprovalModePrompt,
			Overrides: map[string]config.ApprovalMode{
				"write": config.ApprovalModePrompt,
			},
		},
	}

	requestSeen := make(chan ApprovalRequest, 1)
	release := make(chan struct{})
	executor := NewExecutor(reg, cfg, ApprovalResponderFunc(func(ctx context.Context, req ApprovalRequest) error {
		requestSeen <- req
		go func() {
			<-release
			req.Response <- ApprovalResponse{Allow: true, Message: "approved"}
		}()
		return nil
	}), tempRepo)

	done := make(chan error, 1)
	go func() {
		_, err := executor.Execute(context.Background(), "write", map[string]any{
			"path":     "generated.txt",
			"contents": "done\n",
		})
		done <- err
	}()

	req := <-requestSeen
	if req.Response == nil {
		t.Fatal("approval request response channel = nil, want channel")
	}
	select {
	case err := <-done:
		t.Fatalf("Execute() returned early with err=%v", err)
	case <-time.After(50 * time.Millisecond):
	}

	close(release)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Execute() did not resume after approval response")
	}
}

func TestExecutorReturnsStructuredErrorForMissingFile(t *testing.T) {
	bin := mustBuildCoreToolsBinary(t)
	tempRepo := t.TempDir()

	reg := newCoreToolsRegistry(bin)
	cfg := config.Config{
		Approval: config.ApprovalConfig{
			Default: config.ApprovalModePrompt,
			Overrides: map[string]config.ApprovalMode{
				"read": config.ApprovalModeAuto,
			},
		},
	}

	executor := NewExecutor(reg, cfg, nil, tempRepo)
	_, err := executor.Execute(context.Background(), "read", map[string]any{"path": "missing.txt"})
	if err == nil {
		t.Fatal("Execute() error = nil, want structured file error")
	}

	var toolErr *ToolExecutionError
	if !errors.As(err, &toolErr) {
		t.Fatalf("error type = %T, want *ToolExecutionError", err)
	}
	if toolErr.Kind != "read_error" {
		t.Fatalf("error kind = %q, want read_error", toolErr.Kind)
	}
	if toolErr.ExitCode == 0 {
		t.Fatalf("error exit code = %d, want non-zero", toolErr.ExitCode)
	}
	if !strings.Contains(toolErr.Message, "missing.txt") {
		t.Fatalf("error message = %q, want missing file reference", toolErr.Message)
	}
}

func TestExecutorRejectsUnsafePathsBeforeExecution(t *testing.T) {
	root := t.TempDir()
	blocked := filepath.Join(root, "blocked")
	if err := os.MkdirAll(blocked, 0o755); err != nil {
		t.Fatalf("mkdir blocked: %v", err)
	}

	bin := mustBuildCoreToolsBinary(t)
	reg := newCoreToolsRegistry(bin)
	cfg := config.Config{
		Paths: config.PathsConfig{
			ProjectRootOnly: true,
			BlockedPaths:    []string{blocked},
		},
		Approval: config.ApprovalConfig{
			Default: config.ApprovalModeAuto,
			Overrides: map[string]config.ApprovalMode{
				"read":  config.ApprovalModeAuto,
				"write": config.ApprovalModeAuto,
				"edit":  config.ApprovalModeAuto,
				"bash":  config.ApprovalModeAuto,
			},
		},
	}

	executor := NewExecutor(reg, cfg, nil, root)

	tests := []struct {
		name  string
		tool  string
		input map[string]any
		want  string
	}{
		{
			name:  "read out of root",
			tool:  "read",
			input: map[string]any{"path": "../escape.txt"},
			want:  "outside project root",
		},
		{
			name:  "read blocked path",
			tool:  "read",
			input: map[string]any{"path": filepath.Join("blocked", "note.txt")},
			want:  "blocked by policy",
		},
		{
			name:  "bash cwd out of root",
			tool:  "bash",
			input: map[string]any{"command": "pwd", "cwd": "../escape"},
			want:  "outside project root",
		},
		{
			name:  "edit out of root",
			tool:  "edit",
			input: map[string]any{"path": "../escape.txt", "old": "a", "new": "b"},
			want:  "outside current working directory",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := executor.Execute(context.Background(), tc.tool, tc.input)
			if err == nil {
				t.Fatal("Execute() error = nil, want policy rejection")
			}
			var toolErr *ToolExecutionError
			if !errors.As(err, &toolErr) {
				t.Fatalf("error type = %T, want *ToolExecutionError", err)
			}
			if tc.tool == "edit" {
				if toolErr.Kind != "edit_error" {
					t.Fatalf("error kind = %q, want edit_error", toolErr.Kind)
				}
			} else if toolErr.Kind != "policy_denied" {
				t.Fatalf("error kind = %q, want policy_denied", toolErr.Kind)
			}
			if !strings.Contains(toolErr.Message, tc.want) {
				t.Fatalf("error message = %q, want %q", toolErr.Message, tc.want)
			}
		})
	}
}

func TestExecutorDeniesBlockedMutationWithoutApproval(t *testing.T) {
	root := t.TempDir()
	blocked := filepath.Join(root, "blocked")
	mustWriteFile(t, filepath.Join(blocked, "secret.txt"), "secret\n")

	bin := mustBuildCoreToolsBinary(t)
	reg := newCoreToolsRegistry(bin)
	cfg := config.Config{
		Paths: config.PathsConfig{
			ProjectRootOnly: true,
			BlockedPaths:    []string{blocked},
		},
		Approval: config.ApprovalConfig{
			Default: config.ApprovalModePrompt,
			Overrides: map[string]config.ApprovalMode{
				"write": config.ApprovalModePrompt,
			},
		},
	}

	approvals := 0
	executor := NewExecutor(reg, cfg, ApprovalResponderFunc(func(ctx context.Context, req ApprovalRequest) error {
		approvals++
		req.Response <- ApprovalResponse{Allow: true}
		return nil
	}), root)

	_, err := executor.Execute(context.Background(), "write", map[string]any{
		"path":     filepath.Join("blocked", "secret.txt"),
		"contents": "updated\n",
	})
	if err == nil {
		t.Fatal("Execute() error = nil, want policy rejection")
	}

	var toolErr *ToolExecutionError
	if !errors.As(err, &toolErr) {
		t.Fatalf("error type = %T, want *ToolExecutionError", err)
	}
	if toolErr.Kind != "policy_denied" {
		t.Fatalf("error kind = %q, want policy_denied", toolErr.Kind)
	}
	if !strings.Contains(toolErr.Message, "blocked by policy") {
		t.Fatalf("error message = %q, want blocked-path policy rejection", toolErr.Message)
	}
	if approvals != 0 {
		t.Fatalf("approval callbacks = %d, want 0", approvals)
	}
}

func TestExecutorEditRequiresExactSingleMatch(t *testing.T) {
	bin := mustBuildCoreToolsBinary(t)
	tempRepo := t.TempDir()
	mustWriteFile(t, filepath.Join(tempRepo, "notes.txt"), "alpha\nbeta\nalpha\n")

	reg := newCoreToolsRegistry(bin)
	cfg := config.Config{
		Approval: config.ApprovalConfig{
			Default: config.ApprovalModePrompt,
			Overrides: map[string]config.ApprovalMode{
				"edit": config.ApprovalModePrompt,
			},
		},
	}

	executor := NewExecutor(reg, cfg, ApprovalResponderFunc(func(ctx context.Context, req ApprovalRequest) error {
		req.Response <- ApprovalResponse{Allow: true}
		return nil
	}), tempRepo)

	tests := []struct {
		name  string
		input map[string]any
		want  string
	}{
		{
			name:  "missing old snippet",
			input: map[string]any{"path": "notes.txt", "old": "gamma", "new": "delta"},
			want:  "old snippet not found",
		},
		{
			name:  "ambiguous old snippet",
			input: map[string]any{"path": "notes.txt", "old": "alpha", "new": "delta"},
			want:  "must match exactly once",
		},
		{
			name:  "malformed replacement",
			input: map[string]any{"path": "notes.txt", "old": "   ", "new": "delta"},
			want:  "old is required",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := executor.Execute(context.Background(), "edit", tc.input)
			if err == nil {
				t.Fatal("Execute() error = nil, want edit failure")
			}
			var toolErr *ToolExecutionError
			if !errors.As(err, &toolErr) {
				t.Fatalf("error type = %T, want *ToolExecutionError", err)
			}
			if toolErr.Kind != "edit_error" && toolErr.Kind != "invalid_input" {
				t.Fatalf("error kind = %q, want edit_error or invalid_input", toolErr.Kind)
			}
			if !strings.Contains(toolErr.Message, tc.want) {
				t.Fatalf("error message = %q, want %q", toolErr.Message, tc.want)
			}
		})
	}
}

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

func newCoreToolsRegistry(bin string) *Registry {
	reg := NewRegistry(
		ToolDef{Name: "read", ExecPath: bin, Subcommand: "read", Description: "Read a file"},
		ToolDef{Name: "glob", ExecPath: bin, Subcommand: "glob", Description: "Glob files"},
		ToolDef{Name: "search", ExecPath: bin, Subcommand: "search", Description: "Search files"},
		ToolDef{Name: "edit", ExecPath: bin, Subcommand: "edit", Description: "Edit a file with exact replacement"},
		ToolDef{Name: "write", ExecPath: bin, Subcommand: "write", Description: "Write a file"},
		ToolDef{Name: "bash", ExecPath: bin, Subcommand: "bash", Description: "Run shell commands"},
	)
	return reg
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
	default:
		fmt.Fprint(os.Stdout, "{\"ok\":true,\"result\":null}")
	}
}
`

func mustBuildCoreToolsBinary(t *testing.T) string {
	t.Helper()

	coreToolsBinaryOnce.Do(func() {
		root := repoRoot()
		dir, err := os.MkdirTemp("", "steiner-core-tools-*")
		if err != nil {
			coreToolsBinaryErr = err
			return
		}
		path := filepath.Join(dir, "steiner-core-tools")
		cmd := exec.Command("go", "build", "-o", path, "./cmd/steiner-core-tools")
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
		output, err := cmd.CombinedOutput()
		if err != nil {
			coreToolsBinaryErr = errors.New(strings.TrimSpace(string(output)))
			if coreToolsBinaryErr.Error() == "" {
				coreToolsBinaryErr = err
			}
			return
		}
		coreToolsBinaryPath = path
	})

	if coreToolsBinaryErr != nil {
		t.Fatalf("build core tools binary: %v", coreToolsBinaryErr)
	}
	return coreToolsBinaryPath
}

func repoRoot() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		panic("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func mustWriteFile(t *testing.T, path, contents string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write file %s: %v", path, err)
	}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file %s: %v", path, err)
	}
	return data
}

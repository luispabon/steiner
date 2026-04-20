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
				"write":  config.ApprovalModePrompt,
				"bash":   config.ApprovalModePrompt,
			},
		},
	}

	var approvals []string
	executor := NewExecutor(reg, cfg, ApproverFunc(func(ctx context.Context, req ApprovalRequest) (ApprovalResponse, error) {
		approvals = append(approvals, req.Tool.Name+":"+string(req.Mode))
		return ApprovalResponse{Allow: true}, nil
	}), tempRepo)

	readResult, err := executor.Execute(context.Background(), "read", map[string]any{"path": "notes.txt"})
	if err != nil {
		t.Fatalf("read execute: %v", err)
	}
	readMap, ok := readResult.(map[string]any)
	if !ok {
		t.Fatalf("read result type = %T, want map[string]any", readResult)
	}
	if got := readMap["contents"]; got != "hello\nworld\n" {
		t.Fatalf("read contents = %v, want hello\\nworld\\n", got)
	}

	globResult, err := executor.Execute(context.Background(), "glob", map[string]any{"pattern": "*.txt"})
	if err != nil {
		t.Fatalf("glob execute: %v", err)
	}
	globMap := globResult.(map[string]any)
	matches := globMap["matches"].([]any)
	if len(matches) != 1 || matches[0] != "notes.txt" {
		t.Fatalf("glob matches = %#v, want [notes.txt]", matches)
	}

	searchResult, err := executor.Execute(context.Background(), "search", map[string]any{"query": "world"})
	if err != nil {
		t.Fatalf("search execute: %v", err)
	}
	searchMap := searchResult.(map[string]any)
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
	writeMap := writeResult.(map[string]any)
	if got := writeMap["path"]; got != "generated.txt" {
		t.Fatalf("write path = %v, want generated.txt", got)
	}

	bashResult, err := executor.Execute(context.Background(), "bash", map[string]any{
		"command": "pwd",
	})
	if err != nil {
		t.Fatalf("bash execute: %v", err)
	}
	bashMap := bashResult.(map[string]any)
	if got := strings.TrimSpace(bashMap["stdout"].(string)); got != tempRepo {
		t.Fatalf("bash stdout = %q, want repo root %q", got, tempRepo)
	}

	if got := string(mustReadFile(t, filepath.Join(tempRepo, "generated.txt"))); got != "done\n" {
		t.Fatalf("generated file contents = %q, want done\\n", got)
	}

	wantApprovals := []string{"write:prompt", "bash:prompt"}
	if len(approvals) != len(wantApprovals) {
		t.Fatalf("approvals = %#v, want %#v", approvals, wantApprovals)
	}
	for i, want := range wantApprovals {
		if approvals[i] != want {
			t.Fatalf("approvals[%d] = %q, want %q", i, approvals[i], want)
		}
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

func newCoreToolsRegistry(bin string) *Registry {
	reg := NewRegistry(
		ToolDef{Name: "read", ExecPath: bin, Subcommand: "read", Description: "Read a file"},
		ToolDef{Name: "glob", ExecPath: bin, Subcommand: "glob", Description: "Glob files"},
		ToolDef{Name: "search", ExecPath: bin, Subcommand: "search", Description: "Search files"},
		ToolDef{Name: "write", ExecPath: bin, Subcommand: "write", Description: "Write a file"},
		ToolDef{Name: "bash", ExecPath: bin, Subcommand: "bash", Description: "Run shell commands"},
	)
	return reg
}

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

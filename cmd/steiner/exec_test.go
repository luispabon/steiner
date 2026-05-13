package main

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

func TestExecModeReadsPromptFromStdin(t *testing.T) {
	oldBuildRuntime := buildRuntime
	t.Cleanup(func() { buildRuntime = oldBuildRuntime })

	var stdout, stderr bytes.Buffer
	buildRuntime = func(_ context.Context, _ *cobra.Command, _ *cliFlags) (cliRuntime, error) {
		cfg := testRuntimeConfig("test-model")
		return cliRuntime{
			cfg: cfg,
			provider: &fakeProvider{
				responses: []provider.ChatResponse{
					{
						Message: provider.Message{
							Role:    provider.MessageRoleAssistant,
							Content: "stdin answer",
						},
						FinishReason: "stop",
						Usage:        &provider.UsageStats{TotalTokens: 2},
					},
				},
			},
			registry:    tool.NewRegistry(),
			toolNames:   nil,
			skillNames:  nil,
			workDir:     t.TempDir(),
			homeDir:     t.TempDir(),
			human:       output.NewStream(&stdout),
			status:      output.NewStream(&stderr),
			events:      output.NewStream(&stdout),
			sharedInput: bufio.NewReader(strings.NewReader("fix from stdin\n")),
		}, nil
	}

	cmd := newRootCommand()
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--exec"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if got := stdout.String(); !strings.Contains(got, "stdin answer") {
		t.Fatalf("stdout = %q, want stdin answer", got)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestExecModeEmptyPromptReturnsError(t *testing.T) {
	oldBuildRuntime := buildRuntime
	t.Cleanup(func() { buildRuntime = oldBuildRuntime })

	buildRuntime = func(_ context.Context, _ *cobra.Command, _ *cliFlags) (cliRuntime, error) {
		cfg := testRuntimeConfig("test-model")
		return cliRuntime{
			cfg:         cfg,
			provider:    &fakeProvider{},
			registry:    tool.NewRegistry(),
			workDir:     t.TempDir(),
			homeDir:     t.TempDir(),
			human:       output.NewStream(io.Discard),
			status:      output.NewStream(io.Discard),
			events:      output.NoopSink{},
			sharedInput: bufio.NewReader(strings.NewReader("\n")),
		}, nil
	}

	cmd := newRootCommand()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--exec"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute() error = nil, want 'exec mode requires a prompt'")
	}
	if !strings.Contains(err.Error(), "exec mode requires a prompt") {
		t.Fatalf("Execute() error = %v, want prompt required error", err)
	}
}

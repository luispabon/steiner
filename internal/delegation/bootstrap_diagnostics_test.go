package delegation

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/luispabon/steiner/internal/diagnostics"
	"github.com/luispabon/steiner/internal/tool"
)

func TestBuildChildRunRequestCarriesDiagnostics(t *testing.T) {
	t.Parallel()
	writer, err := diagnostics.New(diagnostics.Options{Dir: t.TempDir(), Streams: diagnostics.Streams{Tool: true}})
	if err != nil {
		t.Fatalf("diagnostics.New() error = %v", err)
	}
	t.Cleanup(func() { _ = writer.Close() })

	req := buildChildRunRequest(childRunRequestParams{
		WorkDir:     "/tmp/work",
		AgentID:     "diagnostics-child",
		VisibleReg:  tool.NewRegistry(),
		ExecReg:     tool.NewRegistry(),
		Diagnostics: writer,
	})
	if req.Diagnostics != writer {
		t.Errorf("req.Diagnostics = %v, want the supplied writer", req.Diagnostics)
	}
}

// TestBuildChildRunRequestToolStreamAttribution asserts that the child run
// request's executor writes tool-stream records tagged with the sub-agent
// source, agent id and agent type.
func TestBuildChildRunRequestToolStreamAttribution(t *testing.T) {
	diagDir := t.TempDir()
	writer, err := diagnostics.New(diagnostics.Options{Dir: diagDir, Streams: diagnostics.Streams{Tool: true}})
	if err != nil {
		t.Fatalf("diagnostics.New() error = %v", err)
	}
	t.Cleanup(func() { _ = writer.Close() })

	reg := tool.NewRegistry(tool.ToolDef{
		Name:    "read",
		Handler: func(_ context.Context, _ map[string]any) (any, error) { return "ok", nil },
	})
	req := buildChildRunRequest(childRunRequestParams{
		WorkDir:     "/tmp/work",
		AgentID:     "child-42",
		AgentType:   AgentTypeCode,
		VisibleReg:  reg,
		ExecReg:     reg,
		Diagnostics: writer,
	})

	if _, err := req.Executor.Execute(context.Background(), "read", "", map[string]any{}); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(diagDir, "tool.jsonl"))
	if err != nil {
		t.Fatalf("read tool.jsonl: %v", err)
	}
	var rec diagnostics.Record
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatalf("unmarshal record: %v", err)
	}
	if rec.Source != diagnostics.SourceSubAgent {
		t.Errorf("source = %q, want %q", rec.Source, diagnostics.SourceSubAgent)
	}
	if rec.AgentID != "child-42" || rec.AgentType != "code" {
		t.Errorf("scope = %q/%q, want child-42/code", rec.AgentID, rec.AgentType)
	}
}

func TestBuildChildRunRequestWithoutDiagnostics(t *testing.T) {
	t.Parallel()
	req := buildChildRunRequest(childRunRequestParams{
		WorkDir:    "/tmp/work",
		AgentID:    "no-diagnostics-child",
		VisibleReg: tool.NewRegistry(),
		ExecReg:    tool.NewRegistry(),
	})
	if req.Diagnostics != nil {
		t.Errorf("req.Diagnostics = %v, want nil when none is supplied", req.Diagnostics)
	}
}

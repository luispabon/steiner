package delegation

import (
	"testing"

	"github.com/luispabon/steiner/internal/diagnostics"
	"github.com/luispabon/steiner/internal/tool"
)

func TestBuildChildRunRequestCarriesDiagnostics(t *testing.T) {
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

func TestBuildChildRunRequestWithoutDiagnostics(t *testing.T) {
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

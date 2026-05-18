package tool

import (
	"context"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/config"
)

func TestDummyTools(t *testing.T) {
	tests := []struct {
		name     string
		toolDef  func() ToolDef
		wantName string
	}{
		{name: "web_search", toolDef: DummyWebSearchTool, wantName: "web_search"},
		{name: "fetch_url", toolDef: DummyFetchURLTool, wantName: "fetch_url"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			def := tt.toolDef()

			if def.Name != tt.wantName {
				t.Errorf("Name = %q, want %q", def.Name, tt.wantName)
			}

			if def.Description == "" {
				t.Error("Description is empty")
			}

			if def.Handler == nil {
				t.Fatal("Handler is nil")
			}

			if def.Approval != config.ApprovalModeAuto {
				t.Errorf("Approval = %q, want %q", def.Approval, config.ApprovalModeAuto)
			}

			result, err := def.Handler(context.Background(), map[string]any{})
			if err != nil {
				t.Fatalf("Handler returned unexpected error: %v", err)
			}

			m, ok := result.(map[string]any)
			if !ok {
				t.Fatalf("Handler result is %T, want map[string]any", result)
			}

			errVal, hasErr := m["error"]
			if !hasErr {
				t.Fatal("Handler result missing 'error' field")
			}

			errStr, ok := errVal.(string)
			if !ok {
				t.Fatalf("Handler result 'error' field is %T, want string", errVal)
			}

			if !strings.Contains(errStr, "not yet implemented") {
				t.Errorf("Handler result error = %q, want it to contain 'not yet implemented'", errStr)
			}
		})
	}
}

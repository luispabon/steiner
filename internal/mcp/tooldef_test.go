package mcp

import (
	"context"
	"errors"
	"reflect"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/luispabon/steiner/internal/tool"
)

func TestMCPToolDefSchema(t *testing.T) {
	sess := &Session{name: "fixture"}

	tests := []struct {
		name  string
		input any
		want  map[string]any
	}{
		{
			name:  "nil input schema gets the default empty object schema",
			input: nil,
			want:  map[string]any{"type": "object", "properties": map[string]any{}},
		},
		{
			name:  "non-map input schema gets the default empty object schema",
			input: "not a schema",
			want:  map[string]any{"type": "object", "properties": map[string]any{}},
		},
		{
			name:  "valid input schema passes through",
			input: map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}},
			want:  map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			def := mcpToolDef(sess, &mcpsdk.Tool{Name: "echo", Description: "echoes text", InputSchema: tt.input}, nil)
			if !reflect.DeepEqual(def.ParameterSchema, tt.want) {
				t.Errorf("ParameterSchema = %#v, want %#v", def.ParameterSchema, tt.want)
			}
		})
	}
}

func TestMCPToolDefProvenance(t *testing.T) {
	sess := &Session{name: "fixture"}
	def := mcpToolDef(sess, &mcpsdk.Tool{Name: "echo", Description: "echoes text"}, nil)

	if def.Name != "mcp__fixture__echo" {
		t.Errorf("Name = %q, want %q", def.Name, "mcp__fixture__echo")
	}
	want := tool.MCPProvenance{Server: "fixture", ToolName: "echo"}
	if def.MCP != want {
		t.Errorf("MCP = %+v, want %+v", def.MCP, want)
	}
	if def.Handler == nil {
		t.Error("Handler is nil, want non-nil")
	}
	// Handler dispatch only: no subprocess fields.
	if def.ExecPath != "" || def.Subcommand != "" || def.Timeout != 0 {
		t.Errorf("ExecPath=%q Subcommand=%q Timeout=%v, want all zero", def.ExecPath, def.Subcommand, def.Timeout)
	}
}

func TestMCPHandlerFailClosed(t *testing.T) {
	t.Run("nil approver denies", func(t *testing.T) {
		sess := &Session{name: "fixture"}
		def := mcpToolDef(sess, &mcpsdk.Tool{Name: "echo"}, nil)

		env, err := def.Handler(context.Background(), map[string]any{"text": "hi"})
		assertDenial(t, env, err)
	})

	t.Run("approver error denies", func(t *testing.T) {
		approver := tool.ApprovalResponderFunc(func(context.Context, tool.ApprovalRequest) error {
			return errors.New("approval transport down")
		})
		sess := &Session{name: "fixture"}
		def := mcpToolDef(sess, &mcpsdk.Tool{Name: "echo"}, approver)

		env, err := def.Handler(context.Background(), map[string]any{"text": "hi"})
		assertDenial(t, env, err)
	})
}

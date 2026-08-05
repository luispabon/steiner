package mcp

import (
	"context"
	"fmt"
	"strings"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/luispabon/steiner/internal/tool"
)

// mcpToolDef builds a tool.ToolDef for a discovered MCP tool.
// planMode is captured by the handler closure for approval gating; it is not
// consulted yet (step 7 owns the gate logic).
func mcpToolDef(session *Session, t *mcpsdk.Tool, approver tool.ApprovalResponder, planMode bool) tool.ToolDef {
	name := ToolName(session.Name(), t.Name)

	schema, ok := t.InputSchema.(map[string]any)
	if !ok || schema == nil {
		schema = map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		}
	}

	def := tool.ToolDef{
		Name:            name,
		Description:     t.Description,
		ParameterSchema: schema,
		MCP: tool.MCPProvenance{
			Server:   session.Name(),
			ToolName: t.Name,
		},
	}
	// ExecPath, Subcommand, and Timeout are intentionally NOT set — Handler
	// dispatch is sufficient (internal/tool/execution_pipeline.go).

	def.Handler = mcpHandler(session, t.Name, session.Name(), def, approver, planMode)
	return def
}

// mcpHandler returns a Handler closure that gates on approval and invokes the MCP server.
// planMode is captured for step 7's plan-mode gate; it is unused today.
func mcpHandler(session *Session, toolName, serverName string, def tool.ToolDef, approver tool.ApprovalResponder, planMode bool) func(ctx context.Context, input map[string]any) (any, error) {
	return func(ctx context.Context, input map[string]any) (any, error) {
		// Fail closed: a nil approver denies.
		if approver == nil {
			return tool.JSONEnvelope{
				OK: false,
				Error: &tool.JSONEnvelopeError{
					Kind:    "approval_denied",
					Message: fmt.Sprintf("MCP tool %q on server %q cannot be called without an approver", toolName, serverName),
				},
			}, nil
		}

		req := tool.ApprovalRequest{
			Tool:              def,
			Input:             input,
			Kind:              tool.ApprovalKindMCP,
			Reason:            "MCP tool call",
			GrantInstructions: "Use approval mode in MCP server config to control this",
			Response:          make(chan tool.ApprovalResponse, 1),
			MCP: &tool.MCPApprovalDetails{
				Server:           serverName,
				ToolName:         toolName,
				ArgumentsPreview: "", // placeholder; step 7 will fill with formatted args
			},
		}
		if err := approver.RequestApproval(ctx, req); err != nil {
			return tool.JSONEnvelope{
				OK: false,
				Error: &tool.JSONEnvelopeError{
					Kind:    "approval_denied",
					Message: fmt.Sprintf("MCP tool %q on server %q: approval error: %v", toolName, serverName, err),
				},
			}, nil
		}

		resp := <-req.Response
		if !resp.Allow {
			return tool.JSONEnvelope{
				OK: false,
				Error: &tool.JSONEnvelopeError{
					Kind:    "approval_denied",
					Message: "user declined the MCP tool call",
				},
			}, nil
		}

		result, err := session.Call(ctx, toolName, input)
		if err != nil {
			return nil, fmt.Errorf("call mcp tool %q on server %q: %w", toolName, serverName, err)
		}

		// Flatten content: only text content is rendered; other types are named
		// but not decoded (image/audio/resource rendering is out of scope here).
		var parts []string
		for _, c := range result.Content {
			switch ct := c.(type) {
			case *mcpsdk.TextContent:
				parts = append(parts, ct.Text)
			default:
				parts = append(parts, fmt.Sprintf("[%T content not rendered]", ct))
			}
		}
		text := strings.Join(parts, "\n")

		// Tool-level failures surface as an envelope with a nil Go error; only
		// transport failures return non-nil errors.
		if result.IsError {
			return tool.JSONEnvelope{
				OK: false,
				Error: &tool.JSONEnvelopeError{
					Kind:    "mcp_tool_error",
					Message: text,
				},
			}, nil
		}

		return tool.JSONEnvelope{
			OK:     true,
			Result: text,
		}, nil
	}
}

package mcp

import (
	"context"
	"fmt"
	"sort"
	"strings"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/tool"
)

// mcpToolDef builds a tool.ToolDef for a discovered MCP tool.
// getApprover resolves the approval responder lazily at call time (the manager
// wires it in after connect, once the session approver exists); planMode and
// srv are captured by the handler closure for approval gating: planMode
// downgrades "allow" to "ask" (D7), and srv carries the per-server approval
// mode and trust_annotations opt-in. planMode is read from the closure at call
// time, so mode changes apply without rebuilding the registry.
func mcpToolDef(session *Session, t *mcpsdk.Tool, getApprover func() tool.ApprovalResponder, planMode func() bool, srv config.MCPServerConfig) tool.ToolDef {
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

	approvalMode := srv.Approval
	if approvalMode == "" {
		approvalMode = "ask"
	}
	def.Handler = mcpHandler(session, t.Name, session.Name(), def, getApprover, planMode, approvalMode, srv.TrustAnnotations, t.Annotations)
	return def
}

// mcpHandler returns a Handler closure that gates on approval and invokes the MCP server.
func mcpHandler(session *Session, toolName, serverName string, def tool.ToolDef, getApprover func() tool.ApprovalResponder, planMode func() bool, approvalMode string, trustAnnotations bool, annotations *mcpsdk.ToolAnnotations) func(ctx context.Context, input map[string]any) (any, error) {
	return func(ctx context.Context, input map[string]any) (any, error) {
		// Deny mode: deny servers register no tools, but the handler defends
		// anyway so a stale definition can never bypass the mode.
		if approvalMode == "deny" {
			return tool.JSONEnvelope{
				OK: false,
				Error: &tool.JSONEnvelopeError{
					Kind:    "approval_denied",
					Message: "MCP tool not available: server approval mode is 'deny'",
				},
			}, nil
		}

		// Allow mode in build mode calls the tool directly. In plan mode it is
		// downgraded to ask (D7) and falls through to approval below. planMode
		// is read live so a mode switch applies without rebuilding the registry.
		if approvalMode == "allow" && !planMode() {
			return callMCPTool(ctx, session, toolName, serverName, input)
		}

		// Trusted annotations can skip approval for clearly safe tools (D6:
		// trust_annotations defaults to false, so this runs only on explicit
		// opt-in). DestructiveHint and OpenWorldHint default to true per the MCP
		// spec when unset, so an empty annotation set still prompts. This runs
		// before the nil-approver check: a trusted read-only tool needs no
		// approver at all.
		if trustAnnotations && annotations != nil {
			if annotations.ReadOnlyHint {
				return callMCPTool(ctx, session, toolName, serverName, input)
			}
			isDestructive := annotations.DestructiveHint == nil || *annotations.DestructiveHint
			isOpenWorld := annotations.OpenWorldHint == nil || *annotations.OpenWorldHint
			if !isDestructive && !isOpenWorld {
				return callMCPTool(ctx, session, toolName, serverName, input)
			}
			// Destructive or open-world: fall through to approval.
		}

		// Fail closed: a nil approver denies.
		approver := getApprover()
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
				ArgumentsPreview: formatArgumentsPreview(input),
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

		return callMCPTool(ctx, session, toolName, serverName, input)
	}
}

// callMCPTool invokes the tool on the server and wraps the outcome in a
// JSONEnvelope. Tool-level failures surface as a non-OK envelope with a nil Go
// error; only transport failures return non-nil errors.
func callMCPTool(ctx context.Context, session *Session, toolName, serverName string, input map[string]any) (any, error) {
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

// formatArgumentsPreview renders the call arguments as sorted "key: value"
// lines for the approval prompt (D4 Option A; D8: formatted by the handler).
func formatArgumentsPreview(input map[string]any) string {
	if len(input) == 0 {
		return "no arguments"
	}
	var lines []string
	for k, v := range input {
		valStr := formatArgValue(v)
		lines = append(lines, fmt.Sprintf("%s: %s", k, valStr))
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

// formatArgValue renders one argument value, truncating long strings and
// collapsing nested structures so the preview stays bounded.
func formatArgValue(v any) string {
	switch val := v.(type) {
	case string:
		runes := []rune(val)
		if len(runes) > 60 {
			return string(runes[:57]) + "..."
		}
		return val
	case float64:
		return fmt.Sprintf("%v", val)
	case bool:
		return fmt.Sprintf("%v", val)
	case map[string]any, []any:
		return "{...}"
	default:
		s := []rune(fmt.Sprintf("%v", val))
		if len(s) > 60 {
			return string(s[:57]) + "..."
		}
		return string(s)
	}
}

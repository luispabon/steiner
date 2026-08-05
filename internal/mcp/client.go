// Package mcp connects steiner to Model Context Protocol servers over stdio.
// This is step-1 of the MCP walking skeleton: a long-lived server subprocess
// with JSON-RPC pipes crossing the (optional) bwrap sandbox boundary, plus
// process-group reaping so killing a session leaves no orphans.
package mcp

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const connectTimeout = 30 * time.Second

// clientVersion identifies the steiner MCP client during the handshake. The
// build version lives in cmd/steiner as main.version (injected via ldflags)
// and is not importable from internal packages.
const clientVersion = "dev"

// ServerSpec describes a stdio MCP server to launch.
type ServerSpec struct {
	Name    string
	Command string
	Args    []string
	Env     map[string]string
}

// Session is a connected stdio MCP server.
type Session struct {
	name            string
	sdk             *mcpsdk.ClientSession
	tools           []*mcpsdk.Tool
	cmd             *exec.Cmd
	protocolVersion string
}

// Connect launches the server described by spec, optionally wrapped (e.g. with
// the sandbox), and completes the MCP handshake. The negotiated protocol
// version and the server's tool list are captured; ListTools is called exactly
// once per session.
func Connect(ctx context.Context, spec ServerSpec, wrap func(*exec.Cmd) *exec.Cmd, stderr io.Writer) (*Session, error) {
	cmd := buildCommand(ctx, spec, wrap, stderr)

	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "steiner", Version: clientVersion}, nil)
	transport := &mcpsdk.CommandTransport{Command: cmd}

	ctx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()

	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return nil, fmt.Errorf("connect mcp server %q: %w", spec.Name, err)
	}

	s := &Session{
		name:            spec.Name,
		sdk:             session,
		cmd:             cmd,
		protocolVersion: session.InitializeResult().ProtocolVersion,
	}

	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		_ = session.Close() // Best-effort cleanup after a failed tool list.
		return nil, fmt.Errorf("list tools for mcp server %q: %w", spec.Name, err)
	}
	s.tools = tools.Tools
	return s, nil
}

// Name returns the server name from the spec passed to Connect.
func (s *Session) Name() string {
	return s.name
}

// ProtocolVersion returns the protocol version negotiated during the handshake.
func (s *Session) ProtocolVersion() string {
	return s.protocolVersion
}

// Tools returns the tools advertised by the server at connect time.
func (s *Session) Tools() []*mcpsdk.Tool {
	return s.tools
}

// Call invokes a tool on the server. Tool-level failures (isError) surface as
// IsError on the result with a nil Go error; transport errors return non-nil.
func (s *Session) Call(ctx context.Context, toolName string, args map[string]any) (*mcpsdk.CallToolResult, error) {
	return s.sdk.CallTool(ctx, &mcpsdk.CallToolParams{Name: toolName, Arguments: args})
}

// Close shuts the session down and reaps the server process.
func (s *Session) Close() error {
	return s.sdk.Close()
}

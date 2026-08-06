// Package mcp connects steiner to Model Context Protocol servers over stdio
// or HTTP transports. It handles transport construction, MCP handshake,
// protocol negotiation, and server lifecycle management.
package mcp

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	// DefaultConnectTimeout is the per-server MCP handshake timeout applied when
	// a server config leaves connect_timeout zero. It mirrors the config default
	// (15s) so direct ConnectSession callers get the same bound.
	DefaultConnectTimeout = 15 * time.Second
)

// clientVersion identifies the steiner MCP client during the handshake. The
// build version lives in cmd/steiner as main.version (injected via ldflags)
// and is not importable from internal packages.
const clientVersion = "dev"

// ServerSpec describes one MCP server to connect to. Transport selects which of the
// two field groups applies; config validation guarantees the other group is empty.
type ServerSpec struct {
	Name      string
	Transport string // "stdio" or "http"; empty is treated as "stdio"

	// stdio
	Command string
	Args    []string
	Env     map[string]string

	// http
	URL     string
	Headers map[string]string
}

// Session is a connected MCP server, over either the stdio or HTTP transport.
type Session struct {
	name            string
	sdk             *mcpsdk.ClientSession
	tools           []*mcpsdk.Tool
	cmd             *exec.Cmd
	protocolVersion string
}

// ConnectSession establishes a connection to an MCP server and completes the
// handshake. It dispatches to stdio or HTTP transport based on spec.Transport.
// For stdio, the server described by spec is launched, optionally wrapped (e.g.
// with the sandbox); for HTTP, a request is made to the configured endpoint.
// The negotiated protocol version and the server's tool list are captured;
// ListTools is called exactly once per session. timeout bounds the connect and
// the initial ListTools together; a zero or negative timeout falls back to
// DefaultConnectTimeout.
func ConnectSession(ctx context.Context, spec ServerSpec, wrap func(*exec.Cmd) *exec.Cmd, stderr io.Writer, timeout time.Duration) (*Session, error) {
	var transport mcpsdk.Transport
	var cmd *exec.Cmd

	switch spec.Transport {
	case "http":
		var err error
		transport, err = newHTTPTransport(spec)
		if err != nil {
			return nil, fmt.Errorf("connect mcp server %q: %w", spec.Name, err)
		}
	case "", "stdio":
		transport, cmd = newStdioTransport(ctx, spec, wrap, stderr)
	default:
		return nil, fmt.Errorf("connect mcp server %q: unsupported transport %q", spec.Name, spec.Transport)
	}

	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "steiner", Version: clientVersion}, nil)

	if timeout <= 0 {
		timeout = DefaultConnectTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
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

// Close shuts the session down. For stdio transport, it reaps the server process.
func (s *Session) Close() error {
	return s.sdk.Close()
}

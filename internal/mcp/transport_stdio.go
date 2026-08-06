package mcp

import (
	"context"
	"io"
	"os/exec"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// newStdioTransport constructs a stdio transport by launching the server
// described by spec, optionally wrapped (e.g. with the sandbox). It returns
// both the transport and the exec.Cmd; the cmd is kept in the Session for
// process reaping.
func newStdioTransport(ctx context.Context, spec ServerSpec, wrap func(*exec.Cmd) *exec.Cmd, stderr io.Writer) (mcpsdk.Transport, *exec.Cmd) {
	cmd := buildCommand(ctx, spec, wrap, stderr)
	transport := &mcpsdk.CommandTransport{Command: cmd}
	return transport, cmd
}

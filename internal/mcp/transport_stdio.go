package mcp

import (
	"context"
	"io"
	"os/exec"
	"sync"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// newStdioTransport constructs a stdio transport by launching the server
// command. The release closure is safe to call more than once.
func newStdioTransport(ctx context.Context, spec ServerSpec, wrap WrapFn, release ReleaseFn, stderr io.Writer) (mcpsdk.Transport, *exec.Cmd, func(), error) {
	cmd, wrapped, err := buildCommandTracked(ctx, spec, wrap, stderr)
	if err != nil {
		return nil, nil, nil, err
	}
	var once sync.Once
	releaseCommand := func() {
		once.Do(func() {
			if release != nil {
				release(wrapped)
			}
		})
	}
	return &releaseTransport{inner: &mcpsdk.CommandTransport{Command: cmd}, release: releaseCommand}, cmd, releaseCommand, nil
}

type releaseTransport struct {
	inner   mcpsdk.Transport
	release func()
}

func (t *releaseTransport) Connect(ctx context.Context) (mcpsdk.Connection, error) {
	defer t.release()
	return t.inner.Connect(ctx)
}

package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/lsp"
	"github.com/luispabon/steiner/internal/mcp"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/sandbox"
)

// connectRuntimeMCP connects every enabled MCP server in parallel and returns
// the manager. When asyncMCP is false it blocks (WaitInit) until every server
// resolves to connected or failed, so the caller's registry rebuild freezes the
// complete tool list; this is the non-interactive behaviour. When asyncMCP is
// true it returns immediately so an interactive TUI can paint while servers
// connect: the background MCP init will WaitInit and re-register the manager's
// tool defs, then arm the producer for full snapshots. The returned producer
// forwards pre-arm state changes as states-only snapshots (no registry origins)
// until armed, then switches to full snapshots with origins.
func connectRuntimeMCP(ctx context.Context, cfg config.Config, sb *sandbox.Sandbox, asyncMCP bool, events output.EventSink, stderr io.Writer) (*mcp.Manager, *mcpStateProducer) {
	var wrap func(*exec.Cmd) *exec.Cmd
	var release func(*exec.Cmd)
	if sb != nil {
		wrap = func(c *exec.Cmd) *exec.Cmd { return sb.WrapCommandMode(c, true) }
		release = sb.ReleaseCommandResources
	}
	diagnose := func(severity string) func(string) {
		return func(msg string) {
			events.Emit(output.NewContextDiagnosticsEvent(output.ContextDiagnosticsEvent{
				Kind:     "session_health",
				Severity: severity,
				Notes:    []string{msg},
			}))
		}
	}
	planMode := cfg.Modes.Default == config.ExecutionModePlan
	var producer *mcpStateProducer
	var onStateChange func()
	if asyncMCP {
		producer = &mcpStateProducer{}
		onStateChange = producer.stateChanged
	}
	mgr := mcp.Connect(ctx, cfg.MCP, cfg.Limits, wrap, release, planMode, diagnose("warning"), diagnose("info"), stderr, onStateChange)
	if !asyncMCP {
		// Block until every enabled server resolves (connected or failed) so
		// the registry below freezes the complete tool list. Connects run in
		// parallel, so startup latency is bounded by the slowest server. A
		// cancelled ctx marks the servers failed, which is what the sequential
		// Connect did, so the error is not actionable here.
		_ = mgr.WaitInit(ctx)
	}
	return mgr, producer
}

// connectRuntimeLSP constructs the language server manager when LSP is enabled.
// Unlike MCP there is no WaitInit — language servers start lazily on first tool call,
// so this never blocks CLI startup. Server warnings are routed through the same
// session_health diagnostic channel as MCP.
func connectRuntimeLSP(cfg config.Config, sb *sandbox.Sandbox, workDir string, events output.EventSink, stderr io.Writer) *lsp.Manager {
	var wrap func(*exec.Cmd) *exec.Cmd
	var release func(*exec.Cmd)
	if sb != nil {
		wrap = func(c *exec.Cmd) *exec.Cmd { return sb.WrapCommandMode(c, true) }
		release = sb.ReleaseCommandResources
	}
	warnFn := func(msg string) {
		events.Emit(output.NewContextDiagnosticsEvent(output.ContextDiagnosticsEvent{
			Kind:     "session_health",
			Severity: "warning",
			Notes:    []string{msg},
		}))
	}
	return lsp.NewManager(cfg.LSP, workDir, wrap, release, warnFn, stderr)
}

func buildLSPServerLogWriter(path string) (io.WriteCloser, error) {
	w, err := lsp.NewServerLogWriter(path)
	if err != nil {
		return nil, fmt.Errorf("lsp server log writer: %w", err)
	}
	return w, nil
}

// selectServerStderr picks the destination for server subprocess stderr: the
// derived log file when logPath is non-empty, io.Discard in interactive mode
// otherwise (terminal corruption is non-negotiable), or os.Stderr in
// non-interactive mode where there is no live TUI to trample. logPath must be
// derived from the same inputs used to build logWriter.
func selectServerStderr(logPath string, asyncMCP bool, logWriter io.Writer) io.Writer {
	if logPath != "" {
		return logWriter
	}
	if asyncMCP {
		return io.Discard
	}
	return os.Stderr
}

func buildMCPServerLogWriter(path string) (io.WriteCloser, error) {
	w, err := mcp.NewServerLogWriter(path)
	if err != nil {
		return nil, fmt.Errorf("mcp server log writer: %w", err)
	}
	return w, nil
}

package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"

	"github.com/spf13/cobra"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/delegation"
	"github.com/luispabon/steiner/internal/history"
	"github.com/luispabon/steiner/internal/mcp"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/sandbox"
	"github.com/luispabon/steiner/internal/session"
	"github.com/luispabon/steiner/internal/tool"
	"github.com/luispabon/steiner/internal/usagestats"
)

type cliFlags struct {
	configPath        string
	model             string
	verbose           bool
	exec              bool
	logFile           string
	compactionLogFile string
	maxTurns          int
	enableStreaming   bool
	resume            string
	unsafe            bool
	dev               bool
	// asyncMCP makes MCP connects non-blocking so the interactive TUI paints
	// while servers connect; the interactive session runner waits for every
	// server before the first agent turn. Non-interactive commands keep the
	// blocking behaviour (set false, the zero value).
	asyncMCP bool
}

type cliRuntime struct {
	cfg                    config.Config
	provider               provider.Provider
	providerFactory        func(provider.ResolvedModel) (provider.Provider, error)
	httpClient             *http.Client
	registry               *tool.Registry
	toolNames              []string
	skillNames             []string
	skillSources           map[string]string // skill name -> "project"/"user"/"global"
	skillDescriptions      map[string]string // skill name -> short summary
	skillBundledFS         fs.FS             // embedded bundled skill documents
	projectRoot            string
	workDir                string
	homeDir                string
	sandbox                *sandbox.Sandbox
	mcpManager             *mcp.Manager
	mcpState               *mcpStateProducer
	stdin                  io.Reader
	human                  *output.EventStream
	status                 *output.EventStream
	events                 output.EventSink
	sharedInput            *bufio.Reader
	approvalIn             *bufio.Reader
	closeFn                func() error
	historyWriter          *history.Writer
	sessionStore           *session.Store
	delegationSessionStore *delegation.SessionStore
	delegationLogger       *delegation.TraceLogger
	streamErrorLog         *provider.StreamErrorLogger
	compactionLogFile      string
	usageRecorder          *usagestats.Recorder
	imageStore             *agent.ImageStore
	visionCapabilities     *agent.VisionCapabilities
}

var buildRuntime = defaultBuildRuntime

func defaultBuildRuntime(ctx context.Context, cmd *cobra.Command, flags *cliFlags) (cliRuntime, error) {
	projectRoot, err := os.Getwd()
	if err != nil {
		return cliRuntime{}, fmt.Errorf("get working directory: %w", err)
	}
	return buildRuntimeWithRoots(ctx, cmd, flags, projectRoot, projectRoot, flags.model)
}

func closeRuntime(rt *cliRuntime) {
	if rt.imageStore != nil {
		_ = rt.imageStore.Cleanup()
	}
	// Terminate MCP servers before the sandbox tmp dir is removed.
	if rt.mcpManager != nil {
		emitCloseWarning(rt.events, "close mcp servers", rt.mcpManager.Close())
	}
	if rt.sandbox != nil {
		emitCloseWarning(rt.events, "sandbox tmp cleanup", rt.sandbox.Cleanup())
	}
	if rt.delegationLogger != nil {
		emitCloseWarning(rt.events, "failed to close delegation logger", rt.delegationLogger.Close())
	}
	if rt.streamErrorLog != nil {
		emitCloseWarning(rt.events, "close stream error log", rt.streamErrorLog.Close())
	}
	if rt.historyWriter != nil {
		emitCloseWarning(rt.events, "failed to close history writer", rt.historyWriter.Close())
	}
	if rt.closeFn != nil {
		emitCloseWarning(rt.events, "failed to close runtime", rt.closeFn())
	}
}

// emitCloseWarning emits a session_health warning diagnostic event if err is
// non-nil, prefixing the message with action.
func emitCloseWarning(events output.EventSink, action string, err error) {
	if err == nil {
		return
	}
	events.Emit(output.NewContextDiagnosticsEvent(output.ContextDiagnosticsEvent{
		Kind:     "session_health",
		Severity: "warning",
		Notes:    []string{fmt.Sprintf("%s: %v", action, err)},
	}))
}

func openApprovalInput(stdin io.Reader) (*bufio.Reader, func() error) {
	file, ok := stdin.(*os.File)
	if !ok || file != os.Stdin {
		return nil, nil
	}
	tty, err := os.Open("/dev/tty")
	if err != nil {
		return nil, nil
	}
	return bufio.NewReader(tty), tty.Close
}

func joinClosers(closers ...func() error) func() error {
	available := make([]func() error, 0, len(closers))
	for _, closer := range closers {
		if closer != nil {
			available = append(available, closer)
		}
	}
	if len(available) == 0 {
		return nil
	}
	return func() error {
		var firstErr error
		for _, closer := range available {
			if err := closer(); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		return firstErr
	}
}

func approvalReader(rt cliRuntime) *bufio.Reader {
	if rt.approvalIn != nil {
		return rt.approvalIn
	}
	return rt.sharedInput
}

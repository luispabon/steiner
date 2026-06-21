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

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/delegation"
	"github.com/luispabon/steiner/internal/history"
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
	if rt.delegationLogger != nil {
		if err := rt.delegationLogger.Close(); err != nil {
			rt.events.Emit(output.NewContextDiagnosticsEvent(output.ContextDiagnosticsEvent{
				Kind:     "session_health",
				Severity: "warning",
				Notes:    []string{fmt.Sprintf("failed to close delegation logger: %v", err)},
			}))
		}
	}
	if rt.streamErrorLog != nil {
		if err := rt.streamErrorLog.Close(); err != nil {
			rt.events.Emit(output.NewContextDiagnosticsEvent(output.ContextDiagnosticsEvent{
				Kind:     "session_health",
				Severity: "warning",
				Notes:    []string{fmt.Sprintf("close stream error log: %s", err)},
			}))
		}
	}
	if rt.historyWriter != nil {
		if err := rt.historyWriter.Close(); err != nil {
			rt.events.Emit(output.NewContextDiagnosticsEvent(output.ContextDiagnosticsEvent{
				Kind:     "session_health",
				Severity: "warning",
				Notes:    []string{fmt.Sprintf("failed to close history writer: %v", err)},
			}))
		}
	}
	if rt.closeFn != nil {
		if err := rt.closeFn(); err != nil {
			rt.events.Emit(output.NewContextDiagnosticsEvent(output.ContextDiagnosticsEvent{
				Kind:     "session_health",
				Severity: "warning",
				Notes:    []string{fmt.Sprintf("failed to close runtime: %v", err)},
			}))
		}
	}
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

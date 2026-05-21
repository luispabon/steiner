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
	"github.com/luispabon/steiner/internal/session"
	"github.com/luispabon/steiner/internal/tool"
)

type cliFlags struct {
	configPath      string
	model           string
	verbose         bool
	exec            bool
	logFile         string
	maxTurns        int
	enableStreaming bool
	contextMode     string
	caveman         bool
	resume          string
}

type cliRuntime struct {
	cfg               config.Config
	provider          provider.Provider
	providerFactory   func(provider.ResolvedModel) (provider.Provider, error)
	httpClient        *http.Client
	registry          *tool.Registry
	toolNames         []string
	skillNames        []string
	skillSources      map[string]string // skill name -> "project"/"user"/"global"
	skillDescriptions map[string]string // skill name -> short summary
	skillBundledFS    fs.FS             // embedded bundled skill documents
	workDir           string
	homeDir           string
	stdin             io.Reader
	human             *output.Stream
	status            *output.Stream
	events            output.EventSink
	sharedInput       *bufio.Reader
	approvalIn        *bufio.Reader
	closeFn           func() error
	historyWriter     *history.Writer
	sessionStore      *session.Store
	delegationLogger  *delegation.TraceLogger
}

var buildRuntime = defaultBuildRuntime

func defaultBuildRuntime(ctx context.Context, cmd *cobra.Command, flags *cliFlags) (cliRuntime, error) {
	cfg, err := loadRuntimeConfig(flags)
	if err != nil {
		return cliRuntime{}, err
	}
	httpClient := runtimeHTTPClient()
	providerFactory, err := buildRuntimeProviderFactory(cfg, httpClient)
	if err != nil {
		return cliRuntime{}, err
	}
	events, closeFn, err := buildRuntimeEventSink(cfg, cmd, flags)
	if err != nil {
		return cliRuntime{}, err
	}
	delegationLogger, err := buildDelegationLogger(cfg, flags)
	if err != nil {
		return cliRuntime{}, err
	}
	workDir, registry, err := buildRuntimeRegistry(cfg)
	if err != nil {
		return cliRuntime{}, err
	}
	homeDir, skillBundledFS, skillNames, skillSources, skillDescriptions, err := discoverRuntimeSkills(ctx)
	if err != nil {
		return cliRuntime{}, err
	}
	historyWriter, sessionStore, err := buildRuntimeSessionStores(homeDir)
	if err != nil {
		return cliRuntime{}, err
	}
	sharedInput, approvalInput, approvalClose := buildRuntimeInputs(cmd.InOrStdin())
	closeFn = joinClosers(closeFn, approvalClose)

	return cliRuntime{
		cfg:               cfg,
		providerFactory:   providerFactory,
		httpClient:        httpClient,
		registry:          registry,
		toolNames:         registry.Names(),
		skillNames:        skillNames,
		skillSources:      skillSources,
		skillDescriptions: skillDescriptions,
		skillBundledFS:    skillBundledFS,
		workDir:           workDir,
		homeDir:           homeDir,
		stdin:             cmd.InOrStdin(),
		human:             output.NewStream(cmd.OutOrStdout()),
		status:            output.NewStream(cmd.ErrOrStderr()),
		events:            events,
		sharedInput:       sharedInput,
		approvalIn:        approvalInput,
		closeFn:           closeFn,
		historyWriter:     historyWriter,
		sessionStore:      sessionStore,
		delegationLogger:  delegationLogger,
	}, nil
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

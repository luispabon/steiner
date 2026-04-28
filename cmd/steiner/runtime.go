package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/history"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/skill"
	"github.com/luispabon/steiner/internal/tool"
	"github.com/spf13/cobra"
)

type cliFlags struct {
	configPath string
	model      string
	verbose    bool
	exec       bool
	logFile    string
	maxTurns   int
}

type cliRuntime struct {
	cfg             config.Config
	provider        provider.Provider
	providerFactory func(config.ModelConfig) (provider.Provider, error)
	registry        *tool.Registry
	toolNames       []string
	skillNames      []string
	workDir         string
	homeDir         string
	stdin           io.Reader
	human           *output.Stream
	status          *output.Stream
	events          output.EventSink
	sharedInput     *bufio.Reader
	approvalIn      *bufio.Reader
	closeFn         func() error
	historyWriter   *history.Writer
}

var buildRuntime = defaultBuildRuntime

func defaultBuildRuntime(ctx context.Context, cmd *cobra.Command, flags *cliFlags) (cliRuntime, error) {
	_ = ctx
	cfg, err := config.Load(config.LoadOptions{
		CLI: config.CLIOverrides{
			ConfigPath: flags.configPath,
			Model:      flags.model,
			Verbose:    flags.verbose,
		},
	})
	if err != nil {
		return cliRuntime{}, err
	}

	scheduler, err := newScheduler(cfg.Scheduler.Parallelism)
	if err != nil {
		return cliRuntime{}, err
	}

	if _, err := selectedModelConfig(cfg); err != nil {
		return cliRuntime{}, err
	}
	httpClient := &http.Client{
		Timeout: 0, // no deadline on body reads — streams can run indefinitely
		Transport: &http.Transport{
			MaxIdleConns:          1,
			IdleConnTimeout:       90 * time.Second,
			MaxConnsPerHost:       1,
			ResponseHeaderTimeout: 30 * time.Second,
		},
	}
	providerFactory := func(modelCfg config.ModelConfig) (provider.Provider, error) {
		return newOpenAICompat(provider.OpenAICompatConfig{
			BaseURL:    modelCfg.BaseURL,
			APIKey:     modelCfg.APIKey,
			Model:      modelCfg.Model,
			Scheduler:  scheduler,
			HTTPClient: httpClient,
		})
	}

	events := output.EventSink(output.NoopSink{})
	if flags.exec {
		events = output.EventSink(output.NewStream(cmd.OutOrStdout()))
	}
	var closeFn func() error
	logFile := flags.logFile
	if logFile == "" && cfg.Logging.Enabled {
		logFile = cfg.Logging.File
	}
	if strings.TrimSpace(logFile) != "" {
		fileSink, err := output.NewFileLogSink(logFile, cfg.Logging.ThinkingChunk)
		if err != nil {
			return cliRuntime{}, err
		}
		events = output.NewMultiSink(events, fileSink)
		closeFn = fileSink.Close
	}
	currentDir, err := os.Getwd()
	if err != nil {
		return cliRuntime{}, err
	}

	registry, err := runtimeRegistry(cfg, currentDir)
	if err != nil {
		return cliRuntime{}, err
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		homeDir = ""
	}

	skillsRoot := prompt.DefaultSkillsRoot(homeDir)
	loadedSkills, err := skill.Loader{RootDir: skillsRoot}.Discover(ctx)
	if err != nil {
		return cliRuntime{}, err
	}
	skillNames := make([]string, 0, len(loadedSkills))
	for _, loaded := range loadedSkills {
		skillNames = append(skillNames, loaded.Name)
	}

	historyPath := filepath.Join(homeDir, ".config", "steiner", "history.log")
	historyWriter, err := history.NewWriter(historyPath)
	if err != nil {
		return cliRuntime{}, err
	}

	sharedInput := bufio.NewReader(cmd.InOrStdin())
	approvalInput, approvalClose := openApprovalInput(cmd.InOrStdin())
	closeFn = joinClosers(closeFn, approvalClose)

	return cliRuntime{
		cfg:             cfg,
		providerFactory: providerFactory,
		registry:        registry,
		toolNames:       registry.Names(),
		skillNames:      skillNames,
		workDir:         currentDir,
		homeDir:         homeDir,
		stdin:           cmd.InOrStdin(),
		human:           output.NewStream(cmd.OutOrStdout()),
		status:          output.NewStream(cmd.ErrOrStderr()),
		events:          events,
		sharedInput:     sharedInput,
		approvalIn:      approvalInput,
		closeFn:         closeFn,
		historyWriter:   historyWriter,
	}, nil
}

func closeRuntime(rt *cliRuntime) {
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

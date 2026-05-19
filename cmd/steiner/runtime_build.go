package main

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/delegation"
	"github.com/luispabon/steiner/internal/history"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/session"
	"github.com/luispabon/steiner/internal/skill"
	"github.com/luispabon/steiner/internal/tool"
)

func loadRuntimeConfig(flags *cliFlags) (config.Config, error) {
	return config.Load(config.LoadOptions{
		CLI: config.CLIOverrides{
			ConfigPath:  flags.configPath,
			Model:       flags.model,
			Verbose:     flags.verbose,
			ContextMode: config.ContextMode(flags.contextMode),
		},
	})
}

func buildRuntimeProviderFactory(cfg config.Config, httpClient *http.Client) (func(provider.ResolvedModel) (provider.Provider, error), error) {
	scheduler, err := newScheduler(cfg.Scheduler.Parallelism)
	if err != nil {
		return nil, err
	}
	return func(rm provider.ResolvedModel) (provider.Provider, error) {
		return newOpenAICompat(provider.OpenAICompatConfig{
			BaseURL: rm.ProviderConfig.BaseURL,
			APIKey:  rm.ProviderConfig.APIKey,
			Headers: rm.ProviderConfig.Headers,
			Model:   rm.BackendModelID,
			Timeout: time.Duration(rm.ProviderConfig.Timeout.Duration()),
			Retry: provider.RetryConfig{
				Enabled:        rm.Retry.Enabled,
				MaxAttempts:    rm.Retry.MaxAttempts,
				InitialBackoff: time.Duration(rm.Retry.InitialBackoff.Duration()),
				MaxBackoff:     time.Duration(rm.Retry.MaxBackoff.Duration()),
				RetryAfterMax:  time.Duration(rm.Retry.RetryAfterMax.Duration()),
			},
			Scheduler:  scheduler,
			HTTPClient: httpClient,
		})
	}, nil
}

func runtimeHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 0, // no deadline on body reads — streams can run indefinitely
		Transport: &http.Transport{
			MaxIdleConns:          1,
			IdleConnTimeout:       90 * time.Second,
			MaxConnsPerHost:       1,
			ResponseHeaderTimeout: 30 * time.Second,
		},
	}
}

func buildRuntimeEventSink(cfg config.Config, cmd *cobra.Command, flags *cliFlags) (output.EventSink, func() error, error) {
	events := output.EventSink(output.NoopSink{})
	if flags.exec {
		events = output.EventSink(output.NewStream(cmd.OutOrStdout()))
	}
	logFile := runtimeLogFile(cfg, flags)
	if strings.TrimSpace(logFile) == "" {
		return events, nil, nil
	}
	fileSink, err := output.NewFileLogSink(logFile, cfg.Logging.ThinkingChunk)
	if err != nil {
		return nil, nil, err
	}
	return output.NewMultiSink(events, fileSink), fileSink.Close, nil
}

func runtimeLogFile(cfg config.Config, flags *cliFlags) string {
	if flags.logFile != "" {
		return flags.logFile
	}
	if cfg.Logging.Enabled {
		return cfg.Logging.File
	}
	return ""
}

func buildRuntimeRegistry(cfg config.Config) (string, *tool.Registry, error) {
	workDir, err := os.Getwd()
	if err != nil {
		return "", nil, err
	}
	registry, err := runtimeRegistry(cfg, workDir)
	if err != nil {
		return "", nil, err
	}
	return workDir, registry, nil
}

func discoverRuntimeSkills(ctx context.Context) (string, []string, map[string]string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		homeDir = ""
	}
	workDir, err := os.Getwd()
	if err != nil {
		workDir = ""
	}
	roots := prompt.SkillRoots(homeDir, workDir)
	loadedSkills, err := skill.Loader{RootDirs: roots}.Discover(ctx)
	if err != nil {
		return "", nil, nil, err
	}
	skillNames := make([]string, 0, len(loadedSkills))
	skillSources := make(map[string]string, len(loadedSkills))
	for _, loaded := range loadedSkills {
		skillNames = append(skillNames, loaded.Name)
		skillSources[loaded.Name] = loaded.Source
	}
	return homeDir, skillNames, skillSources, nil
}

func buildRuntimeSessionStores(homeDir string) (*history.Writer, *session.Store, error) {
	historyWriter, err := history.NewWriter(filepath.Join(homeDir, ".config", "steiner", "history.log"))
	if err != nil {
		return nil, nil, err
	}
	sessionStore, err := session.NewStore(filepath.Join(homeDir, ".config", "steiner", "sessions"))
	if err != nil {
		return nil, nil, err
	}
	return historyWriter, sessionStore, nil
}

func buildDelegationLogger(cfg config.Config, flags *cliFlags) (*delegation.TraceLogger, error) {
	logPath := delegation.LogPath(runtimeLogFile(cfg, flags))
	return delegation.NewTraceLogger(logPath)
}

func buildRuntimeInputs(stdin io.Reader) (*bufio.Reader, *bufio.Reader, func() error) {
	sharedInput := bufio.NewReader(stdin)
	approvalInput, approvalClose := openApprovalInput(stdin)
	return sharedInput, approvalInput, approvalClose
}

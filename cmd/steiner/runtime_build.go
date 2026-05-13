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

func buildRuntimeProviderFactory(cfg config.Config) (func(config.ModelConfig) (provider.Provider, error), error) {
	scheduler, err := newScheduler(cfg.Scheduler.Parallelism)
	if err != nil {
		return nil, err
	}
	httpClient := runtimeHTTPClient()
	return func(modelCfg config.ModelConfig) (provider.Provider, error) {
		return newOpenAICompat(provider.OpenAICompatConfig{
			BaseURL: modelCfg.BaseURL,
			APIKey:  modelCfg.APIKey,
			Model:   modelCfg.Model,
			Retry: provider.RetryConfig{
				Enabled:        modelCfg.Retry.Enabled,
				MaxAttempts:    modelCfg.Retry.MaxAttempts,
				InitialBackoff: time.Duration(modelCfg.Retry.InitialBackoff.Duration()),
				MaxBackoff:     time.Duration(modelCfg.Retry.MaxBackoff.Duration()),
				RetryAfterMax:  time.Duration(modelCfg.Retry.RetryAfterMax.Duration()),
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

func discoverRuntimeSkills(ctx context.Context) (string, []string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		homeDir = ""
	}
	loadedSkills, err := skill.Loader{RootDir: prompt.DefaultSkillsRoot(homeDir)}.Discover(ctx)
	if err != nil {
		return "", nil, err
	}
	skillNames := make([]string, 0, len(loadedSkills))
	for _, loaded := range loadedSkills {
		skillNames = append(skillNames, loaded.Name)
	}
	return homeDir, skillNames, nil
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

func buildRuntimeInputs(stdin io.Reader) (*bufio.Reader, *bufio.Reader, func() error) {
	sharedInput := bufio.NewReader(stdin)
	approvalInput, approvalClose := openApprovalInput(stdin)
	return sharedInput, approvalInput, approvalClose
}

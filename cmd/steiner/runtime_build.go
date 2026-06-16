package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"io/fs"
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
	"github.com/luispabon/steiner/internal/sandbox"
	"github.com/luispabon/steiner/internal/session"
	"github.com/luispabon/steiner/internal/skill"
	"github.com/luispabon/steiner/internal/tool"
	"github.com/luispabon/steiner/skills"
)

// ensureSteinerProjectDir creates the .steiner/ directory and a .gitignore inside it.
// It is idempotent and safe to call multiple times.
func ensureSteinerProjectDir(workDir string) error {
	steinerDir := filepath.Join(workDir, ".steiner")
	if err := os.MkdirAll(steinerDir, 0o755); err != nil {
		return fmt.Errorf("create .steiner directory: %w", err)
	}
	gitignorePath := filepath.Join(steinerDir, ".gitignore")
	if _, err := os.Stat(gitignorePath); os.IsNotExist(err) {
		if err := os.WriteFile(gitignorePath, []byte("*\n"), 0o644); err != nil {
			return fmt.Errorf("create .steiner/.gitignore: %w", err)
		}
	}
	return nil
}

func loadRuntimeConfig(cmd *cobra.Command, flags *cliFlags) (config.Config, error) {
	caveHumanOverride := (*bool)(nil)
	if cmd.Flags().Changed("cave-human") {
		caveHumanOverride = &flags.caveHuman
	}
	cfg, err := config.Load(config.LoadOptions{
		CLI: config.CLIOverrides{
			ConfigPath: flags.configPath,
			Model:      flags.model,
			Verbose:    flags.verbose,
			CaveHuman:  caveHumanOverride,
		},
	})
	if err != nil {
		return config.Config{}, err
	}
	if flags.unsafe {
		cfg.Sandbox.Enabled = false
	}
	return cfg, nil
}

func buildRuntimeProviderFactory(cfg config.Config, httpClient *http.Client, streamErrorLog *provider.StreamErrorLogger) (func(provider.ResolvedModel) (provider.Provider, error), error) {
	scheduler, err := newScheduler(cfg.Scheduler.Parallelism)
	if err != nil {
		return nil, err
	}
	return func(rm provider.ResolvedModel) (provider.Provider, error) {
		providerType := rm.EffectiveProviderType
		if providerType == "" {
			providerType = rm.ProviderConfig.Type
		}
		if providerType == "" {
			return nil, fmt.Errorf("resolved provider type is empty for model %q", rm.Alias)
		}

		switch providerType {
		case config.ProviderTypeOpenAICompat, config.ProviderTypeOllama, config.ProviderTypeLMStudio,
			config.ProviderTypeOpenRouter, config.ProviderTypeOpenAI, config.ProviderTypeLiteLLM:
			return newOpenAICompat(runtimeProviderConfig(rm, rm.ProviderConfig.Type, scheduler, httpClient, streamErrorLog))
		case config.ProviderTypeAnthropic:
			return newAnthropic(runtimeProviderConfig(rm, providerType, scheduler, httpClient, streamErrorLog))
		default:
			return nil, fmt.Errorf("provider type %q is not implemented by the runtime provider factory", providerType)
		}
	}, nil
}

func runtimeProviderConfig(rm provider.ResolvedModel, providerType config.ProviderType, scheduler *provider.Scheduler, httpClient *http.Client, streamErrorLog *provider.StreamErrorLogger) provider.OpenAICompatConfig {
	return provider.OpenAICompatConfig{
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
		ProviderType:   string(providerType),
		Scheduler:      scheduler,
		HTTPClient:     httpClient,
		StreamErrorLog: streamErrorLog,
	}
}

func runtimeHTTPClient() *http.Client {
	// No client-level timeout — without a provider timeout, streams can run
	// indefinitely. Transport.ResponseHeaderTimeout acts as a 30s safety net
	// for the header phase so a stuck server doesn't hang forever on the
	// initial read. Providers that set config.timeout get Client.Timeout
	// applied in NewOpenAICompat, which bounds the entire request including
	// the streaming body; that path does not touch the Transport to avoid
	// breaking the http2 wiring.
	return &http.Client{
		Timeout: 0,
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

func runtimeCompactionLogFile(cfg config.Config, flags *cliFlags) string {
	if flags.compactionLogFile != "" {
		return flags.compactionLogFile
	}
	if cfg.Logging.CompactionLogFile != "" {
		return cfg.Logging.CompactionLogFile
	}
	return ""
}

func buildRuntimeRegistry(cfg config.Config, sb *sandbox.Sandbox) (string, *tool.Registry, error) {
	workDir, err := os.Getwd()
	if err != nil {
		return "", nil, err
	}
	registry, err := runtimeRegistryWithSink(cfg, workDir, nil, false, nil, sb)
	if err != nil {
		return "", nil, err
	}
	return workDir, registry, nil
}

// buildRuntimeRegistryWithSandbox rebuilds the registry for a known workDir with a sandbox.
func buildRuntimeRegistryWithSandbox(cfg config.Config, workDir string, sb *sandbox.Sandbox) (*tool.Registry, error) {
	return runtimeRegistryWithSink(cfg, workDir, nil, false, nil, sb)
}

func discoverRuntimeSkills(ctx context.Context) (string, fs.FS, []string, map[string]string, map[string]string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		homeDir = ""
	}
	workDir, err := os.Getwd()
	if err != nil {
		workDir = ""
	}
	roots := prompt.SkillRoots(homeDir, workDir)
	loadedSkills, err := skill.Loader{RootDirs: roots, BundledFS: skills.FS}.Discover(ctx)
	if err != nil {
		return "", nil, nil, nil, nil, err
	}
	skillNames := make([]string, 0, len(loadedSkills))
	skillSources := make(map[string]string, len(loadedSkills))
	skillDescriptions := make(map[string]string, len(loadedSkills))
	for _, loaded := range loadedSkills {
		skillNames = append(skillNames, loaded.Name)
		skillSources[loaded.Name] = loaded.Source
		skillDescriptions[loaded.Name] = loaded.Summary
	}
	return homeDir, skills.FS, skillNames, skillSources, skillDescriptions, nil
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

func buildStreamErrorLogger(cfg config.Config, flags *cliFlags) (*provider.StreamErrorLogger, error) {
	path := provider.StreamErrorLogPath(runtimeLogFile(cfg, flags))
	l, err := provider.NewStreamErrorLogger(path)
	if err != nil {
		return nil, fmt.Errorf("stream error logger: %w", err)
	}
	return l, nil
}

func buildRuntimeInputs(stdin io.Reader) (*bufio.Reader, *bufio.Reader, func() error) {
	sharedInput := bufio.NewReader(stdin)
	approvalInput, approvalClose := openApprovalInput(stdin)
	return sharedInput, approvalInput, approvalClose
}

// buildRuntimeSandbox creates a Sandbox when sandboxing is enabled. Returns nil
// when cfg.Sandbox.Enabled is false (e.g. --unsafe flag was set). Returns an
// error when bwrap is required but not found on PATH.
func buildRuntimeSandbox(cfg config.Config, workDir, userHome string) (*sandbox.Sandbox, error) {
	if !cfg.Sandbox.Enabled {
		return nil, nil
	}
	if err := sandbox.PrereqCheck(); err != nil {
		return nil, err
	}
	s := sandbox.New(cfg.Sandbox, cfg.Permissions, cfg.HostMounts, workDir, userHome)
	if err := s.EnsureHome(); err != nil {
		return nil, fmt.Errorf("sandbox setup: %w", err)
	}
	return s, nil
}

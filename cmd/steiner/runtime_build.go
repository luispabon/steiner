package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/oauth2"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/delegation"
	"github.com/luispabon/steiner/internal/history"
	"github.com/luispabon/steiner/internal/mcp"
	"github.com/luispabon/steiner/internal/oauth"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/sandbox"
	"github.com/luispabon/steiner/internal/session"
	"github.com/luispabon/steiner/internal/skill"
	"github.com/luispabon/steiner/internal/tool"
	"github.com/luispabon/steiner/internal/usagestats"
	"github.com/luispabon/steiner/skills"
)

func loadRuntimeConfig(_ *cobra.Command, flags *cliFlags, modelAlias string) (config.Config, error) {
	overrides := config.CLIOverrides{
		ConfigPath: flags.configPath,
		Model:      modelAlias,
		Verbose:    flags.verbose,
		Unsafe:     flags.unsafe,
	}
	if modelAlias == "" {
		overrides.Model = flags.model
	}
	cfg, err := config.Load(config.LoadOptions{
		CLI: overrides,
	})
	if err != nil {
		return config.Config{}, err
	}
	return cfg, nil
}

func buildRuntimeWithRoots(ctx context.Context, cmd *cobra.Command, flags *cliFlags, projectRoot, workDir, modelAlias string) (cliRuntime, error) {
	if err := session.EnsureSteinerProjectDir(projectRoot); err != nil {
		return cliRuntime{}, err
	}
	cfg, err := loadRuntimeConfig(cmd, flags, modelAlias)
	if err != nil {
		return cliRuntime{}, err
	}
	httpClient := runtimeHTTPClient()
	events, closeFn, err := buildRuntimeEventSink(cfg, cmd, flags)
	if err != nil {
		return cliRuntime{}, err
	}
	delegationLogger, err := buildDelegationLogger(cfg, flags)
	if err != nil {
		return cliRuntime{}, err
	}
	streamErrorLog, err := buildStreamErrorLogger(cfg, flags)
	if err != nil {
		return cliRuntime{}, fmt.Errorf("build stream error logger: %w", err)
	}
	providerFactory, err := buildRuntimeProviderFactory(cfg, httpClient, streamErrorLog)
	if err != nil {
		return cliRuntime{}, err
	}
	compactionLogFile := runtimeCompactionLogFile(cfg, flags)
	workDir, registry := buildRuntimeRegistry(cfg, nil, workDir)
	homeDir, skillBundledFS, skillNames, skillSources, skillDescriptions, err := discoverRuntimeSkills(ctx, projectRoot)
	if err != nil {
		return cliRuntime{}, err
	}
	sb, err := buildRuntimeSandbox(&cfg, projectRoot, workDir, homeDir)
	if err != nil {
		return cliRuntime{}, err
	}

	emitSandboxWarning(cfg, events)

	// Connect MCP servers after the sandbox exists (so server commands can be
	// wrapped) and before the registry is rebuilt (so MCP tools register).
	// Failures are reported and skipped; Connect never returns an error for a
	// server failure.
	var mcpMgr *mcp.Manager
	if cfg.MCP.Enabled {
		var wrap func(*exec.Cmd) *exec.Cmd
		if sb != nil {
			wrap = func(c *exec.Cmd) *exec.Cmd { return sb.WrapCommandMode(c, true) }
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
		mcpMgr = mcp.Connect(ctx, cfg.MCP, wrap, nil, diagnose("warning"), diagnose("info"), os.Stderr)
	}

	// Rebuild registry with sandbox and MCP tools now that workDir and homeDir are known.
	if sb != nil || mcpMgr != nil {
		registry = buildRuntimeRegistryWithSandbox(cfg, workDir, sb, mcpMgr)
	}
	historyWriter, sessionStore, err := buildRuntimeSessionStores(homeDir)
	if err != nil {
		return cliRuntime{}, err
	}
	sharedInput, approvalInput, approvalClose := buildRuntimeInputs(cmd.InOrStdin())
	closeFn = joinClosers(closeFn, approvalClose)

	return cliRuntime{
		cfg:                    cfg,
		providerFactory:        providerFactory,
		httpClient:             httpClient,
		registry:               registry,
		toolNames:              registry.Names(),
		skillNames:             skillNames,
		skillSources:           skillSources,
		skillDescriptions:      skillDescriptions,
		skillBundledFS:         skillBundledFS,
		projectRoot:            projectRoot,
		workDir:                workDir,
		homeDir:                homeDir,
		sandbox:                sb,
		mcpManager:             mcpMgr,
		stdin:                  cmd.InOrStdin(),
		human:                  output.NewStream(cmd.OutOrStdout()),
		status:                 output.NewStream(cmd.ErrOrStderr()),
		events:                 events,
		sharedInput:            sharedInput,
		approvalIn:             approvalInput,
		closeFn:                closeFn,
		historyWriter:          historyWriter,
		sessionStore:           sessionStore,
		delegationLogger:       delegationLogger,
		streamErrorLog:         streamErrorLog,
		delegationSessionStore: delegation.NewSessionStore(),
		compactionLogFile:      compactionLogFile,
		usageRecorder:          usagestats.New(nil),
		imageStore:             agent.NewImageStore(filepath.Join(workDir, ".steiner", "tmp", "images")),
		visionCapabilities:     agent.NewVisionCapabilities(cfg.Models.SubAgents["vision"] != ""),
	}, nil
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
		case config.ProviderTypeCodex:
			return newCodexProvider(rm, providerType, scheduler, httpClient, streamErrorLog)
		default:
			return nil, fmt.Errorf("provider type %q is not implemented by the runtime provider factory", providerType)
		}
	}, nil
}

func newCodexProvider(rm provider.ResolvedModel, providerType config.ProviderType, scheduler *provider.Scheduler, httpClient *http.Client, streamErrorLog *provider.StreamErrorLogger) (provider.Provider, error) {
	path, err := oauth.DefaultTokenPath()
	if err != nil {
		return nil, fmt.Errorf("resolve token path: %w", err)
	}
	store := oauth.NewTokenStore(path)
	token, err := store.Load()
	if errors.Is(err, oauth.ErrNoToken) {
		return nil, fmt.Errorf("codex provider requires authentication — run 'steiner login codex' first")
	} else if err != nil {
		return nil, fmt.Errorf("load codex token: %w", err)
	}
	token, err = oauth.NewRefreshableTokenSource(store, &oauth2.Config{
		ClientID: oauth.CodexClientID,
		Endpoint: oauth2.Endpoint{TokenURL: oauth.CodexTokenURL},
	}, token).Token()
	if err != nil {
		return nil, fmt.Errorf("refresh codex token: %w", err)
	}
	cfg := runtimeProviderConfig(rm, providerType, scheduler, httpClient, streamErrorLog)
	if apiKey := oauth.TokenOpenAIAPIKey(token); apiKey != "" {
		cfg.APIKey = apiKey
	} else {
		accountID := oauth.TokenChatGPTAccountID(token)
		if accountID == "" {
			return nil, fmt.Errorf("codex token missing ChatGPT account metadata — run 'steiner login codex' again")
		}
		cfg.BaseURL = "https://chatgpt.com/backend-api/codex"
		cfg.APIKey = token.AccessToken
		cfg.Headers = cloneStringMap(cfg.Headers)
		cfg.Headers["ChatGPT-Account-ID"] = accountID
	}
	return newCodexResponses(cfg)
}

func runtimeProviderConfig(rm provider.ResolvedModel, providerType config.ProviderType, scheduler *provider.Scheduler, httpClient *http.Client, streamErrorLog *provider.StreamErrorLogger) provider.ClientConfig {
	return provider.ClientConfig{
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
		ProviderType:       string(providerType),
		Scheduler:          scheduler,
		HTTPClient:         httpClient,
		StreamErrorLog:     streamErrorLog,
		MinRequestInterval: time.Duration(rm.ProviderConfig.Codex.MinRequestInterval.Duration()),
	}
}

func cloneStringMap(src map[string]string) map[string]string {
	dst := make(map[string]string, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func runtimeHTTPClient() *http.Client {
	// No client-level timeout — without a provider timeout, streams can run
	// indefinitely. Transport.ResponseHeaderTimeout acts as a 30s safety net
	// for the header phase so a stuck server doesn't hang forever on the
	// initial read. Providers that set config.timeout get Client.Timeout
	// applied in NewOpenAICompat, which clones the transport and clears
	// ResponseHeaderTimeout so the user-supplied timeout bounds the whole
	// request (headers + body + streaming response).
	//
	// ForceAttemptHTTP2 must be true to prevent the cloned transport from
	// losing HTTP/2 support. When Clone() calls onceSetNextProtoDefaults on
	// the original, a TLSClientConfig is created (with "h2" in NextProtos).
	// Clone() copies TLSClientConfig but not TLSNextProto (because it was
	// nil before the defaults ran). The clone then sees a non-nil
	// TLSClientConfig with ForceAttemptHTTP2=false and conservatively
	// disables HTTP/2, while TLS still advertises "h2". The result is
	// "net/http: HTTP/1.x transport connection broken: malformed HTTP
	// response" when the upstream negotiates h2.
	return &http.Client{
		Timeout: 0,
		Transport: &http.Transport{
			MaxIdleConns:          1,
			IdleConnTimeout:       90 * time.Second,
			MaxConnsPerHost:       1,
			ResponseHeaderTimeout: 30 * time.Second,
			ForceAttemptHTTP2:     true,
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

func buildRuntimeRegistry(cfg config.Config, sb *sandbox.Sandbox, workDir string) (string, *tool.Registry) {
	registry := runtimeRegistryWithSinkAndMode(cfg, workDir, nil, false, nil, sb, nil, nil)
	return workDir, registry
}

// buildRuntimeRegistryWithSandbox rebuilds the registry for a known workDir with a sandbox and MCP tools.
func buildRuntimeRegistryWithSandbox(cfg config.Config, workDir string, sb *sandbox.Sandbox, mgr *mcp.Manager) *tool.Registry {
	registry := runtimeRegistryWithSinkAndMode(cfg, workDir, nil, false, nil, sb, nil, mgr)
	return registry
}

func discoverRuntimeSkills(ctx context.Context, projectRoot string) (string, fs.FS, []string, map[string]string, map[string]string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		homeDir = ""
	}
	roots := prompt.SkillRoots(homeDir, projectRoot)
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

// cleanupSandboxTmpOrphans removes stale per-session sandbox tmp directories
// older than maxAge from parentDir. Best-effort: never fails the caller.
// Emits start/done status to stderr when there is work to do, since removing
// large trees (e.g. Go module caches) can take a while.
func cleanupSandboxTmpOrphans(parentDir string, maxAge time.Duration) {
	cutoff := time.Now().Add(-maxAge)
	var oldCount int
	if entries, err := os.ReadDir(parentDir); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			if fi, err := e.Info(); err == nil && fi.ModTime().Before(cutoff) {
				oldCount++
			}
		}
	}
	if oldCount == 0 {
		return
	}
	fmt.Fprintf(os.Stderr, "Cleaning up %d old sandbox tmp director%s...\n", oldCount, pluralSuffix(oldCount))
	sandbox.CleanupOrphans(parentDir, maxAge)
	fmt.Fprintf(os.Stderr, "Done.\n")
}

func pluralSuffix(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}

// createSandboxTmpDir generates a random 8-byte hex ID and creates a fresh
// session-scoped directory at parentDir/<id>. If a directory with the
// generated ID already exists (collision), it is removed and recreated.
func createSandboxTmpDir(parentDir string) (string, error) {
	var idBuf [8]byte
	if _, err := rand.Read(idBuf[:]); err != nil {
		return "", fmt.Errorf("generate sandbox tmp id: %w", err)
	}
	id := fmt.Sprintf("%x", idBuf[:])
	tmpDir := filepath.Join(parentDir, id)
	if _, err := os.Stat(tmpDir); err == nil {
		if err := os.RemoveAll(tmpDir); err != nil {
			return "", fmt.Errorf("remove stale sandbox tmp dir: %w", err)
		}
	}
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return "", fmt.Errorf("create sandbox tmp dir: %w", err)
	}
	return tmpDir, nil
}

// buildRuntimeSandbox creates a Sandbox when sandboxing is enabled. Returns nil
// when cfg.Sandbox.Enabled is false (e.g. --unsafe flag was set). Returns nil
// when bwrap is unavailable (unsupported platform or missing binary) and sets
// cfg.Sandbox.Status to "bypassed" or "unavailable" respectively.
func buildRuntimeSandbox(cfg *config.Config, projectRoot, workDir, userHome string) (*sandbox.Sandbox, error) {
	if !cfg.Sandbox.Enabled {
		cfg.Sandbox.Status = "bypassed"
		return nil, nil
	}
	if err := sandbox.PrereqCheck(); err != nil {
		cfg.Sandbox.Status = "unavailable"
		return nil, nil
	}

	// Session-scoped tmp directory.
	parentDir := filepath.Join(projectRoot, ".steiner", "tmp", "sandbox-tmp")

	cleanupSandboxTmpOrphans(parentDir, 48*time.Hour)
	tmpDir, err := createSandboxTmpDir(parentDir)
	if err != nil {
		return nil, err
	}

	s := sandbox.New(cfg.Sandbox, cfg.Permissions, cfg.HostMounts, projectRoot, workDir, userHome, tmpDir)
	if err := s.EnsureHome(); err != nil {
		return nil, fmt.Errorf("sandbox setup: %w", err)
	}
	cfg.Sandbox.Status = "active"
	return s, nil
}

// emitSandboxWarning emits a SandboxStatusEvent when sandbox is not active and
// WarningOnUnsupportedPlatform is enabled, and independently when the sandbox
// is active but env_passthrough_all disables the credential barrier.
func emitSandboxWarning(cfg config.Config, events output.EventSink) {
	if cfg.Sandbox.Enabled && cfg.Sandbox.EnvPassthroughAll {
		events.Emit(output.NewSandboxStatusEvent(cfg.Sandbox.Status, "sandbox env_passthrough_all is enabled: the credential barrier is disabled and the full host environment (including credentials) is passed to sandboxed processes unfiltered."))
	}
	if cfg.Sandbox.Status == "active" || !cfg.Sandbox.WarningOnUnsupportedPlatform {
		return
	}
	var msg string
	switch cfg.Sandbox.Status {
	case "unavailable":
		msg = fmt.Sprintf("sandbox unavailable: bubblewrap is not supported on %s. Bash and subprocess tools run unsandboxed.", runtime.GOOS)
	case "bypassed":
		msg = "sandbox bypassed: running with --unsafe or sandbox.enabled=false. Bash and subprocess tools run unsandboxed."
	}
	if msg != "" {
		events.Emit(output.NewSandboxStatusEvent(cfg.Sandbox.Status, msg))
	}
}

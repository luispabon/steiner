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

	"github.com/luispabon/steiner/internal/advisor"
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
	"github.com/luispabon/steiner/internal/tui"
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
	return config.Load(config.LoadOptions{CLI: overrides})
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
	modelCatalog, modelCatalogEndpoints, modelPopularity := buildModelCatalogService(&cfg, httpClient)
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
	providerFactory := buildRuntimeProviderFactory(cfg, httpClient, streamErrorLog)
	compactionLogFile := runtimeCompactionLogFile(cfg, flags)
	workDir, registry := buildRuntimeRegistry(cfg, nil, workDir)
	homeDir, skillBundledFS, skillNames, skillSources, skillDescriptions, err := discoverRuntimeSkills(ctx, projectRoot)
	if err != nil {
		return cliRuntime{}, err
	}
	sb, status, err := buildRuntimeSandbox(&cfg, projectRoot, workDir, homeDir)
	if err != nil {
		return cliRuntime{}, err
	}

	emitSandboxWarning(cfg, status, events)
	emitProjectContextDeprecationWarning(cfg, events)

	// Connect MCP servers after the sandbox exists (so server commands can be
	// wrapped) and before the registry is rebuilt (so MCP tools register).
	// Failures are reported and skipped; Connect never returns an error for a
	// server failure. Non-interactive commands block until every server
	// resolves; interactive (asyncMCP) returns immediately so the TUI paints
	// while servers connect and the session runner waits before the first turn.
	var mcpMgr *mcp.Manager
	var mcpState *mcpStateProducer
	if cfg.MCP.Enabled {
		mcpServerLogPath := mcp.ServerLogPath(runtimeLogFile(cfg, flags))
		mcpServerLogWriter, err := buildMCPServerLogWriter(mcpServerLogPath)
		if err != nil {
			return cliRuntime{}, err
		}
		closeFn = joinClosers(closeFn, mcpServerLogWriter.Close)

		mcpStderr := selectMCPStderr(mcpServerLogPath, flags.asyncMCP, mcpServerLogWriter)
		mcpMgr, mcpState = connectRuntimeMCP(ctx, cfg, sb, flags.asyncMCP, events, mcpStderr)
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
		cfg:                     cfg,
		sandboxStatus:           status,
		configWarnings:          projectContextConfigWarnings(cfg),
		providerFactory:         providerFactory,
		httpClient:              httpClient,
		registry:                registry,
		toolNames:               registry.Names(),
		skillNames:              skillNames,
		skillSources:            skillSources,
		skillDescriptions:       skillDescriptions,
		skillBundledFS:          skillBundledFS,
		projectRoot:             projectRoot,
		workDir:                 workDir,
		homeDir:                 homeDir,
		sandbox:                 sb,
		mcpManager:              mcpMgr,
		mcpState:                mcpState,
		stdin:                   cmd.InOrStdin(),
		human:                   output.NewStream(cmd.OutOrStdout()),
		status:                  output.NewStream(cmd.ErrOrStderr()),
		events:                  events,
		sharedInput:             sharedInput,
		approvalIn:              approvalInput,
		closeFn:                 closeFn,
		historyWriter:           historyWriter,
		sessionStore:            sessionStore,
		delegationLogger:        delegationLogger,
		streamErrorLog:          streamErrorLog,
		delegationSessionStore:  delegation.NewSessionStore(),
		delegationCacheKeyStore: delegation.NewCacheKeyStore(),
		advisorState:            advisor.NewSharedState(),
		compactionLogFile:       compactionLogFile,
		usageRecorder:           usagestats.New(nil),
		imageStore:              agent.NewImageStore(filepath.Join(workDir, ".steiner", "tmp", "images")),
		visionCapabilities:      agent.NewVisionCapabilities(cfg.Models.SubAgents["vision"] != ""),
		modelCatalog:            modelCatalog,
		modelCatalogEndpoints:   modelCatalogEndpoints,
		modelPopularity:         modelPopularity,
		modelEntriesUpdates:     make(chan []tui.ModelEntry, max(1, len(modelCatalogEndpoints))),
		codexWSCache:            &codexWSCache{instances: make(map[string]provider.Provider)},
	}, nil
}

func buildRuntimeProviderFactory(_ config.Config, httpClient *http.Client, streamErrorLog *provider.StreamErrorLogger) func(provider.ResolvedModel) (provider.Provider, error) {
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
			return newOpenAICompat(runtimeProviderConfig(rm, rm.ProviderConfig.Type, httpClient, streamErrorLog))
		case config.ProviderTypeAnthropic:
			return newAnthropic(runtimeProviderConfig(rm, providerType, httpClient, streamErrorLog))
		case config.ProviderTypeCodex:
			return newCodexProvider(rm, providerType, httpClient, streamErrorLog)
		default:
			return nil, fmt.Errorf("provider type %q is not implemented by the runtime provider factory", providerType)
		}
	}
}

func newCodexProvider(rm provider.ResolvedModel, providerType config.ProviderType, httpClient *http.Client, streamErrorLog *provider.StreamErrorLogger) (provider.Provider, error) {
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
	cfg := runtimeProviderConfig(rm, providerType, httpClient, streamErrorLog)
	if apiKey := oauth.TokenOpenAIAPIKey(token); apiKey != "" {
		cfg.APIKey = apiKey
	} else {
		accountID := oauth.TokenChatGPTAccountID(token)
		if accountID == "" {
			return nil, fmt.Errorf("codex token missing ChatGPT account metadata — run 'steiner login codex' again")
		}
		cfg.BaseURL = codexChatGPTBackendURL
		cfg.APIKey = token.AccessToken
		cfg.Headers = cloneStringMap(cfg.Headers)
		cfg.Headers["ChatGPT-Account-ID"] = accountID
	}
	if !isCodexWSDispatch(rm) {
		return newCodexResponses(cfg)
	}
	return newCodexResponsesWS(cfg)
}

// isCodexWSDispatch reports whether rm resolves to a Codex WebSocket
// transport (explicit websocket only; anything else, including unset,
// dispatches to HTTP). This is the single place defining WS eligibility;
// buildRuntimeProviderFactory's dispatch and cliRunner.runtimeProvider's
// caching both consult it.
func isCodexWSDispatch(rm provider.ResolvedModel) bool {
	providerType := rm.EffectiveProviderType
	if providerType == "" {
		providerType = rm.ProviderConfig.Type
	}
	if providerType != config.ProviderTypeCodex {
		return false
	}
	return rm.ProviderConfig.Codex.Transport == config.CodexTransportWebSocket
}

func runtimeProviderConfig(rm provider.ResolvedModel, providerType config.ProviderType, httpClient *http.Client, streamErrorLog *provider.StreamErrorLogger) provider.ClientConfig {
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
	planMode := cfg.Modes.Default == config.ExecutionModePlan
	var producer *mcpStateProducer
	var onStateChange func()
	if asyncMCP {
		producer = &mcpStateProducer{}
		onStateChange = producer.stateChanged
	}
	mgr := mcp.Connect(ctx, cfg.MCP, cfg.Limits, wrap, planMode, diagnose("warning"), diagnose("info"), stderr, onStateChange)
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

func buildRuntimeRegistry(cfg config.Config, sb *sandbox.Sandbox, workDir string) (string, *tool.Registry) {
	registry := runtimeRegistryWithSinkAndMode(cfg, workDir, nil, false, nil, sb, nil)
	return workDir, registry
}

// buildRuntimeRegistryWithSandbox rebuilds the registry for a known workDir with a sandbox and MCP tools.
func buildRuntimeRegistryWithSandbox(cfg config.Config, workDir string, sb *sandbox.Sandbox, mgr *mcp.Manager) *tool.Registry {
	registry := runtimeRegistryWithSinkAndMode(cfg, workDir, nil, false, nil, sb, mgr)
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

func buildMCPServerLogWriter(path string) (io.WriteCloser, error) {
	w, err := mcp.NewServerLogWriter(path)
	if err != nil {
		return nil, fmt.Errorf("mcp server log writer: %w", err)
	}
	return w, nil
}

// selectMCPStderr picks the destination for MCP server subprocess stderr: the
// derived log file when logPath is non-empty, io.Discard in interactive mode
// otherwise (terminal corruption is non-negotiable), or os.Stderr in
// non-interactive mode where there is no live TUI to trample. logPath must be
// derived from the same inputs used to build logWriter; callers must not
// recompute it independently, or the two can silently diverge.
func selectMCPStderr(logPath string, asyncMCP bool, logWriter io.Writer) io.Writer {
	if logPath != "" {
		return logWriter
	}
	if asyncMCP {
		return io.Discard
	}
	return os.Stderr
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
// when bwrap is unavailable (unsupported platform or missing binary). The
// returned status is "bypassed", "unavailable", or "active" respectively.
func buildRuntimeSandbox(cfg *config.Config, projectRoot, workDir, userHome string) (*sandbox.Sandbox, string, error) {
	if !cfg.Sandbox.Enabled {
		return nil, "bypassed", nil
	}
	if err := sandbox.PrereqCheck(); err != nil {
		return nil, "unavailable", nil
	}

	// Session-scoped tmp directory.
	parentDir := filepath.Join(projectRoot, ".steiner", "tmp", "sandbox-tmp")

	cleanupSandboxTmpOrphans(parentDir, 48*time.Hour)
	tmpDir, err := createSandboxTmpDir(parentDir)
	if err != nil {
		return nil, "", err
	}

	s := sandbox.New(cfg.Sandbox, cfg.Permissions, projectRoot, workDir, userHome, tmpDir)
	if err := s.EnsureHome(); err != nil {
		return nil, "", fmt.Errorf("sandbox setup: %w", err)
	}
	return s, "active", nil
}

// emitSandboxWarning emits a SandboxStatusEvent when sandbox is not active and
// WarningOnUnsupportedPlatform is enabled, and independently when the sandbox
// is active but env_passthrough_all disables the credential barrier.
func emitSandboxWarning(cfg config.Config, status string, events output.EventSink) {
	if cfg.Sandbox.Enabled && cfg.Sandbox.EnvPassthroughAll {
		events.Emit(output.NewSandboxStatusEvent(status, "sandbox env_passthrough_all is enabled: the credential barrier is disabled and the full host environment (including credentials) is passed to sandboxed processes unfiltered."))
	}
	if status == "active" || !cfg.Sandbox.WarningOnUnsupportedPlatform {
		return
	}
	var msg string
	switch status {
	case "unavailable":
		msg = fmt.Sprintf("sandbox unavailable: bubblewrap is not supported on %s. Bash and subprocess tools run unsandboxed.", runtime.GOOS)
	case "bypassed":
		msg = "sandbox bypassed: running with --unsafe or sandbox.enabled=false. Bash and subprocess tools run unsandboxed."
	}
	if msg != "" {
		events.Emit(output.NewSandboxStatusEvent(status, msg))
	}
}

// emitProjectContextDeprecationWarning warns when the legacy
// project_context.max_tokens key is set. The event is advisory only and never
// carries sandbox status.
func emitProjectContextDeprecationWarning(cfg config.Config, events output.EventSink) {
	for _, msg := range projectContextConfigWarnings(cfg) {
		events.Emit(output.NewConfigWarningEvent(msg))
	}
}

// projectContextConfigWarnings returns the user-facing warnings for deprecated
// project_context config keys, empty when none apply.
func projectContextConfigWarnings(cfg config.Config) []string {
	if cfg.ProjectContext.MaxTokens == 0 {
		return nil
	}
	return []string{"project_context.max_tokens is deprecated; use max_bytes (converted as max_tokens x 4)"}
}

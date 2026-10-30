package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/luispabon/steiner/internal/advisor"
	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/delegation"
	"github.com/luispabon/steiner/internal/history"
	"github.com/luispabon/steiner/internal/lsp"
	"github.com/luispabon/steiner/internal/mcp"
	"github.com/luispabon/steiner/internal/oneshot"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/quota"
	"github.com/luispabon/steiner/internal/sandbox"
	"github.com/luispabon/steiner/internal/session"
	"github.com/luispabon/steiner/internal/skill"
	"github.com/luispabon/steiner/internal/tool"
	"github.com/luispabon/steiner/internal/tool/builtin"
	"github.com/luispabon/steiner/internal/tui"
	"github.com/luispabon/steiner/internal/usagestats"
	"github.com/luispabon/steiner/skills"
)

func loadRuntimeConfig(cmd *cobra.Command, flags *cliFlags, modelAlias string) (config.Config, error) {
	overrides := config.CLIOverrides{
		ConfigPath: flags.configPath,
		Model:      modelAlias,
		Profile:    flags.profile,
		Verbose:    flags.verbose,
		Unsafe:     flags.unsafe,
	}
	if modelAlias == "" {
		overrides.Model = flags.model
	}
	return loadCLIConfig(cmd, flags, overrides)
}

// preflightRuntimeModels checks that cfg can satisfy requirement. It runs
// before the runtime's long-lived resources (providers, servers, sandbox,
// event writers) are created.
func preflightRuntimeModels(cfg config.Config, requirement runtimeRequirement) error {
	switch requirement {
	case requireOrchestratorModel:
		ref := cfg.Models.Effective.ActiveOrchestratorModel
		if strings.TrimSpace(ref) == "" {
			ref = cfg.Models.Effective.DefaultModel
		}
		return config.RequireModelReference(cfg, ref)
	case requireOneshotModels:
		return oneshot.RequireModels(cfg)
	default:
		return nil
	}
}

// pruneRuntimeArtifacts applies shared fetch/bash artifact retention to the
// project root and the (possibly worktree) work directory.
func pruneRuntimeArtifacts(projectRoot, workDir string) (int, error) {
	return builtin.PruneToolArtifacts([]string{projectRoot, workDir})
}

func buildRuntimeWithRoots(ctx context.Context, cmd *cobra.Command, flags *cliFlags, projectRoot, workDir, modelAlias string) (cliRuntime, error) {
	if err := session.EnsureSteinerProjectDir(projectRoot); err != nil {
		return cliRuntime{}, err
	}
	// Best-effort: a failed prune must not block startup, and there is no
	// event sink yet at this point in composition to report it through.
	_, _ = pruneRuntimeArtifacts(projectRoot, workDir)
	cfg, err := loadRuntimeConfig(cmd, flags, modelAlias)
	if err != nil {
		return cliRuntime{}, err
	}
	if err := preflightRuntimeModels(cfg, flags.modelRequirement); err != nil {
		return cliRuntime{}, err
	}
	httpClient := runtimeHTTPClient()
	modelCatalog, modelCatalogEndpoints, modelPopularity := buildModelCatalogService(&cfg, httpClient)
	modelResolver := provider.NewResolver(provider.ResolverOptions{
		HTTPClient: httpClient,
		Catalog:    newCatalogMetadataAdapter(modelCatalog, &cfg),
	})
	var rt cliRuntime
	events, closeFn, err := buildRuntimeEventSink(cfg, cmd, flags)
	if err != nil {
		return cliRuntime{}, err
	}
	rt.events = events
	rt.closeFn = closeFn
	if flags.trustResolved {
		slog.Info("project trust", "root", flags.projectRoot, "source", flags.trustSource)
	}
	diagnosticsWriter, err := buildRuntimeDiagnostics(cfg)
	if err != nil {
		closeRuntime(&rt)
		return cliRuntime{}, err
	}
	rt.diagnostics = diagnosticsWriter
	delegationLogger, err := buildDelegationLogger(cfg, flags, diagnosticsWriter)
	if err != nil {
		closeRuntime(&rt)
		return cliRuntime{}, err
	}
	rt.delegationLogger = delegationLogger
	streamErrorLog, err := buildStreamErrorLogger(cfg, flags, diagnosticsWriter)
	if err != nil {
		closeRuntime(&rt)
		return cliRuntime{}, fmt.Errorf("build stream error logger: %w", err)
	}
	rt.streamErrorLog = streamErrorLog
	compactionLogFile := runtimeCompactionLogFile(cfg, flags)
	delegationActiveController := delegation.NewActiveController()
	delegationSupervisor := delegation.NewSupervisor(delegation.SupervisorOptions{MaxParallel: max(cfg.SubAgent.MaxParallel, 1), Controller: delegationActiveController})
	workDir, registry := buildRuntimeRegistry(cfg, nil, workDir, delegationSupervisor)
	quotaService := buildRuntimeQuotaService()
	claudeSubPool := newClaudeSubscriptionPool(provider.ClaudeSubscriptionPoolOptions{
		ToolOutputMaxBytes: cfg.Limits.ToolOutputMaxBytes,
		WorkDir:            workDir,
		QuotaObserver:      quotaService.Observe,
	})
	rt.claudeSubPool = claudeSubPool
	providerFactory := buildRuntimeProviderFactory(httpClient, streamErrorLog, claudeSubPool, quotaService)
	homeDir, skillBundledFS, skillNames, skillSources, skillDescriptions, err := discoverRuntimeSkills(ctx, projectRoot)
	if err != nil {
		closeRuntime(&rt)
		return cliRuntime{}, err
	}
	sb, status, err := buildRuntimeSandbox(&cfg, projectRoot, workDir, homeDir)
	if err != nil {
		closeRuntime(&rt)
		return cliRuntime{}, err
	}
	rt.sandbox = sb

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
			closeRuntime(&rt)
			return cliRuntime{}, err
		}
		closeFn = joinClosers(closeFn, mcpServerLogWriter.Close)
		rt.closeFn = closeFn

		mcpStderr := selectServerStderr(mcpServerLogPath, flags.asyncMCP, mcpServerLogWriter)
		mcpMgr, mcpState = connectRuntimeMCP(ctx, cfg, sb, flags.asyncMCP, events, mcpStderr)
		rt.mcpManager = mcpMgr
	}

	// Construct LSP manager when enabled. Unlike MCP, servers start lazily on
	// first tool call, so this never blocks startup.
	var lspMgr *lsp.Manager
	if cfg.LSP.Enabled {
		lspServerLogPath := lsp.ServerLogPath(runtimeLogFile(cfg, flags))
		lspServerLogWriter, err := buildLSPServerLogWriter(lspServerLogPath)
		if err != nil {
			closeRuntime(&rt)
			return cliRuntime{}, err
		}
		closeFn = joinClosers(closeFn, lspServerLogWriter.Close)
		rt.closeFn = closeFn

		lspStderr := selectServerStderr(lspServerLogPath, flags.asyncMCP, lspServerLogWriter)
		lspMgr = connectRuntimeLSP(cfg, sb, workDir, events, lspStderr)
		rt.lspManager = lspMgr
	}

	// Rebuild registry with sandbox, MCP and LSP tools now that workDir and homeDir are known.
	if sb != nil || mcpMgr != nil || lspMgr != nil {
		registry = buildRuntimeRegistryWithSandbox(cfg, workDir, sb, mcpMgr, lspMgr, delegationSupervisor)
	}
	historyWriter, sessionStore, err := buildRuntimeSessionStores(homeDir)
	if err != nil {
		closeRuntime(&rt)
		return cliRuntime{}, err
	}
	sharedInput, approvalInput, approvalClose := buildRuntimeInputs(cmd.InOrStdin())
	closeFn = joinClosers(closeFn, approvalClose)
	rt.closeFn = closeFn

	return cliRuntime{
		cfg:                          cfg,
		sandboxStatus:                status,
		configWarnings:               projectContextConfigWarnings(cfg),
		providerFactory:              providerFactory,
		httpClient:                   httpClient,
		registry:                     registry,
		toolNames:                    registry.Names(),
		skillNames:                   skillNames,
		skillSources:                 skillSources,
		skillDescriptions:            skillDescriptions,
		skillBundledFS:               skillBundledFS,
		projectRoot:                  projectRoot,
		workDir:                      workDir,
		homeDir:                      homeDir,
		sandbox:                      sb,
		mcpManager:                   mcpMgr,
		mcpState:                     mcpState,
		lspManager:                   lspMgr,
		stdin:                        cmd.InOrStdin(),
		human:                        output.NewStream(cmd.OutOrStdout()),
		status:                       output.NewStream(cmd.ErrOrStderr()),
		events:                       events,
		sharedInput:                  sharedInput,
		approvalIn:                   approvalInput,
		closeFn:                      closeFn,
		historyWriter:                historyWriter,
		sessionStore:                 sessionStore,
		delegationLogger:             delegationLogger,
		streamErrorLog:               streamErrorLog,
		claudeSubPool:                claudeSubPool,
		quotaService:                 quotaService,
		diagnostics:                  diagnosticsWriter,
		delegationSessionStore:       delegation.NewSessionStore(),
		delegationCacheKeyStore:      delegation.NewCacheKeyStore(),
		delegationActiveController:   delegationActiveController,
		delegationSupervisor:         delegationSupervisor,
		delegationAdvisorBudgetStore: delegation.NewAdvisorBudgetStore(),
		advisorState:                 advisor.NewSharedState(),
		compactionLogFile:            compactionLogFile,
		usageRecorder:                usagestats.New(nil),
		imageStore:                   agent.NewImageStore(filepath.Join(workDir, ".steiner", "tmp", "images")),
		visionCapabilities:           agent.NewVisionCapabilities(cfg.Models.Effective.SubAgents["vision"] != ""),
		modelCatalog:                 modelCatalog,
		modelCatalogEndpoints:        modelCatalogEndpoints,
		modelPopularity:              modelPopularity,
		modelResolver:                modelResolver,
		modelEntriesUpdates:          make(chan []tui.ModelEntry, max(1, len(modelCatalogEndpoints))),
		codexWSProviderCache:         provider.NewCodexWSCache(),
	}, nil
}

// newClaudeSubscriptionPool builds the claude_subscription pool. Tests replace
// it to inspect the options passed to the pool.
var newClaudeSubscriptionPool = provider.NewClaudeSubscriptionPool

// buildRuntimeQuotaService returns the process quota service. Snapshots persist
// to the default store path. When that path cannot be resolved, the failure is
// logged and the service keeps snapshots in memory only.
func buildRuntimeQuotaService() *quota.Service {
	opts := quota.ServiceOptions{
		Logf: func(format string, args ...any) {
			slog.Warn(fmt.Sprintf(format, args...))
		},
	}
	path, err := quota.DefaultStorePath()
	if err != nil {
		slog.Warn("quota history not persisted", "error", err)
	} else {
		opts.Store = quota.NewStore(path)
	}
	return quota.NewService(opts)
}

// runtimeHTTPClient returns the process-wide HTTP client shared by providers,
// sub-agents, the advisor, model catalog and metadata lookups, resolver probes
// and quota fetchers.
//
// There is no client-level timeout: without a provider timeout, streams can run
// indefinitely. ResponseHeaderTimeout is a 30s safety net for the header phase
// so a stuck server cannot hang the initial read. Providers that set
// config.timeout get Client.Timeout applied in NewOpenAICompat, which clones the
// transport and clears ResponseHeaderTimeout so the user-supplied timeout bounds
// the whole request (headers + body + streaming response).
//
// Connections are pooled per host with no cap on concurrent connections per
// host, so HTTP/1.1-only upstreams serve parallel sub-agent and quota requests
// instead of queueing them behind one streaming response. MaxIdleConns is a
// global budget so switching hosts keeps idle connections warm.
// ProxyFromEnvironment honours HTTP_PROXY/HTTPS_PROXY/NO_PROXY, and the dial and
// handshake timeouts bound stalled connections.
//
// ForceAttemptHTTP2 must stay true: a non-zero DialContext disables HTTP/2
// otherwise. Clone() does not copy the TLSNextProto map that the original's
// lazy setup creates, so a clone with ForceAttemptHTTP2=false would
// conservatively disable HTTP/2 while TLS still advertises "h2". The result is
// "net/http: HTTP/1.x transport connection broken: malformed HTTP response"
// when the upstream negotiates h2.
func runtimeHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 0,
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   30 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   16,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
		},
	}
}

func buildRuntimeRegistry(cfg config.Config, sb *sandbox.Sandbox, workDir string, sup *delegation.Supervisor) (string, *tool.Registry) {
	registry := runtimeRegistryWithSinkAndMode(cfg, workDir, nil, false, nil, sb, nil, nil, withPendingSubAgents(sup))
	return workDir, registry
}

// buildRuntimeRegistryWithSandbox rebuilds the registry for a known workDir with a sandbox, MCP and LSP tools.
func buildRuntimeRegistryWithSandbox(cfg config.Config, workDir string, sb *sandbox.Sandbox, mcpMgr *mcp.Manager, lspMgr *lsp.Manager, sup *delegation.Supervisor) *tool.Registry {
	registry := runtimeRegistryWithSinkAndMode(cfg, workDir, nil, false, nil, sb, mcpMgr, lspMgr, withPendingSubAgents(sup))
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

func buildRuntimeInputs(stdin io.Reader) (*bufio.Reader, *bufio.Reader, func() error) {
	sharedInput := bufio.NewReader(stdin)
	approvalInput, approvalClose := openApprovalInput(stdin)
	return sharedInput, approvalInput, approvalClose
}

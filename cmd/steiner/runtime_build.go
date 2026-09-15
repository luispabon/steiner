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
	"time"

	"github.com/spf13/cobra"

	"github.com/luispabon/steiner/internal/advisor"
	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/delegation"
	"github.com/luispabon/steiner/internal/history"
	"github.com/luispabon/steiner/internal/lsp"
	"github.com/luispabon/steiner/internal/mcp"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/sandbox"
	"github.com/luispabon/steiner/internal/session"
	"github.com/luispabon/steiner/internal/skill"
	"github.com/luispabon/steiner/internal/tool"
	"github.com/luispabon/steiner/internal/tool/builtin"
	"github.com/luispabon/steiner/internal/tui"
	"github.com/luispabon/steiner/internal/usagestats"
	"github.com/luispabon/steiner/skills"
)

func loadRuntimeConfig(_ *cobra.Command, flags *cliFlags, modelAlias string) (config.Config, error) {
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
	return config.Load(config.LoadOptions{CLI: overrides})
}

func buildRuntimeWithRoots(ctx context.Context, cmd *cobra.Command, flags *cliFlags, projectRoot, workDir, modelAlias string) (cliRuntime, error) {
	if err := session.EnsureSteinerProjectDir(projectRoot); err != nil {
		return cliRuntime{}, err
	}
	// Best-effort: a failed prune must not block startup, and there is no
	// event sink yet at this point in composition to report it through.
	_, _ = builtin.PruneFetchedDir(projectRoot)
	cfg, err := loadRuntimeConfig(cmd, flags, modelAlias)
	if err != nil {
		return cliRuntime{}, err
	}
	httpClient := runtimeHTTPClient()
	modelCatalog, modelCatalogEndpoints, modelPopularity := buildModelCatalogService(&cfg, httpClient)
	modelResolver := provider.NewResolver(provider.ResolverOptions{HTTPClient: httpClient})
	events, closeFn, err := buildRuntimeEventSink(cfg, cmd, flags)
	if err != nil {
		return cliRuntime{}, err
	}
	diagnosticsWriter, err := buildRuntimeDiagnostics(cfg)
	if err != nil {
		return cliRuntime{}, err
	}
	delegationLogger, err := buildDelegationLogger(cfg, flags, diagnosticsWriter)
	if err != nil {
		return cliRuntime{}, err
	}
	streamErrorLog, err := buildStreamErrorLogger(cfg, flags, diagnosticsWriter)
	if err != nil {
		return cliRuntime{}, fmt.Errorf("build stream error logger: %w", err)
	}
	providerFactory := buildRuntimeProviderFactory(httpClient, streamErrorLog)
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

		mcpStderr := selectServerStderr(mcpServerLogPath, flags.asyncMCP, mcpServerLogWriter)
		mcpMgr, mcpState = connectRuntimeMCP(ctx, cfg, sb, flags.asyncMCP, events, mcpStderr)
	}

	// Construct LSP manager when enabled. Unlike MCP, servers start lazily on
	// first tool call, so this never blocks startup.
	var lspMgr *lsp.Manager
	if cfg.LSP.Enabled {
		lspServerLogPath := lsp.ServerLogPath(runtimeLogFile(cfg, flags))
		lspServerLogWriter, err := buildLSPServerLogWriter(lspServerLogPath)
		if err != nil {
			return cliRuntime{}, err
		}
		closeFn = joinClosers(closeFn, lspServerLogWriter.Close)

		lspStderr := selectServerStderr(lspServerLogPath, flags.asyncMCP, lspServerLogWriter)
		lspMgr = connectRuntimeLSP(cfg, sb, workDir, events, lspStderr)
	}

	// Rebuild registry with sandbox, MCP and LSP tools now that workDir and homeDir are known.
	if sb != nil || mcpMgr != nil || lspMgr != nil {
		registry = buildRuntimeRegistryWithSandbox(cfg, workDir, sb, mcpMgr, lspMgr)
	}
	historyWriter, sessionStore, err := buildRuntimeSessionStores(homeDir)
	if err != nil {
		return cliRuntime{}, err
	}
	sharedInput, approvalInput, approvalClose := buildRuntimeInputs(cmd.InOrStdin())
	closeFn = joinClosers(closeFn, approvalClose)

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
		diagnostics:                  diagnosticsWriter,
		delegationSessionStore:       delegation.NewSessionStore(),
		delegationCacheKeyStore:      delegation.NewCacheKeyStore(),
		delegationActiveController:   delegation.NewActiveController(),
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

func buildRuntimeRegistry(cfg config.Config, sb *sandbox.Sandbox, workDir string) (string, *tool.Registry) {
	registry := runtimeRegistryWithSinkAndMode(cfg, workDir, nil, false, nil, sb, nil, nil)
	return workDir, registry
}

// buildRuntimeRegistryWithSandbox rebuilds the registry for a known workDir with a sandbox, MCP and LSP tools.
func buildRuntimeRegistryWithSandbox(cfg config.Config, workDir string, sb *sandbox.Sandbox, mcpMgr *mcp.Manager, lspMgr *lsp.Manager) *tool.Registry {
	registry := runtimeRegistryWithSinkAndMode(cfg, workDir, nil, false, nil, sb, mcpMgr, lspMgr)
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

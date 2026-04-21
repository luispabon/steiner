package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/repl"
	"github.com/luispabon/steiner/internal/skill"
	"github.com/luispabon/steiner/internal/tool"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

const version = "dev"

type cliFlags struct {
	configPath string
	model      string
	verbose    bool
	exec       bool
	logFile    string
}

type cliRuntime struct {
	cfg         config.Config
	provider    provider.Provider
	registry    *tool.Registry
	toolNames   []string
	skillNames  []string
	workDir     string
	homeDir     string
	stdin       io.Reader
	human       *output.Stream
	status      *output.Stream
	events      output.EventSink
	sharedInput *bufio.Reader
	approvalIn  *bufio.Reader
	close       func() error
}

var buildRuntime = defaultBuildRuntime

func main() {
	if err := newRootCommand().Execute(); err != nil {
		os.Exit(1)
	}
}

func newRootCommand() *cobra.Command {
	flags := &cliFlags{}

	rootCmd := &cobra.Command{
		Use:          "steiner",
		SilenceUsage: true,
		Args:         cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if flags.exec {
				return runExecMode(cmd, flags, args)
			}
			if len(args) > 0 {
				return fmt.Errorf("unexpected arguments: %s", strings.Join(args, " "))
			}
			return runInteractiveMode(cmd, flags)
		},
	}

	rootCmd.PersistentFlags().StringVar(&flags.configPath, "config", "", "project config file path")
	rootCmd.PersistentFlags().StringVar(&flags.model, "model", "", "override provider model")
	rootCmd.PersistentFlags().BoolVar(&flags.verbose, "verbose", false, "enable verbose logging")
	rootCmd.PersistentFlags().BoolVar(&flags.exec, "exec", false, "run a single request and exit")
	rootCmd.PersistentFlags().StringVar(&flags.logFile, "log-file", "", "write full session logs to file")

	rootCmd.AddCommand(newVersionCommand())
	rootCmd.AddCommand(newConfigCommand(flags))
	rootCmd.AddCommand(newToolsCommand(flags))
	rootCmd.AddCommand(newSkillsCommand(flags))

	return rootCmd
}

func newVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the steiner version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "steiner %s\n", version)
			return err
		},
	}
}

func newConfigCommand(flags *cliFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "config",
		Short: "Print the resolved configuration",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			resolved, err := config.Load(config.LoadOptions{
				CLI: config.CLIOverrides{
					ConfigPath: flags.configPath,
					Model:      flags.model,
					Verbose:    flags.verbose,
				},
			})
			if err != nil {
				return err
			}

			data, err := yaml.Marshal(resolved)
			if err != nil {
				return err
			}

			_, err = fmt.Fprint(cmd.OutOrStdout(), string(data))
			return err
		},
	}
}

func newToolsCommand(flags *cliFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "tools",
		Short: "List configured tools",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := buildRuntime(cmd.Context(), cmd, flags)
			if err != nil {
				return err
			}
			renderNames(output.NewStream(cmd.OutOrStdout()), "tools", rt.toolNames)
			return nil
		},
	}
}

func newSkillsCommand(flags *cliFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "skills",
		Short: "List discovered skills",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := buildRuntime(cmd.Context(), cmd, flags)
			if err != nil {
				return err
			}
			renderNames(output.NewStream(cmd.OutOrStdout()), "skills", rt.skillNames)
			return nil
		},
	}
}

func runInteractiveMode(cmd *cobra.Command, flags *cliFlags) error {
	rt, err := buildRuntime(cmd.Context(), cmd, flags)
	if err != nil {
		return err
	}
	defer closeRuntime(rt)

	session := repl.NewSession(
		cliRunner{
			runtime: rt,
			approver: agent.NewEventingApprover(
				rt.events,
				promptingApprover{
					reader: approvalReader(rt),
					out:    rt.status,
				},
			),
		},
		interactiveInput(rt),
		rt.human,
		rt.status,
		rt.toolNames,
		rt.skillNames,
	)
	return session.Run(cmd.Context())
}

func runExecMode(cmd *cobra.Command, flags *cliFlags, args []string) error {
	rt, err := buildRuntime(cmd.Context(), cmd, flags)
	if err != nil {
		return err
	}
	defer closeRuntime(rt)

	promptText := strings.TrimSpace(strings.Join(args, " "))
	if promptText == "" {
		promptText, err = readPromptFromInput(rt.sharedInput)
		if err != nil {
			return err
		}
	}
	if promptText == "" {
		return fmt.Errorf("exec mode requires a prompt")
	}
	rt.events.Emit(output.NewUserInputEvent(promptText, "exec"))

	result, err := cliRunner{
		runtime: rt,
		approver: agent.NewEventingApprover(
			rt.events,
			promptingApprover{
				reader: approvalReader(rt),
				out:    rt.status,
			},
		),
	}.Run(cmd.Context(), []agent.Message{{Role: agent.MessageRoleUser, Content: promptText}}, nil)
	if err != nil {
		return err
	}

	reply := strings.TrimSpace(result.Reply)
	if reply != "" {
		rt.human.Println(reply)
	}
	return nil
}

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

	scheduler, err := provider.NewScheduler(cfg.Provider.Parallelism)
	if err != nil {
		return cliRuntime{}, err
	}

	prov, err := provider.NewOpenAICompat(provider.OpenAICompatConfig{
		BaseURL:   cfg.Provider.BaseURL,
		APIKey:    cfg.Provider.APIKey,
		Model:     cfg.Provider.Model,
		Scheduler: scheduler,
	})
	if err != nil {
		return cliRuntime{}, err
	}
	provWithLogging := provider.Provider(prov)

	events := output.EventSink(output.NewStream(cmd.ErrOrStderr()))
	var closeFn func() error
	if strings.TrimSpace(flags.logFile) != "" {
		fileSink, err := output.NewFileLogSink(flags.logFile)
		if err != nil {
			return cliRuntime{}, err
		}
		events = output.NewMultiSink(events, fileSink)
		closeFn = fileSink.Close
	}
	provWithLogging = loggingProvider{
		inner: provWithLogging,
		sink:  events,
	}

	registry, err := runtimeRegistry(cfg)
	if err != nil {
		return cliRuntime{}, err
	}

	currentDir, err := os.Getwd()
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

	sharedInput := bufio.NewReader(cmd.InOrStdin())
	approvalInput, approvalClose := openApprovalInput(cmd.InOrStdin())
	closeFn = joinClosers(closeFn, approvalClose)

	return cliRuntime{
		cfg:         cfg,
		provider:    provWithLogging,
		registry:    registry,
		toolNames:   registry.Names(),
		skillNames:  skillNames,
		workDir:     currentDir,
		homeDir:     homeDir,
		stdin:       cmd.InOrStdin(),
		human:       output.NewStream(cmd.OutOrStdout()),
		status:      output.NewStream(cmd.ErrOrStderr()),
		events:      events,
		sharedInput: sharedInput,
		approvalIn:  approvalInput,
		close:       closeFn,
	}, nil
}

type cliRunner struct {
	runtime  cliRuntime
	approver tool.Approver
}

func (r cliRunner) Run(ctx context.Context, conversation []agent.Message, skillNames []string) (repl.RunResult, error) {
	if r.runtime.provider == nil {
		return repl.RunResult{}, fmt.Errorf("provider is required")
	}

	assembly := prompt.AssemblyOptions{
		HomeDir:                   r.runtime.homeDir,
		ProjectRoot:               r.runtime.workDir,
		SkillsRoot:                prompt.DefaultSkillsRoot(r.runtime.homeDir),
		SkillNames:                append([]string(nil), skillNames...),
		ProjectContextBudgetBytes: r.runtime.cfg.ProjectContext.MaxTokens,
		ProjectContextExtraFiles:  append([]string(nil), r.runtime.cfg.ProjectContext.ExtraFiles...),
		ProjectContextIgnoreFiles: append([]string(nil), r.runtime.cfg.ProjectContext.IgnoreFiles...),
		Conversation:              toProviderConversation(conversation),
	}

	temperature := r.runtime.cfg.Provider.Temperature
	maxTokens := r.runtime.cfg.Provider.MaxCompletionTokens
	var maxTokensPtr *int
	if maxTokens > 0 {
		maxTokensPtr = &maxTokens
	}

	var diagnostics []output.Event
	events := output.NewMultiSink(
		r.runtime.events,
		output.SinkFunc(func(event output.Event) {
			if event.Type == output.EventTypeContextDiagnostics {
				diagnostics = append(diagnostics, event)
			}
		}),
	)
	executor := tool.NewExecutor(r.runtime.registry, r.runtime.cfg, r.approver, r.runtime.workDir)
	runner := agent.NewRunner()
	state, err := runner.Run(ctx, agent.RunRequest{
		Provider:    r.runtime.provider,
		Executor:    executor,
		Tools:       registryToolSpecs(r.runtime.registry),
		Prompt:      assembly,
		Model:       r.runtime.cfg.Provider.Model,
		Temperature: &temperature,
		MaxTokens:   maxTokensPtr,
		Limits: agent.Limits{
			MaxTurns:  r.runtime.cfg.Limits.MaxTurns,
			MaxTokens: r.runtime.cfg.Limits.MaxTokens,
		},
		Events: events,
	})
	if err != nil {
		return repl.RunResult{}, err
	}

	return repl.RunResult{
		Conversation: state.Conversation,
		Reply:        lastAssistantReply(state.Conversation),
		Diagnostics:  cloneEvents(diagnostics),
	}, nil
}

func cloneEvents(events []output.Event) []output.Event {
	if len(events) == 0 {
		return nil
	}
	out := make([]output.Event, len(events))
	copy(out, events)
	return out
}

func closeRuntime(rt cliRuntime) {
	if rt.close != nil {
		_ = rt.close()
	}
}

func approvalReader(rt cliRuntime) *bufio.Reader {
	if rt.approvalIn != nil {
		return rt.approvalIn
	}
	return rt.sharedInput
}

func interactiveInput(rt cliRuntime) io.Reader {
	if rt.stdin != nil {
		return rt.stdin
	}
	return rt.sharedInput
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

func readPromptFromInput(reader *bufio.Reader) (string, error) {
	if reader == nil {
		return "", fmt.Errorf("input is required")
	}
	line, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func lastAssistantReply(messages []agent.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		message := messages[i]
		if message.Role != agent.MessageRoleAssistant {
			continue
		}
		if reply := strings.TrimSpace(message.Content); reply != "" {
			return reply
		}
	}
	return ""
}

func renderNames(stream *output.Stream, heading string, names []string) {
	if stream == nil {
		return
	}
	if len(names) == 0 {
		stream.Printf("no %s configured\n", heading)
		return
	}
	stream.Printf("%s:\n", heading)
	for _, name := range names {
		stream.Printf("  %s\n", name)
	}
}

type promptingApprover struct {
	reader *bufio.Reader
	out    *output.Stream
}

func (a promptingApprover) Approve(ctx context.Context, req tool.ApprovalRequest) (tool.ApprovalResponse, error) {
	_ = ctx
	if a.out != nil {
		a.out.Printf("approve tool=%s mode=%s", req.Tool.Name, req.Mode)
		if len(req.Input) > 0 {
			a.out.Printf(" args=%s", output.CompactJSON(req.Input))
		}
		a.out.Printf(" [y/N] ")
	}
	if a.reader == nil {
		return tool.ApprovalResponse{Allow: false, Message: "approval input is unavailable"}, fmt.Errorf("approval input is unavailable")
	}
	line, err := a.reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return tool.ApprovalResponse{}, err
	}
	if err == io.EOF && strings.TrimSpace(line) == "" {
		return tool.ApprovalResponse{Allow: false, Message: "approval input is unavailable"}, fmt.Errorf("approval input is unavailable")
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return tool.ApprovalResponse{Allow: true, Message: "approved"}, nil
	default:
		return tool.ApprovalResponse{Allow: false, Message: "denied"}, nil
	}
}

func toProviderConversation(messages []agent.Message) []provider.Message {
	if len(messages) == 0 {
		return nil
	}
	out := make([]provider.Message, 0, len(messages))
	for _, message := range messages {
		wire := provider.Message{
			Role:       provider.MessageRole(message.Role),
			Content:    message.Content,
			Name:       message.Name,
			ToolCallID: message.ToolCallID,
		}
		if len(message.ToolCalls) > 0 {
			wire.ToolCalls = make([]provider.ToolCall, 0, len(message.ToolCalls))
			for _, call := range message.ToolCalls {
				wire.ToolCalls = append(wire.ToolCalls, provider.ToolCall{
					ID:        call.ID,
					Name:      call.Name,
					Arguments: cloneInput(call.Arguments),
				})
			}
		}
		out = append(out, wire)
	}
	return out
}

func cloneInput(input map[string]any) map[string]any {
	if len(input) == 0 {
		return map[string]any{}
	}
	cloned := make(map[string]any, len(input))
	for key, value := range input {
		cloned[key] = cloneValue(value)
	}
	return cloned
}

func cloneValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		return cloneInput(v)
	case []any:
		out := make([]any, len(v))
		for i := range v {
			out[i] = cloneValue(v[i])
		}
		return out
	default:
		return value
	}
}

func runtimeRegistry(cfg config.Config) (*tool.Registry, error) {
	execPath, err := os.Executable()
	if err != nil {
		return nil, err
	}

	registry := tool.NewRegistry(coreToolDefinitions(cfg, execPath)...)
	for _, def := range tool.NewRegistryFromConfig(cfg).Definitions() {
		registry.Register(def)
	}
	return registry, nil
}

func coreToolDefinitions(cfg config.Config, execPath string) []tool.ToolDef {
	coreBin := filepath.Join(filepath.Dir(execPath), "steiner-core-tools")
	return []tool.ToolDef{
		{
			Name:            "read",
			ExecPath:        coreBin,
			Subcommand:      "read",
			Description:     "Read a file from the project.",
			ParameterSchema: schemaObject(requiredStringProperty("path", "Project-relative file path to read.")),
			Timeout:         toolTimeout(cfg, "read"),
			Approval:        tool.ResolveApprovalMode(cfg, tool.ToolDef{Name: "read"}),
		},
		{
			Name:            "glob",
			ExecPath:        coreBin,
			Subcommand:      "glob",
			Description:     "Find files by glob pattern under the project.",
			ParameterSchema: schemaObject(requiredStringProperty("pattern", "Glob pattern such as \"cmd/**\" or \"*.go\".")),
			Timeout:         toolTimeout(cfg, "glob"),
			Approval:        tool.ResolveApprovalMode(cfg, tool.ToolDef{Name: "glob"}),
		},
		{
			Name:            "search",
			ExecPath:        coreBin,
			Subcommand:      "search",
			Description:     "Search text across project files.",
			ParameterSchema: schemaObject(requiredStringProperty("query", "Literal text to search for.")),
			Timeout:         toolTimeout(cfg, "search"),
			Approval:        tool.ResolveApprovalMode(cfg, tool.ToolDef{Name: "search"}),
		},
		{
			Name:        "write",
			ExecPath:    coreBin,
			Subcommand:  "write",
			Description: "Overwrite or create a file with complete contents.",
			ParameterSchema: schemaObject(
				requiredStringProperty("path", "Project-relative file path to write."),
				requiredStringProperty("contents", "Complete file contents to write."),
			),
			Timeout:  toolTimeout(cfg, "write"),
			Approval: tool.ResolveApprovalMode(cfg, tool.ToolDef{Name: "write"}),
		},
		{
			Name:        "edit",
			ExecPath:    coreBin,
			Subcommand:  "edit",
			Description: "Replace one exact snippet in a file.",
			ParameterSchema: schemaObject(
				requiredStringProperty("path", "Project-relative file path to edit."),
				requiredStringProperty("old", "Exact existing text to replace. Must match exactly once."),
				requiredStringProperty("new", "Replacement text."),
			),
			Timeout:  toolTimeout(cfg, "edit"),
			Approval: tool.ResolveApprovalMode(cfg, tool.ToolDef{Name: "edit"}),
		},
		{
			Name:        "bash",
			ExecPath:    coreBin,
			Subcommand:  "bash",
			Description: "Run a bash command inside the project root or a project subdirectory.",
			ParameterSchema: schemaObject(
				requiredStringProperty("command", "Shell command to run."),
				optionalStringProperty("cwd", "Optional project-relative working directory."),
			),
			Timeout:  toolTimeout(cfg, "bash"),
			Approval: tool.ResolveApprovalMode(cfg, tool.ToolDef{Name: "bash"}),
		},
	}
}

func toolTimeout(cfg config.Config, name string) time.Duration {
	if timeout, ok := cfg.Limits.ToolTimeouts[name]; ok && !timeout.IsZero() {
		return time.Duration(timeout.Duration())
	}
	if !cfg.Limits.ToolTimeoutDefault.IsZero() {
		return time.Duration(cfg.Limits.ToolTimeoutDefault.Duration())
	}
	return 30 * time.Second
}

func schemaObject(properties ...map[string]any) map[string]any {
	props := make(map[string]any, len(properties))
	required := make([]any, 0, len(properties))
	for _, property := range properties {
		name, _ := property["_name"].(string)
		if name == "" {
			continue
		}
		schema := cloneInput(property)
		delete(schema, "_name")
		delete(schema, "_required")
		props[name] = schema
		if req, _ := property["_required"].(bool); req {
			required = append(required, name)
		}
	}
	out := map[string]any{
		"type":                 "object",
		"properties":           props,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		out["required"] = required
	}
	return out
}

func requiredStringProperty(name, description string) map[string]any {
	return map[string]any{
		"_name":       name,
		"_required":   true,
		"type":        "string",
		"description": description,
	}
}

func optionalStringProperty(name, description string) map[string]any {
	return map[string]any{
		"_name":       name,
		"_required":   false,
		"type":        "string",
		"description": description,
	}
}

func registryToolSpecs(registry *tool.Registry) []provider.ToolSpec {
	if registry == nil {
		return nil
	}
	defs := registry.Definitions()
	if len(defs) == 0 {
		return nil
	}

	specs := make([]provider.ToolSpec, 0, len(defs))
	for _, def := range defs {
		specs = append(specs, provider.ToolSpec{
			Type: "function",
			Function: provider.ToolFunctionSpec{
				Name:        def.Name,
				Description: def.Description,
				Parameters:  cloneInput(def.ParameterSchema),
			},
		})
	}
	return specs
}

type loggingProvider struct {
	inner provider.Provider
	sink  output.EventSink
}

func (p loggingProvider) ChatCompletion(ctx context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
	if p.sink != nil {
		p.sink.Emit(output.NewAPIRequestEvent(req.Model, req.Messages, req.Tools))
	}
	resp, err := p.inner.ChatCompletion(ctx, req)
	if p.sink != nil {
		p.sink.Emit(output.NewAPIResponseEvent(resp.Message, resp.Usage, resp.FinishReason, err))
	}
	return resp, err
}

func (p loggingProvider) StreamChatCompletion(ctx context.Context, req provider.ChatRequest) (<-chan provider.ChatChunk, error) {
	return p.inner.StreamChatCompletion(ctx, req)
}

func (p loggingProvider) SupportsUsageStats() bool {
	return p.inner.SupportsUsageStats()
}

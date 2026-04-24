package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Scheduler      SchedulerConfig        `yaml:"scheduler"`
	Model          string                 `yaml:"model"`
	Models         map[string]ModelConfig `yaml:"models"`
	Limits         LimitsConfig           `yaml:"limits"`
	Approval       ApprovalConfig         `yaml:"approval"`
	SubAgent       SubAgentConfig         `yaml:"sub_agent"`
	Tools          map[string]ToolConfig  `yaml:"tools"`
	ProjectContext ProjectContextConfig   `yaml:"project_context"`
	Paths          PathsConfig            `yaml:"paths"`
	Logging        LoggingConfig          `yaml:"logging"`
}

type SchedulerConfig struct {
	Parallelism int `yaml:"parallelism"`
}

type ModelConfig struct {
	Type                string           `yaml:"type"`
	BaseURL             string           `yaml:"base_url"`
	APIKey              string           `yaml:"api_key"`
	Model               string           `yaml:"model"`
	Temperature         float64          `yaml:"temperature"`
	MaxCompletionTokens int              `yaml:"max_completion_tokens"`
	ContextSize         int              `yaml:"context_size"`
	Compaction          CompactionConfig `yaml:"compaction"`
}

type CompactionConfig struct {
	SafetyMarginTokens int `yaml:"safety_margin_tokens"`
	SummaryMaxTokens   int `yaml:"summary_max_tokens"`
}

type LimitsConfig struct {
	MaxTurns           int                 `yaml:"max_turns"`
	MaxTokens          int                 `yaml:"max_tokens"`
	ToolTimeoutDefault Duration            `yaml:"tool_timeout_default"`
	ToolTimeouts       map[string]Duration `yaml:"tool_timeouts"`
	ToolOutputMaxBytes int                 `yaml:"tool_output_max_bytes"`
}

type ApprovalMode string

const (
	ApprovalModeAuto   ApprovalMode = "auto"
	ApprovalModePrompt ApprovalMode = "prompt"
	ApprovalModeDeny   ApprovalMode = "deny"
)

type ApprovalConfig struct {
	Default   ApprovalMode            `yaml:"default"`
	Overrides map[string]ApprovalMode `yaml:"overrides"`
}

type SubAgentConfig struct {
	Enabled       bool     `yaml:"enabled"`
	MaxTurns      int      `yaml:"max_turns"`
	MaxTokens     int      `yaml:"max_tokens"`
	AllowedTools  []string `yaml:"allowed_tools"`
	AllowNesting  bool     `yaml:"allow_nesting"`
	MaxConcurrent int      `yaml:"max_concurrent"`
}

type ToolConfig struct {
	Exec        string         `yaml:"exec"`
	Subcommand  string         `yaml:"subcommand"`
	Description string         `yaml:"description"`
	Parameters  map[string]any `yaml:"parameters"`
	Timeout     Duration       `yaml:"timeout"`
	Approval    ApprovalMode   `yaml:"approval"`
	Constraints map[string]any `yaml:"constraints"`
}

type ProjectContextConfig struct {
	MaxTokens   int      `yaml:"max_tokens"`
	ExtraFiles  []string `yaml:"extra_files"`
	IgnoreFiles []string `yaml:"ignore_files"`
}

type PathsConfig struct {
	ProjectRootOnly bool     `yaml:"project_root_only"`
	WritablePaths   []string `yaml:"writable_paths"`
	BlockedPaths    []string `yaml:"blocked_paths"`
}

type LoggingConfig struct {
	Level string `yaml:"level"`
	File  string `yaml:"file"`
}

type CLIOverrides struct {
	ConfigPath string
	Model      string
	Verbose    bool
}

type LoadOptions struct {
	GlobalConfigPath  string
	ProjectConfigPath string
	WorkingDir        string
	HomeDir           string
	Env               map[string]string
	CLI               CLIOverrides
}

type Duration struct {
	value int64
	set   bool
}

func NewDuration(d string) (Duration, error) {
	parsed, err := parseDurationString(d)
	if err != nil {
		return Duration{}, err
	}
	return Duration{value: parsed.Nanoseconds(), set: true}, nil
}

func MustDuration(d string) Duration {
	parsed, err := NewDuration(d)
	if err != nil {
		panic(err)
	}
	return parsed
}

func (d Duration) Duration() int64 {
	return d.value
}

func (d Duration) IsZero() bool {
	return !d.set || d.value == 0
}

func (d Duration) String() string {
	if !d.set {
		return ""
	}
	return formatDurationNanos(d.value)
}

func (d Duration) MarshalYAML() (any, error) {
	if !d.set {
		return nil, nil
	}
	return d.String(), nil
}

func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	if node == nil {
		return nil
	}
	if node.Kind != yaml.ScalarNode {
		return fmt.Errorf("duration must be a scalar string")
	}
	if node.Tag == "!!null" || strings.TrimSpace(node.Value) == "" {
		*d = Duration{}
		return nil
	}
	parsed, err := parseDurationString(node.Value)
	if err != nil {
		return err
	}
	*d = Duration{value: parsed.Nanoseconds(), set: true}
	return nil
}

func Load(opts LoadOptions) (Config, error) {
	cfg := defaultConfig()

	env := opts.Env
	if env == nil {
		env = environMap(os.Environ())
	}

	homeDir := opts.HomeDir
	if homeDir == "" {
		homeDir = os.Getenv("HOME")
		if homeDir == "" {
			if resolved, err := os.UserHomeDir(); err == nil {
				homeDir = resolved
			}
		}
	}

	workingDir := opts.WorkingDir
	if workingDir == "" {
		if cwd, err := os.Getwd(); err == nil {
			workingDir = cwd
		}
	}

	globalPath := opts.GlobalConfigPath
	if globalPath == "" {
		if homeDir != "" {
			globalPath = filepath.Join(homeDir, ".config", "steiner", "config.yaml")
		}
	}

	projectPath := opts.ProjectConfigPath
	explicitProjectPath := false
	if opts.CLI.ConfigPath != "" {
		projectPath = opts.CLI.ConfigPath
		explicitProjectPath = true
	}
	if projectPath == "" && workingDir != "" {
		projectPath = filepath.Join(workingDir, ".steiner", "config.yaml")
	}

	for _, item := range []struct {
		path         string
		allowMissing bool
	}{
		{path: globalPath, allowMissing: true},
		{path: projectPath, allowMissing: !explicitProjectPath},
	} {
		if item.path == "" {
			continue
		}
		patch, err := readConfigPatch(item.path, env, item.allowMissing)
		if err != nil {
			return Config{}, err
		}
		applyPatch(&cfg, patch)
	}

	if err := applyEnvOverrides(&cfg, env); err != nil {
		return Config{}, err
	}
	applyCLIOverrides(&cfg, opts.CLI)
	normalizePaths(&cfg, homeDir)
	if err := Validate(cfg); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

type configPatch struct {
	Scheduler      *schedulerPatch        `yaml:"scheduler"`
	Model          *string                `yaml:"model"`
	Models         *map[string]modelPatch `yaml:"models"`
	Limits         *limitsPatch           `yaml:"limits"`
	Approval       *approvalPatch         `yaml:"approval"`
	SubAgent       *subAgentPatch         `yaml:"sub_agent"`
	Tools          *map[string]toolPatch  `yaml:"tools"`
	ProjectContext *projectContextPatch   `yaml:"project_context"`
	Paths          *pathsPatch            `yaml:"paths"`
	Logging        *loggingPatch          `yaml:"logging"`
}

type schedulerPatch struct {
	Parallelism *int `yaml:"parallelism"`
}

type modelPatch struct {
	Type                *string          `yaml:"type"`
	BaseURL             *string          `yaml:"base_url"`
	APIKey              *string          `yaml:"api_key"`
	Model               *string          `yaml:"model"`
	Temperature         *float64         `yaml:"temperature"`
	MaxCompletionTokens *int             `yaml:"max_completion_tokens"`
	ContextSize         *int             `yaml:"context_size"`
	Compaction          *compactionPatch `yaml:"compaction"`
}

type compactionPatch struct {
	SafetyMarginTokens *int `yaml:"safety_margin_tokens"`
	SummaryMaxTokens   *int `yaml:"summary_max_tokens"`
}

type limitsPatch struct {
	MaxTurns           *int                 `yaml:"max_turns"`
	MaxTokens          *int                 `yaml:"max_tokens"`
	ToolTimeoutDefault *Duration            `yaml:"tool_timeout_default"`
	ToolTimeouts       *map[string]Duration `yaml:"tool_timeouts"`
	ToolOutputMaxBytes *int                 `yaml:"tool_output_max_bytes"`
}

type approvalPatch struct {
	Default   *ApprovalMode            `yaml:"default"`
	Overrides *map[string]ApprovalMode `yaml:"overrides"`
}

type subAgentPatch struct {
	Enabled       *bool     `yaml:"enabled"`
	MaxTurns      *int      `yaml:"max_turns"`
	MaxTokens     *int      `yaml:"max_tokens"`
	AllowedTools  *[]string `yaml:"allowed_tools"`
	AllowNesting  *bool     `yaml:"allow_nesting"`
	MaxConcurrent *int      `yaml:"max_concurrent"`
}

type toolPatch struct {
	Exec        *string         `yaml:"exec"`
	Subcommand  *string         `yaml:"subcommand"`
	Description *string         `yaml:"description"`
	Parameters  *map[string]any `yaml:"parameters"`
	Timeout     *Duration       `yaml:"timeout"`
	Approval    *ApprovalMode   `yaml:"approval"`
	Constraints *map[string]any `yaml:"constraints"`
}

type projectContextPatch struct {
	MaxTokens   *int      `yaml:"max_tokens"`
	ExtraFiles  *[]string `yaml:"extra_files"`
	IgnoreFiles *[]string `yaml:"ignore_files"`
}

type pathsPatch struct {
	ProjectRootOnly *bool     `yaml:"project_root_only"`
	WritablePaths   *[]string `yaml:"writable_paths"`
	BlockedPaths    *[]string `yaml:"blocked_paths"`
}

type loggingPatch struct {
	Level *string `yaml:"level"`
	File  *string `yaml:"file"`
}

func readConfigPatch(path string, env map[string]string, allowMissing bool) (configPatch, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if allowMissing {
				return configPatch{}, nil
			}
			return configPatch{}, fmt.Errorf("config path %q does not exist", path)
		}
		return configPatch{}, fmt.Errorf("read config %q: %w", path, err)
	}

	expanded := expandEnvText(string(contents), func(name string) (string, bool) {
		v, ok := env[name]
		return v, ok
	})

	var patch configPatch
	dec := yaml.NewDecoder(strings.NewReader(expanded))
	dec.KnownFields(true)
	if err := dec.Decode(&patch); err != nil {
		return configPatch{}, fmt.Errorf("parse config %q: %w", path, err)
	}

	return patch, nil
}

func applyPatch(cfg *Config, patch configPatch) {
	if patch.Scheduler != nil {
		applySchedulerPatch(&cfg.Scheduler, patch.Scheduler)
	}
	if patch.Model != nil {
		cfg.Model = *patch.Model
	}
	if patch.Models != nil {
		if cfg.Models == nil {
			cfg.Models = make(map[string]ModelConfig)
		}
		for name, model := range *patch.Models {
			current := cfg.Models[name]
			applyModelPatch(&current, &model)
			cfg.Models[name] = current
		}
	}
	if patch.Limits != nil {
		applyLimitsPatch(&cfg.Limits, patch.Limits)
	}
	if patch.Approval != nil {
		applyApprovalPatch(&cfg.Approval, patch.Approval)
	}
	if patch.SubAgent != nil {
		applySubAgentPatch(&cfg.SubAgent, patch.SubAgent)
	}
	if patch.Tools != nil {
		if cfg.Tools == nil {
			cfg.Tools = make(map[string]ToolConfig)
		}
		for name, tool := range *patch.Tools {
			current := cfg.Tools[name]
			applyToolPatch(&current, &tool)
			cfg.Tools[name] = current
		}
	}
	if patch.ProjectContext != nil {
		applyProjectContextPatch(&cfg.ProjectContext, patch.ProjectContext)
	}
	if patch.Paths != nil {
		applyPathsPatch(&cfg.Paths, patch.Paths)
	}
	if patch.Logging != nil {
		applyLoggingPatch(&cfg.Logging, patch.Logging)
	}
}

func applySchedulerPatch(dst *SchedulerConfig, patch *schedulerPatch) {
	if patch.Parallelism != nil {
		dst.Parallelism = *patch.Parallelism
	}
}

func applyModelPatch(dst *ModelConfig, patch *modelPatch) {
	if patch.Type != nil {
		dst.Type = *patch.Type
	}
	if patch.BaseURL != nil {
		dst.BaseURL = *patch.BaseURL
	}
	if patch.APIKey != nil {
		dst.APIKey = *patch.APIKey
	}
	if patch.Model != nil {
		dst.Model = *patch.Model
	}
	if patch.Temperature != nil {
		dst.Temperature = *patch.Temperature
	}
	if patch.MaxCompletionTokens != nil {
		dst.MaxCompletionTokens = *patch.MaxCompletionTokens
	}
	if patch.ContextSize != nil {
		dst.ContextSize = *patch.ContextSize
	}
	if patch.Compaction != nil {
		applyCompactionPatch(&dst.Compaction, patch.Compaction)
	}
}

func applyCompactionPatch(dst *CompactionConfig, patch *compactionPatch) {
	if patch.SafetyMarginTokens != nil {
		dst.SafetyMarginTokens = *patch.SafetyMarginTokens
	}
	if patch.SummaryMaxTokens != nil {
		dst.SummaryMaxTokens = *patch.SummaryMaxTokens
	}
}

func applyLimitsPatch(dst *LimitsConfig, patch *limitsPatch) {
	if patch.MaxTurns != nil {
		dst.MaxTurns = *patch.MaxTurns
	}
	if patch.MaxTokens != nil {
		dst.MaxTokens = *patch.MaxTokens
	}
	if patch.ToolTimeoutDefault != nil {
		dst.ToolTimeoutDefault = *patch.ToolTimeoutDefault
	}
	if patch.ToolTimeouts != nil {
		if dst.ToolTimeouts == nil {
			dst.ToolTimeouts = make(map[string]Duration)
		}
		for name, timeout := range *patch.ToolTimeouts {
			dst.ToolTimeouts[name] = timeout
		}
	}
	if patch.ToolOutputMaxBytes != nil {
		dst.ToolOutputMaxBytes = *patch.ToolOutputMaxBytes
	}
}

func applyApprovalPatch(dst *ApprovalConfig, patch *approvalPatch) {
	if patch.Default != nil {
		dst.Default = *patch.Default
	}
	if patch.Overrides != nil {
		if dst.Overrides == nil {
			dst.Overrides = make(map[string]ApprovalMode)
		}
		for name, mode := range *patch.Overrides {
			dst.Overrides[name] = mode
		}
	}
}

func applySubAgentPatch(dst *SubAgentConfig, patch *subAgentPatch) {
	if patch.Enabled != nil {
		dst.Enabled = *patch.Enabled
	}
	if patch.MaxTurns != nil {
		dst.MaxTurns = *patch.MaxTurns
	}
	if patch.MaxTokens != nil {
		dst.MaxTokens = *patch.MaxTokens
	}
	if patch.AllowedTools != nil {
		dst.AllowedTools = append([]string(nil), (*patch.AllowedTools)...)
	}
	if patch.AllowNesting != nil {
		dst.AllowNesting = *patch.AllowNesting
	}
	if patch.MaxConcurrent != nil {
		dst.MaxConcurrent = *patch.MaxConcurrent
	}
}

func applyToolPatch(dst *ToolConfig, patch *toolPatch) {
	if patch.Exec != nil {
		dst.Exec = *patch.Exec
	}
	if patch.Subcommand != nil {
		dst.Subcommand = *patch.Subcommand
	}
	if patch.Description != nil {
		dst.Description = *patch.Description
	}
	if patch.Parameters != nil {
		dst.Parameters = copyStringAnyMap(*patch.Parameters)
	}
	if patch.Timeout != nil {
		dst.Timeout = *patch.Timeout
	}
	if patch.Approval != nil {
		dst.Approval = *patch.Approval
	}
	if patch.Constraints != nil {
		dst.Constraints = copyStringAnyMap(*patch.Constraints)
	}
}

func applyProjectContextPatch(dst *ProjectContextConfig, patch *projectContextPatch) {
	if patch.MaxTokens != nil {
		dst.MaxTokens = *patch.MaxTokens
	}
	if patch.ExtraFiles != nil {
		dst.ExtraFiles = append([]string(nil), (*patch.ExtraFiles)...)
	}
	if patch.IgnoreFiles != nil {
		dst.IgnoreFiles = append([]string(nil), (*patch.IgnoreFiles)...)
	}
}

func applyPathsPatch(dst *PathsConfig, patch *pathsPatch) {
	if patch.ProjectRootOnly != nil {
		dst.ProjectRootOnly = *patch.ProjectRootOnly
	}
	if patch.WritablePaths != nil {
		dst.WritablePaths = append([]string(nil), (*patch.WritablePaths)...)
	}
	if patch.BlockedPaths != nil {
		dst.BlockedPaths = append([]string(nil), (*patch.BlockedPaths)...)
	}
}

func applyLoggingPatch(dst *LoggingConfig, patch *loggingPatch) {
	if patch.Level != nil {
		dst.Level = *patch.Level
	}
	if patch.File != nil {
		dst.File = *patch.File
	}
}

func applyCLIOverrides(cfg *Config, cli CLIOverrides) {
	if cli.Model != "" {
		cfg.Model = cli.Model
	}
	if cli.Verbose {
		cfg.Logging.Level = "debug"
	}
}

func normalizePaths(cfg *Config, homeDir string) {
	expand := func(path string) string {
		if path == "" || !strings.HasPrefix(path, "~") {
			return path
		}
		if path == "~" {
			return homeDir
		}
		if strings.HasPrefix(path, "~/") && homeDir != "" {
			return filepath.Join(homeDir, path[2:])
		}
		return path
	}

	cfg.Logging.File = expand(cfg.Logging.File)

	for i := range cfg.ProjectContext.ExtraFiles {
		cfg.ProjectContext.ExtraFiles[i] = expand(cfg.ProjectContext.ExtraFiles[i])
	}
	for i := range cfg.ProjectContext.IgnoreFiles {
		cfg.ProjectContext.IgnoreFiles[i] = expand(cfg.ProjectContext.IgnoreFiles[i])
	}
	for i := range cfg.Paths.WritablePaths {
		cfg.Paths.WritablePaths[i] = expand(cfg.Paths.WritablePaths[i])
	}
	for i := range cfg.Paths.BlockedPaths {
		cfg.Paths.BlockedPaths[i] = expand(cfg.Paths.BlockedPaths[i])
	}
	for name, tool := range cfg.Tools {
		tool.Exec = expand(tool.Exec)
		cfg.Tools[name] = tool
	}
}

func copyStringAnyMap(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func environMap(env []string) map[string]string {
	out := make(map[string]string, len(env))
	for _, entry := range env {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		out[key] = value
	}
	return out
}

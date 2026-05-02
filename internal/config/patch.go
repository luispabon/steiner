package config

// configPatch represents a partial config update from YAML.
type configPatch struct {
	Scheduler         *schedulerPatch         `yaml:"scheduler"`
	Model             *modelPatch             `yaml:"model"`
	ModelAlias        string                  `yaml:"-"` // populated when model is a scalar alias string
	Models            *map[string]modelPatch  `yaml:"models"`
	Limits            *limitsPatch            `yaml:"limits"`
	Approval          *approvalPatch          `yaml:"approval"`
	SubAgent          *subAgentPatch          `yaml:"sub_agent"`
	Tools             *map[string]toolPatch   `yaml:"tools"`
	ProjectContext    *projectContextPatch    `yaml:"project_context"`
	Paths             *pathsPatch             `yaml:"paths"`
	Logging           *loggingPatch           `yaml:"logging"`
	ContextManagement *contextManagementPatch `yaml:"context_management"`
}

type contextManagementPatch struct {
	Mode               *ContextMode        `yaml:"mode"`
	CompactionStrategy *CompactionStrategy `yaml:"compaction_strategy"`
	MaskingWindowTurns *int                `yaml:"masking_window_turns"`
	ReadAnnotations    *bool               `yaml:"read_annotations"`
	ScratchpadMode     *ScratchpadMode     `yaml:"scratchpad_mode"`
}

type schedulerPatch struct {
	Parallelism *int `yaml:"parallelism"`
}

type modelPatch struct {
	Type                *string            `yaml:"type"`
	BaseURL             *string            `yaml:"base_url"`
	APIKey              *string            `yaml:"api_key"`
	Model               *string            `yaml:"model"`
	ExtraParams         *map[string]any    `yaml:"extra_params"`
	MaxCompletionTokens *int               `yaml:"max_completion_tokens"`
	ContextSize         *int               `yaml:"context_size"`
	Compaction          *compactionPatch   `yaml:"compaction"`
	Prompts             *modelPromptsPatch `yaml:"prompts"`
}

type modelPromptsPatch struct {
	System     *string `yaml:"system"`
	Compaction *string `yaml:"compaction"`
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
	Default       *ApprovalMode             `yaml:"default"`
	ToolOverrides *map[string]*ApprovalMode `yaml:"tool_overrides"`
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
	ExcludePaths    *[]string `yaml:"exclude_paths"`
	ExcludePatterns *[]string `yaml:"exclude_patterns"`
}

type loggingPatch struct {
	Enabled       *bool   `yaml:"enabled"`
	Level         *string `yaml:"level"`
	File          *string `yaml:"file"`
	ThinkingChunk *bool   `yaml:"thinking_chunk"`
}

// applyPatch applies a config patch to the config.
func applyPatch(cfg *Config, patch configPatch) {
	if patch.Scheduler != nil {
		applySchedulerPatch(&cfg.Scheduler, patch.Scheduler)
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
	if patch.ModelAlias != "" {
		if m, ok := cfg.Models[patch.ModelAlias]; ok {
			cfg.Model = m
		}
	}
	if patch.Model != nil {
		applyModelPatch(&cfg.Model, patch.Model)
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
	if patch.ContextManagement != nil {
		applyContextManagementPatch(&cfg.ContextManagement, patch.ContextManagement)
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
	if patch.ExtraParams != nil {
		dst.ExtraParams = copyStringAnyMap(*patch.ExtraParams)
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
	if patch.Prompts != nil {
		applyModelPromptsPatch(&dst.Prompts, patch.Prompts)
	}
}

func applyModelPromptsPatch(dst *ModelPrompts, patch *modelPromptsPatch) {
	if patch.System != nil {
		dst.System = *patch.System
	}
	if patch.Compaction != nil {
		dst.Compaction = *patch.Compaction
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
	if patch.ToolOverrides != nil {
		if dst.ToolOverrides == nil {
			dst.ToolOverrides = make(map[string]*ApprovalMode)
		}
		for name, mode := range *patch.ToolOverrides {
			dst.ToolOverrides[name] = mode
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
	if patch.ExcludePaths != nil {
		dst.ExcludePaths = append([]string(nil), (*patch.ExcludePaths)...)
	}
	if patch.ExcludePatterns != nil {
		dst.ExcludePatterns = append([]string(nil), (*patch.ExcludePatterns)...)
	}
}

func applyLoggingPatch(dst *LoggingConfig, patch *loggingPatch) {
	if patch.Enabled != nil {
		dst.Enabled = *patch.Enabled
	}
	if patch.Level != nil {
		dst.Level = *patch.Level
	}
	if patch.File != nil {
		dst.File = *patch.File
	}
	if patch.ThinkingChunk != nil {
		dst.ThinkingChunk = *patch.ThinkingChunk
	}
}

func applyContextManagementPatch(dst *ContextManagementConfig, patch *contextManagementPatch) {
	if patch.Mode != nil {
		dst.Mode = *patch.Mode
	}
	if patch.CompactionStrategy != nil {
		dst.CompactionStrategy = *patch.CompactionStrategy
	}
	if patch.MaskingWindowTurns != nil {
		dst.MaskingWindowTurns = *patch.MaskingWindowTurns
	}
	if patch.ReadAnnotations != nil {
		dst.ReadAnnotations = *patch.ReadAnnotations
	}
	if patch.ScratchpadMode != nil {
		dst.ScratchpadMode = *patch.ScratchpadMode
	}
}

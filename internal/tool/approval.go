package tool

import "github.com/luispabon/steiner/internal/config"

type ApprovalResolver struct {
	Config config.Config
}

func NewApprovalResolver(cfg config.Config) ApprovalResolver {
	return ApprovalResolver{Config: cfg}
}

func ResolveApprovalMode(cfg config.Config, def ToolDef) config.ApprovalMode {
	if def.Approval != "" {
		return def.Approval
	}
	if mode, ok := cfg.Approval.Overrides[def.Name]; ok && mode != "" {
		return mode
	}
	if cfg.Approval.Default != "" {
		return cfg.Approval.Default
	}
	return config.ApprovalModePrompt
}

func (r ApprovalResolver) ModeFor(def ToolDef) config.ApprovalMode {
	return ResolveApprovalMode(r.Config, def)
}

func IsApprovalPrompt(mode config.ApprovalMode) bool {
	return mode == config.ApprovalModePrompt || mode == ""
}

func IsApprovalDenied(mode config.ApprovalMode) bool {
	return mode == config.ApprovalModeDeny
}

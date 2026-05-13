package config

func applyApprovalConfigPatch(cfg *Config, patch configPatch) {
	if patch.Approval != nil {
		applyApprovalPatch(&cfg.Approval, patch.Approval)
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

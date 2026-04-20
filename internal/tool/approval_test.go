package tool

import (
	"testing"

	"github.com/luispabon/steiner/internal/config"
)

func TestResolveApprovalMode(t *testing.T) {
	cfg := config.Config{
		Approval: config.ApprovalConfig{
			Default: config.ApprovalModePrompt,
			Overrides: map[string]config.ApprovalMode{
				"read": config.ApprovalModeAuto,
				"bash": config.ApprovalModePrompt,
			},
		},
	}

	tests := []struct {
		cfg  config.Config
		name string
		def  ToolDef
		want config.ApprovalMode
	}{
		{
			cfg:  cfg,
			name: "tool explicit approval wins",
			def:  ToolDef{Name: "write", Approval: config.ApprovalModeDeny},
			want: config.ApprovalModeDeny,
		},
		{
			cfg:  cfg,
			name: "config override wins",
			def:  ToolDef{Name: "read"},
			want: config.ApprovalModeAuto,
		},
		{
			cfg:  cfg,
			name: "config default used",
			def:  ToolDef{Name: "search"},
			want: config.ApprovalModePrompt,
		},
		{
			cfg:  config.Config{},
			name: "empty config falls back to prompt",
			def:  ToolDef{Name: "search"},
			want: config.ApprovalModePrompt,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveApprovalMode(tc.cfg, tc.def)
			if got != tc.want {
				t.Fatalf("ResolveApprovalMode() = %q, want %q", got, tc.want)
			}
		})
	}
}

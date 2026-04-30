package tool

import (
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/config"
)

func TestResolveApprovalMode(t *testing.T) {
	cfg := config.Config{
		Approval: config.ApprovalConfig{
			Default: config.ApprovalModePrompt,
			ToolOverrides: map[string]*config.ApprovalMode{
				"read": configApprovalModePtr(config.ApprovalModeAuto),
				"bash": nil,
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
			def:  ToolDef{Name: "grep"},
			want: config.ApprovalModePrompt,
		},
		{
			cfg:  cfg,
			name: "nil config override uses default",
			def:  ToolDef{Name: "bash"},
			want: config.ApprovalModePrompt,
		},
		{
			cfg:  config.Config{},
			name: "empty config falls back to prompt",
			def:  ToolDef{Name: "grep"},
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

func configApprovalModePtr(mode config.ApprovalMode) *config.ApprovalMode {
	return &mode
}

func TestApprovalResolverBuildsPreviewFromNormalizedInput(t *testing.T) {
	resolver := NewApprovalResolver(config.Config{
		Approval: config.ApprovalConfig{
			Default: config.ApprovalModePrompt,
		},
	})
	policy := NewPathPolicy("/repo", config.PathsConfig{ProjectRootOnly: true})
	def := ToolDef{Name: "bash", Timeout: 10 * time.Second}

	preview, err := resolver.PreviewFor(def, map[string]any{
		"command": "echo hello",
		"cwd":     "subdir",
	}, policy)
	if err != nil {
		t.Fatalf("PreviewFor() error = %v", err)
	}
	if got, want := preview.Tool, "bash"; got != want {
		t.Fatalf("Tool = %q, want %q", got, want)
	}
	if got, want := preview.Mode, config.ApprovalModePrompt; got != want {
		t.Fatalf("Mode = %q, want %q", got, want)
	}
	if got, want := preview.WorkDir, "/repo"; got != want {
		t.Fatalf("WorkDir = %q, want %q", got, want)
	}
	if got, want := preview.Timeout, 10*time.Second; got != want {
		t.Fatalf("Timeout = %v, want %v", got, want)
	}
	if len(preview.Fields) != 2 {
		t.Fatalf("Fields len = %d, want 2", len(preview.Fields))
	}
	if got, want := preview.Fields[0].Name, "cwd"; got != want {
		t.Fatalf("Fields[0].Name = %q, want %q", got, want)
	}
	if got, want := preview.Fields[0].Value, "/repo/subdir"; got != want {
		t.Fatalf("Fields[0].Value = %q, want %q", got, want)
	}
	if got, want := preview.Fields[1].Name, "command"; got != want {
		t.Fatalf("Fields[1].Name = %q, want %q", got, want)
	}
	if got, want := preview.Fields[1].Value, "echo hello"; got != want {
		t.Fatalf("Fields[1].Value = %q, want %q", got, want)
	}
}

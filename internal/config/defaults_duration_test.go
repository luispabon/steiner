package config

import "testing"

// TestDefaultConfigDurationLiterals builds every default that uses a
// MustDuration literal (defaultConfig, NewModelConfigBase, advisorTimeout), so
// a malformed literal in defaults.go fails this test instead of panicking at
// runtime. Each duration that defaults to a non-zero value must parse to one.
func TestDefaultConfigDurationLiterals(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("building defaults panicked: %v", r)
		}
	}()

	cfg := defaultConfig(nil)
	model := NewModelConfigBase()

	tests := []struct {
		name string
		got  Duration
	}{
		{"TUI.QuotaRefreshInterval", cfg.TUI.QuotaRefreshInterval},
		{"Limits.ModelCallTimeout", cfg.Limits.ModelCallTimeout},
		{"Limits.ToolTimeoutDefault", cfg.Limits.ToolTimeoutDefault},
		{"Limits.ToolTimeouts[bash]", cfg.Limits.ToolTimeouts["bash"]},
		{"Limits.ToolTimeouts[read]", cfg.Limits.ToolTimeouts["read"]},
		{"Limits.ToolTimeouts[grep]", cfg.Limits.ToolTimeouts["grep"]},
		{"Limits.ToolTimeouts[ls]", cfg.Limits.ToolTimeouts["ls"]},
		{"LSP.IdleTimeout", cfg.LSP.IdleTimeout},
		{"LSP.RequestTimeout", cfg.LSP.RequestTimeout},
		{"LSP.ReadyTimeout", cfg.LSP.ReadyTimeout},
		{"LSP.ReadyGracePeriod", cfg.LSP.ReadyGracePeriod},
		{"LSP.DiagnosticsWindow", cfg.LSP.DiagnosticsWindow},
		{"NewModelConfigBase().Retry.InitialBackoff", model.Retry.InitialBackoff},
		{"NewModelConfigBase().Retry.MaxBackoff", model.Retry.MaxBackoff},
		{"NewModelConfigBase().Retry.RetryAfterMax", model.Retry.RetryAfterMax},
		{"Advisor.Timeout", *cfg.Advisor.Timeout},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got.IsZero() {
				t.Errorf("%s is zero, want a non-zero default duration", tt.name)
			}
		})
	}
}

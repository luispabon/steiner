package config

import "testing"

func TestApplyCLIOverrides(t *testing.T) {
	cfg := defaultConfig()
	cfg.Logging.Level = "info"
	cfg.Sandbox.Enabled = true

	if err := applyCLIOverrides(&cfg, CLIOverrides{
		Model:   "default",
		Verbose: true,
		Unsafe:  true,
	}); err != nil {
		t.Fatalf("applyCLIOverrides() error = %v", err)
	}

	if got := cfg.Selection.ModelOverride; got != "default" {
		t.Fatalf("default_model = %q, want %q", got, "default")
	}
	if got := cfg.Logging.Level; got != "debug" {
		t.Fatalf("logging.level = %q, want %q", got, "debug")
	}
	if got := cfg.Sandbox.Enabled; got {
		t.Fatalf("sandbox.enabled = %v, want false", got)
	}
}

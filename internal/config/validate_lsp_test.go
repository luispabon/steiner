package config

import (
	"strings"
	"testing"
)

func TestValidateLSPDisabledIsValid(t *testing.T) {
	cfg := LSPConfig{
		Enabled: false,
	}
	if err := validateLSP(cfg); err != nil {
		t.Fatalf("validateLSP() error = %v, want nil", err)
	}
}

func TestValidateLSPDisabledIgnoresInvalidServers(t *testing.T) {
	cfg := LSPConfig{
		Enabled: false,
		Servers: map[string]LSPServerConfig{
			"broken": {
				Enabled:        true,
				Command:        "",
				FileExtensions: []string{},
				RootMarkers:    []string{},
			},
		},
	}
	if err := validateLSP(cfg); err != nil {
		t.Fatalf("validateLSP() error = %v, want nil (disabled config should skip validation)", err)
	}
}

func TestValidateLSPEnabledNoServers(t *testing.T) {
	cfg := LSPConfig{
		Enabled:           true,
		IdleTimeout:       MustDuration("5m"),
		RequestTimeout:    MustDuration("10s"),
		ReadyTimeout:      MustDuration("30s"),
		ReadyGracePeriod:  MustDuration("2s"),
		DiagnosticsWindow: MustDuration("3s"),
		MaxResults:        200,
	}
	if err := validateLSP(cfg); err != nil {
		t.Fatalf("validateLSP() error = %v, want nil", err)
	}
}

func TestValidateLSPValidEnabledServer(t *testing.T) {
	cfg := LSPConfig{
		Enabled:           true,
		IdleTimeout:       MustDuration("5m"),
		RequestTimeout:    MustDuration("10s"),
		ReadyTimeout:      MustDuration("30s"),
		ReadyGracePeriod:  MustDuration("2s"),
		DiagnosticsWindow: MustDuration("3s"),
		MaxResults:        200,
		Servers: map[string]LSPServerConfig{
			"go": {
				Enabled:        true,
				Command:        "gopls",
				Args:           []string{},
				FileExtensions: []string{".go"},
				RootMarkers:    []string{"go.mod"},
			},
		},
	}
	if err := validateLSP(cfg); err != nil {
		t.Fatalf("validateLSP() error = %v, want nil", err)
	}
}

func TestValidateLSPEmptyServerName(t *testing.T) {
	cfg := LSPConfig{
		Enabled:           true,
		IdleTimeout:       MustDuration("5m"),
		RequestTimeout:    MustDuration("10s"),
		ReadyTimeout:      MustDuration("30s"),
		ReadyGracePeriod:  MustDuration("2s"),
		DiagnosticsWindow: MustDuration("3s"),
		MaxResults:        200,
		Servers: map[string]LSPServerConfig{
			"": {
				Enabled:        true,
				Command:        "gopls",
				FileExtensions: []string{".go"},
				RootMarkers:    []string{"go.mod"},
			},
		},
	}
	err := validateLSP(cfg)
	if err == nil {
		t.Fatal("validateLSP() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "empty server name") {
		t.Fatalf("validateLSP() error = %v, want error containing empty server name", err)
	}
}

func TestValidateLSPEnabledServerMissingCommand(t *testing.T) {
	cfg := LSPConfig{
		Enabled:           true,
		IdleTimeout:       MustDuration("5m"),
		RequestTimeout:    MustDuration("10s"),
		ReadyTimeout:      MustDuration("30s"),
		ReadyGracePeriod:  MustDuration("2s"),
		DiagnosticsWindow: MustDuration("3s"),
		MaxResults:        200,
		Servers: map[string]LSPServerConfig{
			"go": {
				Enabled:        true,
				Command:        "",
				FileExtensions: []string{".go"},
				RootMarkers:    []string{"go.mod"},
			},
		},
	}
	err := validateLSP(cfg)
	if err == nil {
		t.Fatal("validateLSP() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "command is required") {
		t.Fatalf("validateLSP() error = %v, want error containing command is required", err)
	}
}

func TestValidateLSPEmptyFileExtensions(t *testing.T) {
	cfg := LSPConfig{
		Enabled:           true,
		IdleTimeout:       MustDuration("5m"),
		RequestTimeout:    MustDuration("10s"),
		ReadyTimeout:      MustDuration("30s"),
		ReadyGracePeriod:  MustDuration("2s"),
		DiagnosticsWindow: MustDuration("3s"),
		MaxResults:        200,
		Servers: map[string]LSPServerConfig{
			"go": {
				Enabled:        true,
				Command:        "gopls",
				FileExtensions: []string{},
				RootMarkers:    []string{"go.mod"},
			},
		},
	}
	err := validateLSP(cfg)
	if err == nil {
		t.Fatal("validateLSP() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "file_extensions must be non-empty") {
		t.Fatalf("validateLSP() error = %v, want error containing file_extensions must be non-empty", err)
	}
}

func TestValidateLSPFileExtensionMissingDot(t *testing.T) {
	cfg := LSPConfig{
		Enabled:           true,
		IdleTimeout:       MustDuration("5m"),
		RequestTimeout:    MustDuration("10s"),
		ReadyTimeout:      MustDuration("30s"),
		ReadyGracePeriod:  MustDuration("2s"),
		DiagnosticsWindow: MustDuration("3s"),
		MaxResults:        200,
		Servers: map[string]LSPServerConfig{
			"go": {
				Enabled:        true,
				Command:        "gopls",
				FileExtensions: []string{"go"},
				RootMarkers:    []string{"go.mod"},
			},
		},
	}
	err := validateLSP(cfg)
	if err == nil {
		t.Fatal("validateLSP() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "must start with") {
		t.Fatalf("validateLSP() error = %v, want error containing must start with", err)
	}
}

func TestValidateLSPEmptyRootMarkers(t *testing.T) {
	cfg := LSPConfig{
		Enabled:           true,
		IdleTimeout:       MustDuration("5m"),
		RequestTimeout:    MustDuration("10s"),
		ReadyTimeout:      MustDuration("30s"),
		ReadyGracePeriod:  MustDuration("2s"),
		DiagnosticsWindow: MustDuration("3s"),
		MaxResults:        200,
		Servers: map[string]LSPServerConfig{
			"go": {
				Enabled:        true,
				Command:        "gopls",
				FileExtensions: []string{".go"},
				RootMarkers:    []string{},
			},
		},
	}
	err := validateLSP(cfg)
	if err == nil {
		t.Fatal("validateLSP() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "root_markers must be non-empty") {
		t.Fatalf("validateLSP() error = %v, want error containing root_markers must be non-empty", err)
	}
}

func TestValidateLSPDisabledServerIsIgnored(t *testing.T) {
	cfg := LSPConfig{
		Enabled:           true,
		IdleTimeout:       MustDuration("5m"),
		RequestTimeout:    MustDuration("10s"),
		ReadyTimeout:      MustDuration("30s"),
		ReadyGracePeriod:  MustDuration("2s"),
		DiagnosticsWindow: MustDuration("3s"),
		MaxResults:        200,
		Servers: map[string]LSPServerConfig{
			"go": {
				Enabled:        false,
				Command:        "",
				FileExtensions: []string{},
				RootMarkers:    []string{},
			},
		},
	}
	if err := validateLSP(cfg); err != nil {
		t.Fatalf("validateLSP() error = %v, want nil (disabled server should be ignored)", err)
	}
}

func TestValidateLSPDuplicateFileExtension(t *testing.T) {
	cfg := LSPConfig{
		Enabled:           true,
		IdleTimeout:       MustDuration("5m"),
		RequestTimeout:    MustDuration("10s"),
		ReadyTimeout:      MustDuration("30s"),
		ReadyGracePeriod:  MustDuration("2s"),
		DiagnosticsWindow: MustDuration("3s"),
		MaxResults:        200,
		Servers: map[string]LSPServerConfig{
			"go": {
				Enabled:        true,
				Command:        "gopls",
				FileExtensions: []string{".go"},
				RootMarkers:    []string{"go.mod"},
			},
			"go-alt": {
				Enabled:        true,
				Command:        "gopls-alt",
				FileExtensions: []string{".go"},
				RootMarkers:    []string{"go.mod"},
			},
		},
	}
	err := validateLSP(cfg)
	if err == nil {
		t.Fatal("validateLSP() error = nil, want non-nil")
	}
	errMsg := err.Error()
	if !strings.Contains(errMsg, ".go") {
		t.Fatalf("validateLSP() error = %v, want error containing .go", errMsg)
	}
	if !strings.Contains(errMsg, "go") || !strings.Contains(errMsg, "go-alt") {
		t.Fatalf("validateLSP() error = %v, want error naming both servers", errMsg)
	}
}

func TestValidateLSPNonPositiveIdleTimeout(t *testing.T) {
	cfg := LSPConfig{
		Enabled:           true,
		IdleTimeout:       MustDuration("0s"),
		RequestTimeout:    MustDuration("10s"),
		ReadyTimeout:      MustDuration("30s"),
		ReadyGracePeriod:  MustDuration("2s"),
		DiagnosticsWindow: MustDuration("3s"),
		MaxResults:        200,
	}
	err := validateLSP(cfg)
	if err == nil {
		t.Fatal("validateLSP() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "idle_timeout") {
		t.Fatalf("validateLSP() error = %v, want error containing idle_timeout", err)
	}
}

func TestValidateLSPNonPositiveRequestTimeout(t *testing.T) {
	cfg := LSPConfig{
		Enabled:           true,
		IdleTimeout:       MustDuration("5m"),
		RequestTimeout:    MustDuration("-1s"),
		ReadyTimeout:      MustDuration("30s"),
		ReadyGracePeriod:  MustDuration("2s"),
		DiagnosticsWindow: MustDuration("3s"),
		MaxResults:        200,
	}
	err := validateLSP(cfg)
	if err == nil {
		t.Fatal("validateLSP() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "request_timeout") {
		t.Fatalf("validateLSP() error = %v, want error containing request_timeout", err)
	}
}

func TestValidateLSPNonPositiveReadyTimeout(t *testing.T) {
	cfg := LSPConfig{
		Enabled:           true,
		IdleTimeout:       MustDuration("5m"),
		RequestTimeout:    MustDuration("10s"),
		ReadyTimeout:      MustDuration("0s"),
		ReadyGracePeriod:  MustDuration("2s"),
		DiagnosticsWindow: MustDuration("3s"),
		MaxResults:        200,
	}
	err := validateLSP(cfg)
	if err == nil {
		t.Fatal("validateLSP() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "ready_timeout") {
		t.Fatalf("validateLSP() error = %v, want error containing ready_timeout", err)
	}
}

func TestValidateLSPNonPositiveReadyGracePeriod(t *testing.T) {
	cfg := LSPConfig{
		Enabled:           true,
		IdleTimeout:       MustDuration("5m"),
		RequestTimeout:    MustDuration("10s"),
		ReadyTimeout:      MustDuration("30s"),
		ReadyGracePeriod:  MustDuration("-1s"),
		DiagnosticsWindow: MustDuration("3s"),
		MaxResults:        200,
	}
	err := validateLSP(cfg)
	if err == nil {
		t.Fatal("validateLSP() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "ready_grace_period") {
		t.Fatalf("validateLSP() error = %v, want error containing ready_grace_period", err)
	}
}

func TestValidateLSPNonPositiveDiagnosticsWindow(t *testing.T) {
	cfg := LSPConfig{
		Enabled:           true,
		IdleTimeout:       MustDuration("5m"),
		RequestTimeout:    MustDuration("10s"),
		ReadyTimeout:      MustDuration("30s"),
		ReadyGracePeriod:  MustDuration("2s"),
		DiagnosticsWindow: MustDuration("0s"),
		MaxResults:        200,
	}
	err := validateLSP(cfg)
	if err == nil {
		t.Fatal("validateLSP() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "diagnostics_window") {
		t.Fatalf("validateLSP() error = %v, want error containing diagnostics_window", err)
	}
}

func TestValidateLSPZeroMaxResults(t *testing.T) {
	cfg := LSPConfig{
		Enabled:           true,
		IdleTimeout:       MustDuration("5m"),
		RequestTimeout:    MustDuration("10s"),
		ReadyTimeout:      MustDuration("30s"),
		ReadyGracePeriod:  MustDuration("2s"),
		DiagnosticsWindow: MustDuration("3s"),
		MaxResults:        0,
	}
	err := validateLSP(cfg)
	if err == nil {
		t.Fatal("validateLSP() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "max_results") {
		t.Fatalf("validateLSP() error = %v, want error containing max_results", err)
	}
}

func TestValidateLSPNegativeMaxResults(t *testing.T) {
	cfg := LSPConfig{
		Enabled:           true,
		IdleTimeout:       MustDuration("5m"),
		RequestTimeout:    MustDuration("10s"),
		ReadyTimeout:      MustDuration("30s"),
		ReadyGracePeriod:  MustDuration("2s"),
		DiagnosticsWindow: MustDuration("3s"),
		MaxResults:        -1,
	}
	err := validateLSP(cfg)
	if err == nil {
		t.Fatal("validateLSP() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "max_results") {
		t.Fatalf("validateLSP() error = %v, want error containing max_results", err)
	}
}

func TestValidateLSPMultipleExtensions(t *testing.T) {
	cfg := LSPConfig{
		Enabled:           true,
		IdleTimeout:       MustDuration("5m"),
		RequestTimeout:    MustDuration("10s"),
		ReadyTimeout:      MustDuration("30s"),
		ReadyGracePeriod:  MustDuration("2s"),
		DiagnosticsWindow: MustDuration("3s"),
		MaxResults:        200,
		Servers: map[string]LSPServerConfig{
			"go": {
				Enabled:        true,
				Command:        "gopls",
				FileExtensions: []string{".go", ".mod", ".sum"},
				RootMarkers:    []string{"go.mod"},
			},
		},
	}
	if err := validateLSP(cfg); err != nil {
		t.Fatalf("validateLSP() error = %v, want nil", err)
	}
}

func TestValidateLSPMultipleValidServers(t *testing.T) {
	cfg := LSPConfig{
		Enabled:           true,
		IdleTimeout:       MustDuration("5m"),
		RequestTimeout:    MustDuration("10s"),
		ReadyTimeout:      MustDuration("30s"),
		ReadyGracePeriod:  MustDuration("2s"),
		DiagnosticsWindow: MustDuration("3s"),
		MaxResults:        200,
		Servers: map[string]LSPServerConfig{
			"go": {
				Enabled:        true,
				Command:        "gopls",
				FileExtensions: []string{".go"},
				RootMarkers:    []string{"go.mod"},
			},
			"python": {
				Enabled:        true,
				Command:        "pylsp",
				FileExtensions: []string{".py"},
				RootMarkers:    []string{"pyproject.toml", "setup.py"},
			},
		},
	}
	if err := validateLSP(cfg); err != nil {
		t.Fatalf("validateLSP() error = %v, want nil", err)
	}
}

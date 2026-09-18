package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// F116: validateLimitsConfig must reject negative tool timeouts.
func TestValidateLimitsConfigRejectsNegativeToolTimeouts(t *testing.T) {
	tests := []struct {
		name    string
		cfg     LimitsConfig
		wantErr string
	}{
		{
			name: "positive tool_timeout_default passes",
			cfg: LimitsConfig{
				MaxTurns:           10,
				MaxTokens:          100000,
				ToolTimeoutDefault: MustDuration("30s"),
				ToolOutputMaxBytes: 65536,
				MaxParallelTools:   1,
			},
		},
		{
			name: "zero tool_timeout_default rejected",
			cfg: LimitsConfig{
				MaxTurns:           10,
				MaxTokens:          100000,
				ToolTimeoutDefault: MustDuration("0s"),
				ToolOutputMaxBytes: 65536,
				MaxParallelTools:   1,
			},
			wantErr: "limits.tool_timeout_default must be greater than zero",
		},
		{
			name: "negative tool_timeout_default rejected",
			cfg: LimitsConfig{
				MaxTurns:           10,
				MaxTokens:          100000,
				ToolTimeoutDefault: MustDuration("-5s"),
				ToolOutputMaxBytes: 65536,
				MaxParallelTools:   1,
			},
			wantErr: "limits.tool_timeout_default must be greater than zero",
		},
		{
			name: "positive tool_timeout entry passes",
			cfg: LimitsConfig{
				MaxTurns:           10,
				MaxTokens:          100000,
				ToolTimeoutDefault: MustDuration("30s"),
				ToolTimeouts: map[string]Duration{
					"bash": MustDuration("120s"),
				},
				ToolOutputMaxBytes: 65536,
				MaxParallelTools:   1,
			},
		},
		{
			name: "zero tool_timeout entry rejected",
			cfg: LimitsConfig{
				MaxTurns:           10,
				MaxTokens:          100000,
				ToolTimeoutDefault: MustDuration("30s"),
				ToolTimeouts: map[string]Duration{
					"bash": MustDuration("0s"),
				},
				ToolOutputMaxBytes: 65536,
				MaxParallelTools:   1,
			},
			wantErr: `limits.tool_timeouts["bash"] must be greater than zero`,
		},
		{
			name: "negative tool_timeout entry rejected",
			cfg: LimitsConfig{
				MaxTurns:           10,
				MaxTokens:          100000,
				ToolTimeoutDefault: MustDuration("30s"),
				ToolTimeouts: map[string]Duration{
					"bash": MustDuration("-10s"),
				},
				ToolOutputMaxBytes: 65536,
				MaxParallelTools:   1,
			},
			wantErr: `limits.tool_timeouts["bash"] must be greater than zero`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var problems []string
			validateLimitsConfig(&problems, tt.cfg)
			joined := strings.Join(problems, "; ")
			if tt.wantErr == "" {
				if len(problems) != 0 {
					t.Fatalf("validateLimitsConfig() problems = %q, want none", joined)
				}
				return
			}
			if !strings.Contains(joined, tt.wantErr) {
				t.Fatalf("validateLimitsConfig() problems = %q, want substring %q", joined, tt.wantErr)
			}
		})
	}
}

// F117: appendRetryProblems must reject negative retry durations.
func TestAppendRetryProblemsRejectsNegativeDurations(t *testing.T) {
	tests := []struct {
		name    string
		retry   RetryConfig
		wantErr string
	}{
		{
			name: "all positive durations pass",
			retry: RetryConfig{
				Enabled:        true,
				MaxAttempts:    3,
				InitialBackoff: MustDuration("100ms"),
				MaxBackoff:     MustDuration("5s"),
				RetryAfterMax:  MustDuration("60s"),
			},
		},
		{
			name: "negative initial_backoff rejected",
			retry: RetryConfig{
				Enabled:        true,
				MaxAttempts:    3,
				InitialBackoff: MustDuration("-100ms"),
				MaxBackoff:     MustDuration("5s"),
				RetryAfterMax:  MustDuration("60s"),
			},
			wantErr: "initial_backoff must be greater than zero",
		},
		{
			name: "zero initial_backoff rejected",
			retry: RetryConfig{
				Enabled:        true,
				MaxAttempts:    3,
				InitialBackoff: MustDuration("0s"),
				MaxBackoff:     MustDuration("5s"),
				RetryAfterMax:  MustDuration("60s"),
			},
			wantErr: "initial_backoff must be greater than zero",
		},
		{
			name: "negative max_backoff rejected",
			retry: RetryConfig{
				Enabled:        true,
				MaxAttempts:    3,
				InitialBackoff: MustDuration("100ms"),
				MaxBackoff:     MustDuration("-5s"),
				RetryAfterMax:  MustDuration("60s"),
			},
			wantErr: "max_backoff must be greater than zero",
		},
		{
			name: "zero max_backoff rejected",
			retry: RetryConfig{
				Enabled:        true,
				MaxAttempts:    3,
				InitialBackoff: MustDuration("100ms"),
				MaxBackoff:     MustDuration("0s"),
				RetryAfterMax:  MustDuration("60s"),
			},
			wantErr: "max_backoff must be greater than zero",
		},
		{
			name: "negative retry_after_max rejected",
			retry: RetryConfig{
				Enabled:        true,
				MaxAttempts:    3,
				InitialBackoff: MustDuration("100ms"),
				MaxBackoff:     MustDuration("5s"),
				RetryAfterMax:  MustDuration("-60s"),
			},
			wantErr: "retry_after_max must be greater than zero",
		},
		{
			name: "zero retry_after_max rejected",
			retry: RetryConfig{
				Enabled:        true,
				MaxAttempts:    3,
				InitialBackoff: MustDuration("100ms"),
				MaxBackoff:     MustDuration("5s"),
				RetryAfterMax:  MustDuration("0s"),
			},
			wantErr: "retry_after_max must be greater than zero",
		},
		{
			name: "max_backoff < initial_backoff rejected",
			retry: RetryConfig{
				Enabled:        true,
				MaxAttempts:    3,
				InitialBackoff: MustDuration("5s"),
				MaxBackoff:     MustDuration("100ms"),
				RetryAfterMax:  MustDuration("60s"),
			},
			wantErr: "max_backoff must be greater than or equal to",
		},
		{
			name: "retry_after_max < initial_backoff rejected",
			retry: RetryConfig{
				Enabled:        true,
				MaxAttempts:    3,
				InitialBackoff: MustDuration("5s"),
				MaxBackoff:     MustDuration("10s"),
				RetryAfterMax:  MustDuration("100ms"),
			},
			wantErr: "retry_after_max must be greater than or equal to",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var problems []string
			appendRetryProblems(&problems, "retry", tt.retry)
			joined := strings.Join(problems, "; ")
			if tt.wantErr == "" {
				if len(problems) != 0 {
					t.Fatalf("appendRetryProblems() problems = %q, want none", joined)
				}
				return
			}
			if !strings.Contains(joined, tt.wantErr) {
				t.Fatalf("appendRetryProblems() problems = %q, want substring %q", joined, tt.wantErr)
			}
		})
	}
}

// F112: Load must resolve injected environment for default diagnostics dir.
func TestLoadResolvesDiagnosticsDirFromInjectedEnv(t *testing.T) {
	homeDir := t.TempDir()

	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{
			name: "XDG_STATE_HOME injected",
			env: map[string]string{
				"XDG_STATE_HOME": filepath.Join(homeDir, "state"),
				"HOME":           homeDir,
			},
			want: filepath.Join(homeDir, "state", "steiner", "diagnostics"),
		},
		{
			name: "no XDG_STATE_HOME falls back to ~",
			env: map[string]string{
				"HOME": homeDir,
			},
			want: filepath.Join(homeDir, ".local", "state", "steiner", "diagnostics"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := Load(LoadOptions{
				HomeDir: homeDir,
				Env:     tt.env,
			})
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if cfg.Diagnostics.Dir != tt.want {
				t.Errorf("Diagnostics.Dir = %q, want %q", cfg.Diagnostics.Dir, tt.want)
			}
		})
	}
}

// F114 + F115: Load must expand home paths and support compaction_log_file in config.
func TestLoadExpandsPathsAndCompactionLogFile(t *testing.T) {
	homeDir := t.TempDir()
	configDir := filepath.Join(homeDir, ".config", "steiner")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	configPath := filepath.Join(configDir, "steiner.yaml")
	configContent := `
logging:
  file: ~/logs/steiner.log
  compaction_log_file: ~/logs/compaction.log
lsp:
  cache_dir: ~/cache/lsp
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(LoadOptions{
		GlobalConfigPath: configPath,
		HomeDir:          homeDir,
		Env: map[string]string{
			"HOME": homeDir,
		},
	})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	expectedLoggingFile := filepath.Join(homeDir, "logs", "steiner.log")
	if cfg.Logging.File != expectedLoggingFile {
		t.Errorf("Logging.File = %q, want %q", cfg.Logging.File, expectedLoggingFile)
	}

	expectedCompactionLogFile := filepath.Join(homeDir, "logs", "compaction.log")
	if cfg.Logging.CompactionLogFile != expectedCompactionLogFile {
		t.Errorf("Logging.CompactionLogFile = %q, want %q", cfg.Logging.CompactionLogFile, expectedCompactionLogFile)
	}

	expectedCacheDir := filepath.Join(homeDir, "cache", "lsp")
	if cfg.LSP.CacheDir != expectedCacheDir {
		t.Errorf("LSP.CacheDir = %q, want %q", cfg.LSP.CacheDir, expectedCacheDir)
	}
}

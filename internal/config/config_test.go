package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestDefaultConfigProjectContextFilesDefaultToNil(t *testing.T) {
	cfg := defaultConfig()

	if !reflect.DeepEqual(cfg.ProjectContext.ExtraFiles, []string(nil)) {
		t.Fatalf("project_context.extra_files = %#v, want nil", cfg.ProjectContext.ExtraFiles)
	}
	if !reflect.DeepEqual(cfg.ProjectContext.IgnoreFiles, []string(nil)) {
		t.Fatalf("project_context.ignore_files = %#v, want nil", cfg.ProjectContext.IgnoreFiles)
	}
}

func TestDefaultConfigRetryDefaults(t *testing.T) {
	cfg := defaultConfig()

	want := RetryConfig{
		Enabled:        true,
		MaxAttempts:    5,
		InitialBackoff: MustDuration("250ms"),
		MaxBackoff:     MustDuration("5s"),
		RetryAfterMax:  MustDuration("60s"),
	}
	if !reflect.DeepEqual(cfg.Models.Definitions["default"].Retry, want) {
		t.Fatalf("models[default].retry = %#v, want %#v", cfg.Models.Definitions["default"].Retry, want)
	}
}

func TestDefaultConfigThinkingChunkDefaultsToFalse(t *testing.T) {
	cfg := defaultConfig()
	if cfg.Logging.ThinkingChunk {
		t.Fatal("default logging.thinking_chunk = true, want false")
	}
}

func TestDefaultConfigReadAnnotationsDefaultsToTrue(t *testing.T) {
	cfg := defaultConfig()
	if !cfg.ContextManagement.ReadAnnotations {
		t.Fatal("context_management.read_annotations = false, want true")
	}
}

func TestDefaultConfigWorkflowHandoffDefaultsToEmpty(t *testing.T) {
	cfg := defaultConfig()
	if len(cfg.Models.WorkflowHandoff) != 0 {
		t.Fatalf("workflow_handoff.models = %#v, want empty", cfg.Models.WorkflowHandoff)
	}
}

func TestDefaultConfigDesktopNotificationsDefaultsToFalseAndZero(t *testing.T) {
	cfg := defaultConfig()
	if cfg.DesktopNotifications.Enabled {
		t.Fatal("desktop_notifications.enabled = true, want false")
	}
	if cfg.DesktopNotifications.Duration != 0 {
		t.Fatalf("desktop_notifications.duration = %d, want 0", cfg.DesktopNotifications.Duration)
	}
}

func TestDefaultConfigOneShotDefaultsToEmpty(t *testing.T) {
	cfg := defaultConfig()
	if len(cfg.Models.OneShot) != 0 {
		t.Fatalf("oneshot.models = %#v, want empty", cfg.Models.OneShot)
	}
	if cfg.OneShot.AutoPR {
		t.Fatal("oneshot.auto_pr = true, want false")
	}
}

func TestDefaultConfigAdvisorDisabledByDefault(t *testing.T) {
	cfg := defaultConfig()
	if cfg.Advisor.Enabled {
		t.Fatal("advisor.enabled = true, want false")
	}
	if cfg.Models.Advisor != "" {
		t.Fatalf("advisor.model = %q, want empty", cfg.Models.Advisor)
	}
	if cfg.Advisor.MaxUsesPerRun != 3 {
		t.Fatalf("advisor.max_uses_per_run = %d, want 3", cfg.Advisor.MaxUsesPerRun)
	}
	if cfg.Advisor.MaxTokens != nil {
		t.Fatalf("advisor.max_tokens = %#v, want nil", cfg.Advisor.MaxTokens)
	}
	if cfg.Advisor.Timeout == nil {
		t.Fatal("advisor.timeout = nil, want 180s default")
	} else if got, want := cfg.Advisor.Timeout.Duration(), int64(180*1_000_000_000); got != want {
		t.Fatalf("advisor.timeout = %d ns, want %d ns", got, want)
	}
}

func TestAdvisorConfigPatchAndYAMLParsing(t *testing.T) {
	patch, err := parseConfigPatch("test.yaml", `advisor:
  enabled: true
  max_uses_per_run: 2
  max_tokens: 512
  timeout: 90s
models:
  advisor: advisor-model
`)
	if err != nil {
		t.Fatalf("parseConfigPatch() error = %v", err)
	}
	if patch.Advisor == nil {
		t.Fatal("patch.Advisor = nil, want parsed advisor patch")
	}

	dst := AdvisorConfig{}
	applyAdvisorPatch(&dst, patch.Advisor)

	want := AdvisorConfig{
		Enabled:       true,
		MaxUsesPerRun: 2,
		MaxTokens:     intPtr(512),
		Timeout:       durationPtr(MustDuration("90s")),
	}
	if !reflect.DeepEqual(dst, want) {
		t.Fatalf("applyAdvisorPatch() = %#v, want %#v", dst, want)
	}

	if patch.Models == nil || patch.Models.Advisor == nil {
		t.Fatal("patch.Models.Advisor = nil, want parsed models.advisor")
	}
	if got, want := *patch.Models.Advisor, "advisor-model"; got != want {
		t.Fatalf("patch.Models.Advisor = %q, want %q", got, want)
	}
}

func TestOneShotConfigPatchAndYAMLParsing(t *testing.T) {
	patch, err := parseConfigPatch("test.yaml", `oneshot:
  auto_pr: true
models:
  oneshot:
    plan: planner-model
    implement: implement-model
    review: review-model
`)
	if err != nil {
		t.Fatalf("parseConfigPatch() error = %v", err)
	}
	if patch.OneShot == nil {
		t.Fatal("patch.OneShot = nil, want parsed oneshot patch")
	}

	dst := oneshotConfig{}
	applyOneShotPatch(&dst, patch.OneShot)

	want := oneshotConfig{
		AutoPR: true,
	}
	if !reflect.DeepEqual(dst, want) {
		t.Fatalf("applyOneShotPatch() = %#v, want %#v", dst, want)
	}

	if patch.Models == nil || patch.Models.OneShot == nil {
		t.Fatal("patch.Models.OneShot = nil, want parsed models.oneshot")
	}
	wantModels := map[string]string{
		"plan":      "planner-model",
		"implement": "implement-model",
		"review":    "review-model",
	}
	if !reflect.DeepEqual(*patch.Models.OneShot, wantModels) {
		t.Fatalf("patch.Models.OneShot = %#v, want %#v", *patch.Models.OneShot, wantModels)
	}
}

func TestApplyAdvisorPatch(t *testing.T) {
	tests := []struct {
		name    string
		initial AdvisorConfig
		patch   advisorPatch
		want    AdvisorConfig
	}{
		{
			name:    "sets advisor fields",
			initial: AdvisorConfig{},
			patch: advisorPatch{
				Enabled:       boolPtr(true),
				MaxUsesPerRun: intPtr(3),
				MaxTokens:     intPtr(256),
				Timeout:       durationPtr(MustDuration("45s")),
			},
			want: AdvisorConfig{
				Enabled:       true,
				MaxUsesPerRun: 3,
				MaxTokens:     intPtr(256),
				Timeout:       durationPtr(MustDuration("45s")),
			},
		},
		{
			name: "nil fields leave values untouched",
			initial: AdvisorConfig{
				Enabled:       true,
				MaxUsesPerRun: 1,
				MaxTokens:     intPtr(128),
				Timeout:       durationPtr(MustDuration("30s")),
			},
			patch: advisorPatch{},
			want: AdvisorConfig{
				Enabled:       true,
				MaxUsesPerRun: 1,
				MaxTokens:     intPtr(128),
				Timeout:       durationPtr(MustDuration("30s")),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dst := tt.initial
			applyAdvisorPatch(&dst, &tt.patch)
			if !reflect.DeepEqual(dst, tt.want) {
				t.Fatalf("applyAdvisorPatch() = %#v, want %#v", dst, tt.want)
			}
		})
	}
}

func TestLoadPrecedence(t *testing.T) {
	tempDir := t.TempDir()

	homeDir := filepath.Join(tempDir, "home")
	projectDir := filepath.Join(tempDir, "project")
	globalDir := filepath.Join(homeDir, ".config", "steiner")
	projectConfigDir := filepath.Join(projectDir, ".steiner")

	mustMkdirAll(t, globalDir)
	mustMkdirAll(t, projectConfigDir)

	writeFile(t, filepath.Join(globalDir, "config.yaml"), `scheduler:
  parallelism: 2
providers:
  local:
    type: openai_compat
    base_url: http://global.example/v1
models:
  default: global
  definitions:
    global:
      provider: local
      id: global-backend
      retry:
        enabled: true
        max_attempts: 3
        initial_backoff: 250ms
        max_backoff: 5s
        retry_after_max: 30s
    env:
      provider: local
      id: env-backend
      retry:
        enabled: true
        max_attempts: 3
        initial_backoff: 250ms
        max_backoff: 5s
        retry_after_max: 30s
limits:
  max_turns: 25
paths:
  project_root_only: false
`)

	writeFile(t, filepath.Join(projectConfigDir, "config.yaml"), `providers:
  local:
    type: openai_compat
    base_url: http://project.example/v1
models:
  default: project
  definitions:
    project:
      provider: local
      id: project-backend
      retry:
        enabled: true
        max_attempts: 3
        initial_backoff: 250ms
        max_backoff: 5s
        retry_after_max: 30s
    cli:
      provider: local
      id: cli-backend
      retry:
        enabled: true
        max_attempts: 3
        initial_backoff: 250ms
        max_backoff: 5s
        retry_after_max: 30s
limits:
  max_turns: 10
logging:
  level: warn
`)

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(cwd)
	})
	if err := os.Chdir(projectDir); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(LoadOptions{
		HomeDir: homeDir,
		Env: map[string]string{
			"STEINER_MODEL":                 "env",
			"STEINER_MAX_TURNS":             "77",
			"STEINER_LOG_LEVEL":             "trace",
			"STEINER_SCHEDULER_PARALLELISM": "4",
		},
		CLI: CLIOverrides{
			Model:   "cli",
			Verbose: true,
		},
	})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if got := cfg.Models.Default; got != "cli" {
		t.Fatalf("default_model = %q, want %q", got, "cli")
	}
	if got := cfg.Models.Definitions["cli"].ID; got != "cli-backend" {
		t.Fatalf("models[cli].id = %q, want %q", got, "cli-backend")
	}
	if got := cfg.Scheduler.Parallelism; got != 4 {
		t.Fatalf("scheduler.parallelism = %d, want %d", got, 4)
	}
	if got := cfg.Limits.MaxTurns; got != 77 {
		t.Fatalf("limits.max_turns = %d, want %d", got, 77)
	}
	if got := cfg.Logging.Level; got != "debug" {
		t.Fatalf("logging.level = %q, want %q", got, "debug")
	}
	if got := cfg.Paths.ProjectRootOnly; got {
		t.Fatalf("paths.project_root_only = %v, want false", got)
	}
	if got := cfg.Models.Definitions["global"].ID; got != "global-backend" {
		t.Fatalf("models[global].id = %q, want global config", got)
	}
	if got := cfg.Models.Definitions["env"].ID; got != "env-backend" {
		t.Fatalf("models[env].id = %q, want env config", got)
	}
}

func TestLoadAppliesRetryConfigFromModelBlocks(t *testing.T) {
	tempDir := t.TempDir()
	projectDir := filepath.Join(tempDir, "project")
	projectConfigDir := filepath.Join(projectDir, ".steiner")
	mustMkdirAll(t, projectConfigDir)

	writeFile(t, filepath.Join(projectConfigDir, "config.yaml"), `providers:
  local:
    type: openai_compat
    base_url: http://localhost:11434/v1
models:
  default: default
  definitions:
    default:
      provider: local
      id: qwen3-35b-a3b
      retry:
        enabled: false
        max_attempts: 5
        initial_backoff: 500ms
        max_backoff: 4s
        retry_after_max: 45s
    custom:
      provider: local
      id: custom-backend
      retry:
        enabled: true
        max_attempts: 9
        initial_backoff: 250ms
        max_backoff: 5s
        retry_after_max: 30s
`)

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(cwd)
	})
	if err := os.Chdir(projectDir); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(LoadOptions{
		HomeDir: filepath.Join(tempDir, "home"),
		Env:     map[string]string{},
	})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if got, want := cfg.Models.Definitions["default"].Retry, (RetryConfig{
		Enabled:        false,
		MaxAttempts:    5,
		InitialBackoff: MustDuration("500ms"),
		MaxBackoff:     MustDuration("4s"),
		RetryAfterMax:  MustDuration("45s"),
	}); !reflect.DeepEqual(got, want) {
		t.Fatalf("models[default].retry = %#v, want %#v", got, want)
	}
	if got, want := cfg.Models.Definitions["custom"].Retry, (RetryConfig{
		Enabled:        true,
		MaxAttempts:    9,
		InitialBackoff: MustDuration("250ms"),
		MaxBackoff:     MustDuration("5s"),
		RetryAfterMax:  MustDuration("30s"),
	}); !reflect.DeepEqual(got, want) {
		t.Fatalf("models[custom].retry = %#v, want %#v", got, want)
	}
}

func TestLoadAppliesWorkflowHandoffModelOverrides(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, "home")
	projectDir := filepath.Join(tempDir, "project")
	globalDir := filepath.Join(homeDir, ".config", "steiner")
	projectConfigDir := filepath.Join(projectDir, ".steiner")

	mustMkdirAll(t, globalDir)
	mustMkdirAll(t, projectConfigDir)

	writeFile(t, filepath.Join(globalDir, "config.yaml"), `providers:
  local:
    type: openai_compat
    base_url: http://localhost:11434/v1
models:
  default: default
  definitions:
    default:
      provider: local
      id: qwen3-35b-a3b
      retry:
        enabled: true
        max_attempts: 3
        initial_backoff: 250ms
        max_backoff: 5s
        retry_after_max: 30s
    implement:
      provider: local
      id: implement-model
      retry:
        enabled: true
        max_attempts: 3
        initial_backoff: 250ms
        max_backoff: 5s
        retry_after_max: 30s
  workflow_handoff:
    implement: implement
`)

	writeFile(t, filepath.Join(projectConfigDir, "config.yaml"), `models:
  definitions:
    review:
      provider: local
      id: review-model
      retry:
        enabled: true
        max_attempts: 3
        initial_backoff: 250ms
        max_backoff: 5s
        retry_after_max: 30s
  workflow_handoff:
    review: review
`)

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(cwd)
	})
	if err := os.Chdir(projectDir); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(LoadOptions{
		HomeDir: homeDir,
		Env:     map[string]string{},
	})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if got, want := cfg.Models.WorkflowHandoff["implement"], "implement"; got != want {
		t.Fatalf("workflow_handoff.models[implement] = %q, want %q", got, want)
	}
	if got, want := cfg.Models.WorkflowHandoff["review"], "review"; got != want {
		t.Fatalf("workflow_handoff.models[review] = %q, want %q", got, want)
	}
}

func TestLoadNewModelAliasesInheritRetryDefaults(t *testing.T) {
	tempDir := t.TempDir()
	projectDir := filepath.Join(tempDir, "project")
	projectConfigDir := filepath.Join(projectDir, ".steiner")
	mustMkdirAll(t, projectConfigDir)

	writeFile(t, filepath.Join(projectConfigDir, "config.yaml"), `providers:
  local:
    type: openai_compat
    base_url: http://localhost:11434/v1
models:
  default: default
  definitions:
    default:
      provider: local
      id: qwen3-35b-a3b
    custom:
      provider: local
      id: custom-backend
`)

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(cwd)
	})
	if err := os.Chdir(projectDir); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(LoadOptions{
		HomeDir: filepath.Join(tempDir, "home"),
		Env:     map[string]string{},
	})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if got, want := cfg.Models.Definitions["custom"].Retry, cfg.Models.Definitions["default"].Retry; !reflect.DeepEqual(got, want) {
		t.Fatalf("models[custom].retry = %#v, want %#v", got, want)
	}
}

func TestLoadNewModelAliasesDoNotInheritDefaultTokenLimits(t *testing.T) {
	tempDir := t.TempDir()
	projectDir := filepath.Join(tempDir, "project")
	projectConfigDir := filepath.Join(projectDir, ".steiner")
	mustMkdirAll(t, projectConfigDir)

	writeFile(t, filepath.Join(projectConfigDir, "config.yaml"), `providers:
  local:
    type: openai_compat
    base_url: http://localhost:11434/v1
models:
  default: custom
  definitions:
    default:
      provider: local
      id: qwen3-35b-a3b
      advanced:
        limits:
          context_window: 32768
          max_output_tokens: 8192
    custom:
      provider: local
      id: custom-backend
`)

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(cwd)
	})
	if err := os.Chdir(projectDir); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(LoadOptions{
		HomeDir: filepath.Join(tempDir, "home"),
		Env:     map[string]string{},
	})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if got := cfg.Models.Definitions["custom"].Advanced.Limits; got != (AdvancedLimitsConfig{}) {
		t.Fatalf("models[custom].advanced.limits = %#v, want empty for metadata discovery", got)
	}
}

func TestLoadModelAdvancedTransport(t *testing.T) {
	tempDir := t.TempDir()
	projectDir := filepath.Join(tempDir, "project")
	projectConfigDir := filepath.Join(projectDir, ".steiner")
	mustMkdirAll(t, projectConfigDir)

	writeFile(t, filepath.Join(projectConfigDir, "config.yaml"), `providers:
  local:
    type: openai_compat
    base_url: http://localhost:11434/v1
models:
  default: custom
  definitions:
    custom:
      provider: local
      id: minimax-m3
      advanced:
        transport: anthropic
`)

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(cwd)
	})
	if err := os.Chdir(projectDir); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(LoadOptions{
		HomeDir: filepath.Join(tempDir, "home"),
		Env:     map[string]string{},
	})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if got, want := cfg.Models.Definitions["custom"].Advanced.Transport, ModelTransportAnthropic; got != want {
		t.Fatalf("models[custom].advanced.transport = %q, want %q", got, want)
	}
}

func TestLoadPrefersExplicitHomeDirOverEnvHome(t *testing.T) {
	tempDir := t.TempDir()
	explicitHomeDir := filepath.Join(tempDir, "explicit-home")
	envHomeDir := filepath.Join(tempDir, "env-home")
	projectDir := filepath.Join(tempDir, "project")

	mustMkdirAll(t, filepath.Join(explicitHomeDir, ".config", "steiner"))
	mustMkdirAll(t, filepath.Join(envHomeDir, ".config", "steiner"))
	mustMkdirAll(t, projectDir)

	writeFile(t, filepath.Join(explicitHomeDir, ".config", "steiner", "config.yaml"), `providers:
  local:
    type: openai_compat
    base_url: http://explicit.example/v1
models:
  default: default
  definitions:
    default:
      provider: local
      id: explicit-backend
`)

	writeFile(t, filepath.Join(envHomeDir, ".config", "steiner", "config.yaml"), `providers:
  local:
    type: openai_compat
    base_url: http://env.example/v1
models:
  default: default
  definitions:
    default:
      provider: local
      id: env-backend
`)

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(cwd)
	})
	if err := os.Chdir(projectDir); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(LoadOptions{
		HomeDir: explicitHomeDir,
		Env: map[string]string{
			"HOME": envHomeDir,
		},
	})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if got := cfg.Providers["local"].BaseURL; got != "http://explicit.example/v1" {
		t.Fatalf("providers[local].base_url = %q, want explicit home config", got)
	}
}

func TestLoadUsesEnvHomeWhenExplicitHomeDirUnset(t *testing.T) {
	tempDir := t.TempDir()
	envHomeDir := filepath.Join(tempDir, "env-home")
	projectDir := filepath.Join(tempDir, "project")

	mustMkdirAll(t, filepath.Join(envHomeDir, ".config", "steiner"))
	mustMkdirAll(t, projectDir)
	writeFile(t, filepath.Join(envHomeDir, ".config", "steiner", "config.yaml"), `providers:
  local:
    type: openai_compat
    base_url: http://env.example/v1
models:
  default: default
  definitions:
    default:
      provider: local
      id: env-backend
`)

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(cwd)
	})
	if err := os.Chdir(projectDir); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(LoadOptions{
		Env: map[string]string{
			"HOME": envHomeDir,
		},
	})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if got := cfg.Providers["local"].BaseURL; got != "http://env.example/v1" {
		t.Fatalf("providers[local].base_url = %q, want env home config", got)
	}
}

func TestLoadSurfacesHomeResolutionFailure(t *testing.T) {
	orig := userHomeDir
	userHomeDir = func() (string, error) {
		return "", os.ErrNotExist
	}
	t.Cleanup(func() {
		userHomeDir = orig
	})

	_, err := Load(LoadOptions{
		Env: map[string]string{
			"HOME": "",
		},
	})
	if err == nil {
		t.Fatal("Load() error = nil, want home resolution error")
	}
	if !strings.Contains(err.Error(), "resolve home directory") {
		t.Fatalf("error = %q, want home resolution failure", err)
	}
}

func TestLoadSurfacesWorkingDirResolutionFailure(t *testing.T) {
	orig := getwd
	getwd = func() (string, error) {
		return "", os.ErrNotExist
	}
	t.Cleanup(func() {
		getwd = orig
	})

	_, err := Load(LoadOptions{
		Env: map[string]string{
			"HOME": t.TempDir(),
		},
	})
	if err == nil {
		t.Fatal("Load() error = nil, want working directory resolution error")
	}
	if !strings.Contains(err.Error(), "resolve working directory") {
		t.Fatalf("error = %q, want working directory resolution failure", err)
	}
}

func TestLoadExpandsEnvInterpolation(t *testing.T) {
	tempDir := t.TempDir()
	projectDir := filepath.Join(tempDir, "project")
	projectConfigDir := filepath.Join(projectDir, ".steiner")
	homeDir := filepath.Join(tempDir, "home")
	mustMkdirAll(t, projectConfigDir)

	writeFile(t, filepath.Join(projectConfigDir, "config.yaml"), `providers:
  local:
    type: openai_compat
    base_url: ${STEINER_BASE_URL:-http://localhost:11434/v1}
models:
  default: default
  definitions:
    default:
      provider: local
      id: qwen3-35b-a3b
logging:
  file: ~/.local/share/steiner/${STEINER_LOG_FILE:-steiner.log}
`)

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(cwd)
	})
	if err := os.Chdir(projectDir); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(LoadOptions{
		HomeDir: homeDir,
		Env:     map[string]string{},
	})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if got := cfg.Providers["local"].BaseURL; got != "http://localhost:11434/v1" {
		t.Fatalf("providers[local].base_url = %q, want default expansion", got)
	}
	if !strings.HasPrefix(cfg.Logging.File, filepath.Join(homeDir, ".local", "share", "steiner")) {
		t.Fatalf("logging.file = %q, want home-expanded path", cfg.Logging.File)
	}
}

func TestLoadExpandsUnbracedEnvInterpolation(t *testing.T) {
	tempDir := t.TempDir()
	projectDir := filepath.Join(tempDir, "project")
	projectConfigDir := filepath.Join(projectDir, ".steiner")
	homeDir := filepath.Join(tempDir, "home")
	mustMkdirAll(t, projectConfigDir)

	writeFile(t, filepath.Join(projectConfigDir, "config.yaml"), `logging:
  file: "$HOME/steiner.log"
providers:
  local:
    type: openai_compat
    base_url: http://localhost:11434/v1
models:
  default: default
  definitions:
    default:
      provider: local
      id: qwen3-35b-a3b
`)

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(cwd)
	})
	if err := os.Chdir(projectDir); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", homeDir)

	cfg, err := Load(LoadOptions{
		HomeDir: homeDir,
		Env: map[string]string{
			"HOME": homeDir,
		},
	})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	want := filepath.Join(homeDir, "steiner.log")
	if got := cfg.Logging.File; got != want {
		t.Fatalf("logging.file = %q, want %q", got, want)
	}
}

func TestLoadRejectsInvalidConfig(t *testing.T) {
	tempDir := t.TempDir()
	projectDir := filepath.Join(tempDir, "project")
	projectConfigDir := filepath.Join(projectDir, ".steiner")
	mustMkdirAll(t, projectConfigDir)

	writeFile(t, filepath.Join(projectConfigDir, "config.yaml"), `providers:
  local:
    type: unsupported
`)

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(cwd)
	})
	if err := os.Chdir(projectDir); err != nil {
		t.Fatal(err)
	}

	_, err = Load(LoadOptions{
		HomeDir: filepath.Join(tempDir, "home"),
		Env:     map[string]string{},
	})
	if err == nil {
		t.Fatal("Load() error = nil, want invalid config error")
	}
	if !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("error = %q, want provider type validation error", err)
	}
}

func TestLoadRejectsInvalidModelBlock(t *testing.T) {
	tempDir := t.TempDir()
	projectDir := filepath.Join(tempDir, "project")
	projectConfigDir := filepath.Join(projectDir, ".steiner")
	mustMkdirAll(t, projectConfigDir)

	writeFile(t, filepath.Join(projectConfigDir, "config.yaml"), `providers:
  local:
    type: openai_compat
    base_url: http://localhost:11434/v1
models:
  default: default
  definitions:
    default:
      provider: local
      id: ""
`)

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(cwd)
	})
	if err := os.Chdir(projectDir); err != nil {
		t.Fatal(err)
	}

	_, err = Load(LoadOptions{
		HomeDir: filepath.Join(tempDir, "home"),
		Env:     map[string]string{},
	})
	if err == nil {
		t.Fatal("Load() error = nil, want model id validation error")
	}
	if !strings.Contains(err.Error(), "id") {
		t.Fatalf("error = %q, want model id required error", err)
	}
}

func TestLoadRejectsRemovedAdvancedLimitFields(t *testing.T) {
	tests := []struct {
		name      string
		fieldYAML string
	}{
		{
			name:      "output_reserve",
			fieldYAML: "output_reserve: 128\n",
		},
		{
			name:      "safety_margin_tokens",
			fieldYAML: "safety_margin_tokens: 128\n",
		},
		{
			name:      "summary_max_tokens",
			fieldYAML: "summary_max_tokens: 128\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tempDir := t.TempDir()
			projectDir := filepath.Join(tempDir, "project")
			projectConfigDir := filepath.Join(projectDir, ".steiner")
			mustMkdirAll(t, projectConfigDir)

			writeFile(t, filepath.Join(projectConfigDir, "config.yaml"), `providers:
  local:
    type: openai_compat
    base_url: http://localhost:11434/v1
models:
  default: default
  definitions:
    default:
      provider: local
      id: qwen3-35b-a3b
      advanced:
        limits:
`+tt.fieldYAML)

			cwd, err := os.Getwd()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_ = os.Chdir(cwd)
			})
			if err := os.Chdir(projectDir); err != nil {
				t.Fatal(err)
			}

			_, err = Load(LoadOptions{
				HomeDir: filepath.Join(tempDir, "home"),
				Env:     map[string]string{},
			})
			if err == nil {
				t.Fatal("Load() error = nil, want unknown field error")
			}
			if !strings.Contains(err.Error(), tt.name) {
				t.Fatalf("error = %q, want removed field %q in error", err, tt.name)
			}
		})
	}
}

func mustMkdirAll(t *testing.T, path string) {
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %q: %v", path, err)
	}
}

func writeFile(t *testing.T, path, contents string) {
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %q: %v", path, err)
	}
}

func TestLoadSearchConfigEmpty(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, "home")
	projectDir := filepath.Join(tempDir, "project")
	globalDir := filepath.Join(homeDir, ".config", "steiner")
	projectConfigDir := filepath.Join(projectDir, ".steiner")

	mustMkdirAll(t, globalDir)
	mustMkdirAll(t, projectConfigDir)

	writeFile(t, filepath.Join(globalDir, "config.yaml"), `scheduler:
  parallelism: 1
providers:
  local:
    type: openai_compat
    base_url: http://localhost:11434/v1
models:
  default: default
  definitions:
    default:
      provider: local
      id: test-model
      retry:
        enabled: true
        max_attempts: 3
        initial_backoff: 250ms
        max_backoff: 5s
        retry_after_max: 30s
limits:
  max_turns: 25
`)

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(cwd)
	})
	if err := os.Chdir(projectDir); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(LoadOptions{
		HomeDir: homeDir,
		Env:     map[string]string{},
	})
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}

	if cfg.Search.Backend != "" {
		t.Fatalf("Search.Backend = %q, want empty", cfg.Search.Backend)
	}
	if cfg.Search.SearxngURL != "" {
		t.Fatalf("Search.SearxngURL = %q, want empty", cfg.Search.SearxngURL)
	}
}

func TestLoadSearchConfigDeserializes(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, "home")
	projectDir := filepath.Join(tempDir, "project")
	globalDir := filepath.Join(homeDir, ".config", "steiner")
	projectConfigDir := filepath.Join(projectDir, ".steiner")

	mustMkdirAll(t, globalDir)
	mustMkdirAll(t, projectConfigDir)

	writeFile(t, filepath.Join(globalDir, "config.yaml"), `scheduler:
  parallelism: 1
providers:
  local:
    type: openai_compat
    base_url: http://localhost:11434/v1
models:
  default: default
  definitions:
    default:
      provider: local
      id: test-model
      retry:
        enabled: true
        max_attempts: 3
        initial_backoff: 250ms
        max_backoff: 5s
        retry_after_max: 30s
limits:
  max_turns: 25
search:
  backend: searxng
  searxng_url: http://localhost:8888
`)

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(cwd)
	})
	if err := os.Chdir(projectDir); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(LoadOptions{
		HomeDir: homeDir,
		Env:     map[string]string{},
	})
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}

	if cfg.Search.Backend != "searxng" {
		t.Fatalf("Search.Backend = %q, want %q", cfg.Search.Backend, "searxng")
	}
	if cfg.Search.SearxngURL != "http://localhost:8888" {
		t.Fatalf("Search.SearxngURL = %q, want %q", cfg.Search.SearxngURL, "http://localhost:8888")
	}
}

func TestModelConfigVisionFieldNil(t *testing.T) {
	tempDir := t.TempDir()
	projectDir := filepath.Join(tempDir, "project")
	projectConfigDir := filepath.Join(projectDir, ".steiner")
	mustMkdirAll(t, projectConfigDir)

	writeFile(t, filepath.Join(projectConfigDir, "config.yaml"), `providers:
  local:
    type: openai_compat
    base_url: http://localhost:11434/v1
models:
  default: default
  definitions:
    default:
      provider: local
      id: test-model
`)

	cwd, err := os.Getwd()
	if err == nil {
		t.Cleanup(func() {
			_ = os.Chdir(cwd)
		})
	}
	if err := os.Chdir(projectDir); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(LoadOptions{
		HomeDir: filepath.Join(tempDir, "home"),
		Env:     map[string]string{},
	})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Models.Definitions["default"].Vision != nil {
		t.Fatalf("models[default].vision = %v, want nil", cfg.Models.Definitions["default"].Vision)
	}
}

func TestModelConfigVisionFalse(t *testing.T) {
	tempDir := t.TempDir()
	projectDir := filepath.Join(tempDir, "project")
	projectConfigDir := filepath.Join(projectDir, ".steiner")
	mustMkdirAll(t, projectConfigDir)

	writeFile(t, filepath.Join(projectConfigDir, "config.yaml"), `providers:
  local:
    type: openai_compat
    base_url: http://localhost:11434/v1
models:
  default: default
  definitions:
    default:
      provider: local
      id: test-model
      vision: false
`)

	cwd, err := os.Getwd()
	if err == nil {
		t.Cleanup(func() {
			_ = os.Chdir(cwd)
		})
	}
	if err := os.Chdir(projectDir); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(LoadOptions{
		HomeDir: filepath.Join(tempDir, "home"),
		Env:     map[string]string{},
	})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Models.Definitions["default"].Vision == nil {
		t.Fatal("models[default].vision = nil, want pointer to false")
	}
	if *cfg.Models.Definitions["default"].Vision != false {
		t.Fatalf("models[default].vision = %v, want false", *cfg.Models.Definitions["default"].Vision)
	}
}

func TestModelConfigVisionTrue(t *testing.T) {
	tempDir := t.TempDir()
	projectDir := filepath.Join(tempDir, "project")
	projectConfigDir := filepath.Join(projectDir, ".steiner")
	mustMkdirAll(t, projectConfigDir)

	writeFile(t, filepath.Join(projectConfigDir, "config.yaml"), `providers:
  local:
    type: openai_compat
    base_url: http://localhost:11434/v1
models:
  default: default
  definitions:
    default:
      provider: local
      id: test-model
      vision: true
`)

	cwd, err := os.Getwd()
	if err == nil {
		t.Cleanup(func() {
			_ = os.Chdir(cwd)
		})
	}
	if err := os.Chdir(projectDir); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(LoadOptions{
		HomeDir: filepath.Join(tempDir, "home"),
		Env:     map[string]string{},
	})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Models.Definitions["default"].Vision == nil {
		t.Fatal("models[default].vision = nil, want pointer to true")
	}
	if *cfg.Models.Definitions["default"].Vision != true {
		t.Fatalf("models[default].vision = %v, want true", *cfg.Models.Definitions["default"].Vision)
	}
}

func TestLoadDesktopNotificationsConfig(t *testing.T) {
	tempDir := t.TempDir()
	projectDir := filepath.Join(tempDir, "project")
	projectConfigDir := filepath.Join(projectDir, ".steiner")
	mustMkdirAll(t, projectConfigDir)

	writeFile(t, filepath.Join(projectConfigDir, "config.yaml"), `providers:
  local:
    type: openai_compat
    base_url: http://localhost:11434/v1
models:
  default: default
  definitions:
    default:
      provider: local
      id: test-model
desktop_notifications:
  enabled: true
  duration: 10
`)

	cwd, err := os.Getwd()
	if err == nil {
		t.Cleanup(func() {
			_ = os.Chdir(cwd)
		})
	}
	if err := os.Chdir(projectDir); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(LoadOptions{
		HomeDir: filepath.Join(tempDir, "home"),
		Env:     map[string]string{},
	})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if !cfg.DesktopNotifications.Enabled {
		t.Fatal("desktop_notifications.enabled = false, want true")
	}
	if cfg.DesktopNotifications.Duration != 10 {
		t.Fatalf("desktop_notifications.duration = %d, want 10", cfg.DesktopNotifications.Duration)
	}
}

func TestLoadDesktopNotificationsOmitted(t *testing.T) {
	tempDir := t.TempDir()
	projectDir := filepath.Join(tempDir, "project")
	projectConfigDir := filepath.Join(projectDir, ".steiner")
	mustMkdirAll(t, projectConfigDir)

	writeFile(t, filepath.Join(projectConfigDir, "config.yaml"), `providers:
  local:
    type: openai_compat
    base_url: http://localhost:11434/v1
models:
  default: default
  definitions:
    default:
      provider: local
      id: test-model
`)

	cwd, err := os.Getwd()
	if err == nil {
		t.Cleanup(func() {
			_ = os.Chdir(cwd)
		})
	}
	if err := os.Chdir(projectDir); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(LoadOptions{
		HomeDir: filepath.Join(tempDir, "home"),
		Env:     map[string]string{},
	})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.DesktopNotifications.Enabled {
		t.Fatal("desktop_notifications.enabled = true, want false")
	}
	if cfg.DesktopNotifications.Duration != 0 {
		t.Fatalf("desktop_notifications.duration = %d, want 0", cfg.DesktopNotifications.Duration)
	}
}

// TestLoadPermissionsConfig guards against the permissions block being
// documented and struct-defined but never wired into configPatch: a user
// writing the documented `permissions: docker: true` YAML would previously
// hit "field permissions not found in type config.configPatch" at startup.
func TestLoadPermissionsConfig(t *testing.T) {
	tempDir := t.TempDir()
	projectDir := filepath.Join(tempDir, "project")
	projectConfigDir := filepath.Join(projectDir, ".steiner")
	mustMkdirAll(t, projectConfigDir)

	writeFile(t, filepath.Join(projectConfigDir, "config.yaml"), `providers:
  local:
    type: openai_compat
    base_url: http://localhost:11434/v1
models:
  default: default
  definitions:
    default:
      provider: local
      id: test-model
permissions:
  docker: true
`)

	cwd, err := os.Getwd()
	if err == nil {
		t.Cleanup(func() {
			_ = os.Chdir(cwd)
		})
	}
	if err := os.Chdir(projectDir); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(LoadOptions{
		HomeDir: filepath.Join(tempDir, "home"),
		Env:     map[string]string{},
	})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if !cfg.Permissions.Docker {
		t.Fatal("permissions.docker = false, want true")
	}
}

func TestLoadPermissionsConfigOmittedDefaultsToDenied(t *testing.T) {
	tempDir := t.TempDir()
	projectDir := filepath.Join(tempDir, "project")
	projectConfigDir := filepath.Join(projectDir, ".steiner")
	mustMkdirAll(t, projectConfigDir)

	writeFile(t, filepath.Join(projectConfigDir, "config.yaml"), `providers:
  local:
    type: openai_compat
    base_url: http://localhost:11434/v1
models:
  default: default
  definitions:
    default:
      provider: local
      id: test-model
`)

	cwd, err := os.Getwd()
	if err == nil {
		t.Cleanup(func() {
			_ = os.Chdir(cwd)
		})
	}
	if err := os.Chdir(projectDir); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(LoadOptions{
		HomeDir: filepath.Join(tempDir, "home"),
		Env:     map[string]string{},
	})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Permissions.Docker {
		t.Fatal("permissions.docker = true, want false (default deny)")
	}
}

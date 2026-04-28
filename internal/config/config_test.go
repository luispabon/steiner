package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
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

func TestDefaultConfigThinkingChunkDefaultsToFalse(t *testing.T) {
	cfg := defaultConfig()
	if cfg.Logging.ThinkingChunk {
		t.Fatal("default logging.thinking_chunk = true, want false")
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
model:
  type: openai_compat
  base_url: http://global.example/v1
  model: global-backend
  max_completion_tokens: 2048
  context_size: 8192
  compaction:
    safety_margin_tokens: 256
    summary_max_tokens: 128
models:
  global:
    type: openai_compat
    base_url: http://global.example/v1
    model: global-backend
    max_completion_tokens: 2048
    context_size: 8192
    compaction:
      safety_margin_tokens: 256
      summary_max_tokens: 128
  env:
    type: openai_compat
    base_url: http://env.example/v1
    model: env-backend
    max_completion_tokens: 1024
    context_size: 16384
    compaction:
      safety_margin_tokens: 512
      summary_max_tokens: 256
limits:
  max_turns: 25
approval:
  default: auto
paths:
  project_root_only: false
`)

	writeFile(t, filepath.Join(projectConfigDir, "config.yaml"), `model:
  type: openai_compat
  base_url: http://project.example/v1
  model: project-backend
  max_completion_tokens: 4096
  context_size: 32768
  compaction:
    safety_margin_tokens: 1024
    summary_max_tokens: 512
models:
  project:
    type: openai_compat
    base_url: http://project.example/v1
    model: project-backend
    max_completion_tokens: 4096
    context_size: 32768
    compaction:
      safety_margin_tokens: 1024
      summary_max_tokens: 512
  cli:
    type: openai_compat
    base_url: http://cli.example/v1
    model: cli-backend
    max_completion_tokens: 8192
    context_size: 65536
    compaction:
      safety_margin_tokens: 2048
      summary_max_tokens: 1024
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

	if got := cfg.Model.Type; got != "openai_compat" {
		t.Fatalf("model.type = %q, want %q", got, "openai_compat")
	}
	if got := cfg.Model.BaseURL; got != "http://cli.example/v1" {
		t.Fatalf("model.base_url = %q, want %q", got, "http://cli.example/v1")
	}
	if got := cfg.Model.Model; got != "cli-backend" {
		t.Fatalf("model.model = %q, want %q", got, "cli-backend")
	}
	if got := cfg.Model.MaxCompletionTokens; got != 8192 {
		t.Fatalf("model.max_completion_tokens = %d, want %d", got, 8192)
	}
	if got := cfg.Model.ContextSize; got != 65536 {
		t.Fatalf("model.context_size = %d, want %d", got, 65536)
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
	if got := cfg.Models["cli"].BaseURL; got != "http://cli.example/v1" {
		t.Fatalf("models[cli].base_url = %q, want cli override", got)
	}
	if got := cfg.Models["cli"].Model; got != "cli-backend" {
		t.Fatalf("models[cli].model = %q, want cli-backend", got)
	}
	if got := cfg.Models["global"].BaseURL; got != "http://global.example/v1" {
		t.Fatalf("models[global].base_url = %q, want global config", got)
	}
	if got := cfg.Models["env"].Model; got != "env-backend" {
		t.Fatalf("models[env].model = %q, want env config", got)
	}
}

func TestLoadExpandsEnvInterpolation(t *testing.T) {
	tempDir := t.TempDir()
	projectDir := filepath.Join(tempDir, "project")
	projectConfigDir := filepath.Join(projectDir, ".steiner")
	homeDir := filepath.Join(tempDir, "home")
	mustMkdirAll(t, projectConfigDir)

	writeFile(t, filepath.Join(projectConfigDir, "config.yaml"), `model:
  type: openai_compat
  base_url: ${STEINER_BASE_URL:-http://localhost:11434/v1}
  model: qwen3-35b-a3b
  max_completion_tokens: 8192
  context_size: 32768
  compaction:
    safety_margin_tokens: 2048
    summary_max_tokens: 1024
models:
  default:
    type: openai_compat
    base_url: ${STEINER_BASE_URL:-http://localhost:11434/v1}
    model: qwen3-35b-a3b
    max_completion_tokens: 8192
    context_size: 32768
    compaction:
      safety_margin_tokens: 2048
      summary_max_tokens: 1024
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

	if got := cfg.Models["default"].BaseURL; got != "http://localhost:11434/v1" {
		t.Fatalf("models[default].base_url = %q, want default expansion", got)
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
model:
  type: openai_compat
  base_url: http://localhost:11434/v1
  model: qwen3-35b-a3b
  max_completion_tokens: 8192
  context_size: 32768
  compaction:
    safety_margin_tokens: 2048
    summary_max_tokens: 1024
models:
  default:
    type: openai_compat
    base_url: http://localhost:11434/v1
    model: qwen3-35b-a3b
    max_completion_tokens: 8192
    context_size: 32768
    compaction:
      safety_margin_tokens: 2048
      summary_max_tokens: 1024
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

	writeFile(t, filepath.Join(projectConfigDir, "config.yaml"), `provider:
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
	if !strings.Contains(err.Error(), "provider") {
		t.Fatalf("error = %q, want old provider schema rejection", err)
	}
}

func TestLoadRejectsInvalidModelBlock(t *testing.T) {
	tempDir := t.TempDir()
	projectDir := filepath.Join(tempDir, "project")
	projectConfigDir := filepath.Join(projectDir, ".steiner")
	mustMkdirAll(t, projectConfigDir)

	writeFile(t, filepath.Join(projectConfigDir, "config.yaml"), `model:
  type: ""
  base_url: ""
  model: ""
models:
  default:
    type: openai_compat
    base_url: http://localhost:11434/v1
    model: qwen3-35b-a3b
    max_completion_tokens: 8192
    context_size: 32768
    compaction:
      safety_margin_tokens: 2048
      summary_max_tokens: 1024
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
		t.Fatal("Load() error = nil, want model validation error")
	}
	if !strings.Contains(err.Error(), "model.type is required") {
		t.Fatalf("error = %q, want model validation error", err)
	}
}

func TestLoadAllowsZeroMaxTurns(t *testing.T) {
	tempDir := t.TempDir()
	projectDir := filepath.Join(tempDir, "project")
	projectConfigDir := filepath.Join(projectDir, ".steiner")
	mustMkdirAll(t, projectConfigDir)

	writeFile(t, filepath.Join(projectConfigDir, "config.yaml"), `model:
  type: openai_compat
  base_url: http://localhost:11434/v1
  model: qwen3-35b-a3b
  max_completion_tokens: 8192
  context_size: 32768
  compaction:
    safety_margin_tokens: 2048
    summary_max_tokens: 1024
models:
  default:
    type: openai_compat
    base_url: http://localhost:11434/v1
    model: qwen3-35b-a3b
    max_completion_tokens: 8192
    context_size: 32768
    compaction:
      safety_margin_tokens: 2048
      summary_max_tokens: 1024
limits:
  max_turns: 0
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
	if got := cfg.Limits.MaxTurns; got != 0 {
		t.Fatalf("limits.max_turns = %d, want 0", got)
	}
}

func TestLoadRejectsSummaryMaxTokensAboveMaxCompletionTokens(t *testing.T) {
	tempDir := t.TempDir()
	projectDir := filepath.Join(tempDir, "project")
	projectConfigDir := filepath.Join(projectDir, ".steiner")
	mustMkdirAll(t, projectConfigDir)

	writeFile(t, filepath.Join(projectConfigDir, "config.yaml"), `model:
  type: openai_compat
  base_url: http://localhost:11434/v1
  model: qwen3-35b-a3b
  max_completion_tokens: 256
  context_size: 32768
  compaction:
    safety_margin_tokens: 2048
    summary_max_tokens: 512
models:
  default:
    type: openai_compat
    base_url: http://localhost:11434/v1
    model: qwen3-35b-a3b
    max_completion_tokens: 256
    context_size: 32768
    compaction:
      safety_margin_tokens: 2048
      summary_max_tokens: 512
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
		t.Fatal("Load() error = nil, want summary max validation error")
	}
	if !strings.Contains(err.Error(), `models["default"].compaction.summary_max_tokens must be less than or equal to models["default"].max_completion_tokens`) {
		t.Fatalf("error = %q, want summary/max completion validation", err)
	}
}

func mustMkdirAll(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestLoadMergesExtraParams(t *testing.T) {
	tempDir := t.TempDir()
	projectDir := filepath.Join(tempDir, "project")
	projectConfigDir := filepath.Join(projectDir, ".steiner")
	mustMkdirAll(t, projectConfigDir)

	writeFile(t, filepath.Join(projectConfigDir, "config.yaml"), `model:
  type: openai_compat
  base_url: http://localhost:11434/v1
  model: qwen3-35b-a3b
  extra_params:
    temperature: 0.7
    top_p: 0.9
  max_completion_tokens: 8192
  context_size: 32768
  compaction:
    safety_margin_tokens: 2048
    summary_max_tokens: 1024
models:
  default:
    type: openai_compat
    base_url: http://localhost:11434/v1
    model: qwen3-35b-a3b
    extra_params:
      temperature: 0.7
      top_p: 0.9
    max_completion_tokens: 8192
    context_size: 32768
    compaction:
      safety_margin_tokens: 2048
      summary_max_tokens: 1024
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

	ep := cfg.Models["default"].ExtraParams
	if ep == nil {
		t.Fatal("ExtraParams = nil, want non-nil map")
	}
	if got, want := ep["temperature"], 0.7; got != want {
		t.Fatalf("ExtraParams[temperature] = %v, want %v", got, want)
	}
	if got, want := ep["top_p"], 0.9; got != want {
		t.Fatalf("ExtraParams[top_p] = %v, want %v", got, want)
	}
}

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestExpandEnvText(t *testing.T) {
	tests := []struct {
		name  string
		input string
		env   map[string]string
		want  string
	}{
		{
			name:  "unbraced var",
			input: "$HOME",
			env:   map[string]string{"HOME": "/home/user"},
			want:  "/home/user",
		},
		{
			name:  "braced var",
			input: "${HOME}",
			env:   map[string]string{"HOME": "/home/user"},
			want:  "/home/user",
		},
		{
			name:  "braced default when var unset",
			input: "${UNDEF:-default}",
			env:   map[string]string{},
			want:  "default",
		},
		{
			name:  "braced default when var empty",
			input: "${VAR:-default}",
			env:   map[string]string{"VAR": ""},
			want:  "default",
		},
		{
			name:  "braced default empty string",
			input: "${UNDEF:-}",
			env:   map[string]string{},
			want:  "",
		},
		{
			name:  "dollar escape",
			input: "$$HOME",
			env:   map[string]string{"HOME": "/home/user"},
			want:  "$HOME",
		},
		{
			name:  "unclosed brace passes through",
			input: "${HOME",
			env:   map[string]string{"HOME": "/home/user"},
			want:  "${HOME",
		},
		{
			name:  "non-identifier name passes through",
			input: "${123invalid}",
			env:   map[string]string{"123invalid": "val"},
			want:  "${123invalid}",
		},
		{
			name:  "empty input",
			input: "",
			env:   map[string]string{},
			want:  "",
		},
		{
			name:  "unbraced var with empty value",
			input: "$HOME",
			env:   map[string]string{"HOME": ""},
			want:  "",
		},
		{
			name:  "dollar at end of string",
			input: "path$",
			env:   map[string]string{},
			want:  "path$",
		},
		{
			name:  "dollar followed by non-identifier",
			input: "hello$ world",
			env:   map[string]string{},
			want:  "hello$ world",
		},
		{
			name:  "mixed content with multiple vars",
			input: "prefix/${VAR}/suffix",
			env:   map[string]string{"VAR": "middle"},
			want:  "prefix/middle/suffix",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lookup := func(name string) (string, bool) {
				v, ok := tt.env[name]
				return v, ok
			}
			got := expandEnvText(tt.input, lookup)
			if got != tt.want {
				t.Errorf("expandEnvText(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestApplyEnvOverridesRejectsInvalidIntegers(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
	}{
		{
			name: "invalid STEINER_SCHEDULER_PARALLELISM",
			env:  map[string]string{"STEINER_SCHEDULER_PARALLELISM": "not-a-number"},
		},
		{
			name: "invalid STEINER_MAX_TURNS",
			env:  map[string]string{"STEINER_MAX_TURNS": "abc"},
		},
		{
			name: "invalid STEINER_MAX_TOKENS",
			env:  map[string]string{"STEINER_MAX_TOKENS": "12.5"},
		},
		{
			name: "invalid STEINER_TOOL_OUTPUT_MAX_BYTES",
			env:  map[string]string{"STEINER_TOOL_OUTPUT_MAX_BYTES": ""},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := defaultConfig()
			err := applyEnvOverrides(&cfg, tt.env)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}

func TestEnvironMap(t *testing.T) {
	tests := []struct {
		name  string
		input []string
		want  map[string]string
	}{
		{
			name:  "skips malformed entries without =",
			input: []string{"KEY=value", "MALFORMED", "OTHER=val"},
			want:  map[string]string{"KEY": "value", "OTHER": "val"},
		},
		{
			name:  "handles empty value",
			input: []string{"EMPTY="},
			want:  map[string]string{"EMPTY": ""},
		},
		{
			name:  "handles multiple equals signs",
			input: []string{"KEY=a=b=c"},
			want:  map[string]string{"KEY": "a=b=c"},
		},
		{
			name:  "empty input returns empty map",
			input: []string{},
			want:  map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := environMap(tt.input)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("environMap(%v) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestNormalizePaths(t *testing.T) {
	home := "/home/user"

	tests := []struct {
		name string
		cfg  Config
		want Config
	}{
		{
			name: "expands tilde alone",
			cfg:  Config{Logging: LoggingConfig{File: "~"}},
			want: Config{Logging: LoggingConfig{File: home}},
		},
		{
			name: "expands tilde prefix path",
			cfg:  Config{Logging: LoggingConfig{File: "~/logs/steiner.log"}},
			want: Config{Logging: LoggingConfig{File: "/home/user/logs/steiner.log"}},
		},
		{
			name: "leaves absolute path unchanged",
			cfg:  Config{Logging: LoggingConfig{File: "/var/log/steiner.log"}},
			want: Config{Logging: LoggingConfig{File: "/var/log/steiner.log"}},
		},
		{
			name: "leaves empty path unchanged",
			cfg:  Config{Logging: LoggingConfig{File: ""}},
			want: Config{Logging: LoggingConfig{File: ""}},
		},
		{
			name: "leaves path without tilde unchanged",
			cfg:  Config{Logging: LoggingConfig{File: "relative/path.log"}},
			want: Config{Logging: LoggingConfig{File: "relative/path.log"}},
		},
		{
			name: "expands tilde in extra files",
			cfg:  Config{ProjectContext: ProjectContextConfig{ExtraFiles: []string{"~/file1", "/abs/file2"}}},
			want: Config{ProjectContext: ProjectContextConfig{ExtraFiles: []string{"/home/user/file1", "/abs/file2"}}},
		},
		{
			name: "expands tilde in ignore files",
			cfg:  Config{ProjectContext: ProjectContextConfig{IgnoreFiles: []string{"~/ignore"}}},
			want: Config{ProjectContext: ProjectContextConfig{IgnoreFiles: []string{"/home/user/ignore"}}},
		},
		{
			name: "expands tilde in writable paths",
			cfg:  Config{Paths: PathsConfig{WritablePaths: []string{"~/data"}}},
			want: Config{Paths: PathsConfig{WritablePaths: []string{"/home/user/data"}}},
		},
		{
			name: "expands tilde in blocked paths",
			cfg:  Config{Paths: PathsConfig{BlockedPaths: []string{"~/blocked"}}},
			want: Config{Paths: PathsConfig{BlockedPaths: []string{"/home/user/blocked"}}},
		},
		{
			name: "expands tilde in exclude paths",
			cfg:  Config{Paths: PathsConfig{ExcludePaths: []string{"~/secret"}}},
			want: Config{Paths: PathsConfig{ExcludePaths: []string{"/home/user/secret"}}},
		},
		{
			name: "expands tilde in tool exec",
			cfg:  Config{Tools: map[string]ToolConfig{"test": {Exec: "~/bin/tool"}}},
			want: Config{Tools: map[string]ToolConfig{"test": {Exec: "/home/user/bin/tool"}}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			normalizePaths(&tt.cfg, home)
			if !reflect.DeepEqual(tt.cfg, tt.want) {
				t.Errorf("normalizePaths() = %+v, want %+v", tt.cfg, tt.want)
			}
		})
	}
}

func TestDurationString(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{input: "5s", want: "5s"},
		{input: "30s", want: "30s"},
		{input: "5m", want: "5m0s"},
		{input: "1h", want: "1h0m0s"},
		{input: "500ms", want: "500ms"},
		{input: "0s", want: "0s"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			d, err := newDuration(tt.input)
			if err != nil {
				t.Fatal(err)
			}
			got := d.String()
			if got != tt.want {
				t.Errorf("Duration(%q).String() = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestDurationIsZero(t *testing.T) {
	t.Run("unset duration is zero", func(t *testing.T) {
		var d Duration
		if !d.IsZero() {
			t.Error("expected IsZero() = true for zero value Duration")
		}
	})
	t.Run("zero duration is zero", func(t *testing.T) {
		d, err := newDuration("0s")
		if err != nil {
			t.Fatal(err)
		}
		if !d.IsZero() {
			t.Error("expected IsZero() = true for 0s duration")
		}
	})
	t.Run("non-zero duration is not zero", func(t *testing.T) {
		d, err := newDuration("5s")
		if err != nil {
			t.Fatal(err)
		}
		if d.IsZero() {
			t.Error("expected IsZero() = false for 5s duration")
		}
	})
}

func TestDurationYAMLRoundTrip(t *testing.T) {
	d, err := newDuration("30s")
	if err != nil {
		t.Fatal(err)
	}

	marshaled, err := yaml.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}

	var got Duration
	if err := yaml.Unmarshal(marshaled, &got); err != nil {
		t.Fatal(err)
	}

	if got.Duration() != d.Duration() {
		t.Errorf("round-trip Duration() = %d, want %d", got.Duration(), d.Duration())
	}
}

func TestDurationUnmarshalYAMLNull(t *testing.T) {
	var d Duration
	if err := yaml.Unmarshal([]byte("null"), &d); err != nil {
		t.Fatal(err)
	}
	if !d.IsZero() {
		t.Error("expected IsZero() = true after unmarshaling null")
	}
}

func TestDurationUnmarshalYAMLEmpty(t *testing.T) {
	var d Duration
	if err := yaml.Unmarshal([]byte(`""`), &d); err != nil {
		t.Fatal(err)
	}
	if !d.IsZero() {
		t.Error("expected IsZero() = true after unmarshaling empty string")
	}
}

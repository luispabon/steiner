package sandbox

import (
	"slices"
	"testing"
)

func TestFilterEnv_PassesAllowlisted(t *testing.T) {
	input := []string{
		"HOME=/home/realuser",
		"PATH=/usr/bin:/bin",
		"TERM=xterm-256color",
		"LANG=en_US.UTF-8",
		"TZ=UTC",
		"SSH_AUTH_SOCK=/run/user/1000/ssh-agent.sock",
		"EDITOR=vim",
		"VISUAL=vim",
		"SHELL=/bin/bash",
		"USER=luis",
		"LOGNAME=luis",
		"XDG_RUNTIME_DIR=/run/user/1000",
	}

	result := FilterEnv(input, EnvPolicy{})

	for _, want := range input {
		if !slices.Contains(result, want) {
			t.Errorf("expected %q to be present in filtered env", want)
		}
	}
}

func TestFilterEnv_BlocksSensitiveVars(t *testing.T) {
	input := []string{
		"AWS_SECRET_ACCESS_KEY=secret",
		"AWS_ACCESS_KEY_ID=key",
		"GH_TOKEN=ghp_xxx",
		"GITHUB_TOKEN=xxx",
		"OPENAI_API_KEY=sk-xxx",
		"ANTHROPIC_API_KEY=xxx",
	}

	result := FilterEnv(input, EnvPolicy{})

	for _, blocked := range input {
		if slices.Contains(result, blocked) {
			t.Errorf("expected %q to be blocked from filtered env", blocked)
		}
	}
}

func TestFilterEnv_PassesHomeUnchanged(t *testing.T) {
	input := []string{
		"HOME=/home/realuser",
		"PATH=/usr/bin",
	}

	result := FilterEnv(input, EnvPolicy{})

	if !slices.Contains(result, "HOME=/home/realuser") {
		t.Errorf("HOME should pass through unchanged, got: %v", result)
	}
}

func TestFilterEnv_PassesLCPrefix(t *testing.T) {
	input := []string{
		"LC_ALL=en_US.UTF-8",
		"LC_CTYPE=UTF-8",
		"LC_MESSAGES=en_US",
	}

	result := FilterEnv(input, EnvPolicy{})

	for _, want := range input {
		if !slices.Contains(result, want) {
			t.Errorf("expected LC_ var %q to be passed through", want)
		}
	}
}

func TestFilterEnv_NilInput(t *testing.T) {
	result := FilterEnv(nil, EnvPolicy{})
	if result != nil {
		t.Errorf("expected nil for nil input, got %v", result)
	}

	resultPassthrough := FilterEnv(nil, EnvPolicy{PassthroughAll: true})
	if resultPassthrough != nil {
		t.Errorf("expected nil for nil input even with PassthroughAll, got %v", resultPassthrough)
	}
}

func TestFilterEnv_MissingEqualsSkipped(t *testing.T) {
	input := []string{"NOEQUALS", "PATH=/usr/bin"}
	result := FilterEnv(input, EnvPolicy{})

	// NOEQUALS should be skipped, PATH should pass.
	for _, kv := range result {
		if kv == "NOEQUALS" {
			t.Error("entry without '=' should be skipped")
		}
	}
	if !slices.Contains(result, "PATH=/usr/bin") {
		t.Error("PATH should be present")
	}
}

func TestFilterEnv_ExtraAllowlist(t *testing.T) {
	tests := []struct {
		name    string
		extra   []string
		input   []string
		wantIn  []string
		wantOut []string
	}{
		{
			name:    "exact name match",
			extra:   []string{"MY_CUSTOM_VAR"},
			input:   []string{"MY_CUSTOM_VAR=value", "OTHER_VAR=x"},
			wantIn:  []string{"MY_CUSTOM_VAR=value"},
			wantOut: []string{"OTHER_VAR=x"},
		},
		{
			name:    "prefix glob match",
			extra:   []string{"MYAPP_*"},
			input:   []string{"MYAPP_FOO=1", "MYAPP_BAR=2", "OTHERAPP_FOO=3"},
			wantIn:  []string{"MYAPP_FOO=1", "MYAPP_BAR=2"},
			wantOut: []string{"OTHERAPP_FOO=3"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FilterEnv(tt.input, EnvPolicy{Extra: tt.extra})
			for _, want := range tt.wantIn {
				if !slices.Contains(result, want) {
					t.Errorf("expected %q to be present, got %v", want, result)
				}
			}
			for _, notWant := range tt.wantOut {
				if slices.Contains(result, notWant) {
					t.Errorf("expected %q to be absent, got %v", notWant, result)
				}
			}
		})
	}
}

func TestFilterEnv_PassthroughAll(t *testing.T) {
	input := []string{
		"ANTHROPIC_API_KEY=sk-xxx",
		"GH_TOKEN=ghp_xxx",
		"RANDOM_VAR=whatever",
	}

	result := FilterEnv(input, EnvPolicy{PassthroughAll: true})

	if !slices.Equal(result, input) {
		t.Errorf("expected all vars passed through unchanged, got %v", result)
	}

	// Confirm we returned a copy, not an alias of the caller's slice.
	result[0] = "MUTATED=1"
	if input[0] != "ANTHROPIC_API_KEY=sk-xxx" {
		t.Error("FilterEnv must not alias the caller's slice under PassthroughAll")
	}
}

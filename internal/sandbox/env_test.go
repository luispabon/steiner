package sandbox

import (
	"slices"
	"testing"
)

func TestFilterEnv_PassesAllowlisted(t *testing.T) {
	input := []string{
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

	result := FilterEnv(input)

	for _, want := range input {
		key, _, _ := splitKV(want)
		if key == "HOME" {
			continue
		}
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

	result := FilterEnv(input)

	for _, blocked := range input {
		if slices.Contains(result, blocked) {
			t.Errorf("expected %q to be blocked from filtered env", blocked)
		}
	}
}

func TestFilterEnv_OverridesHome(t *testing.T) {
	input := []string{
		"HOME=/home/realuser",
		"PATH=/usr/bin",
	}

	result := FilterEnv(input)

	if slices.Contains(result, "HOME=/home/realuser") {
		t.Error("original HOME should not pass through")
	}
	if !slices.Contains(result, "HOME=/home/steiner") {
		t.Errorf("HOME should be overridden to /home/steiner, got: %v", result)
	}
}

func TestFilterEnv_PassesLCPrefix(t *testing.T) {
	input := []string{
		"LC_ALL=en_US.UTF-8",
		"LC_CTYPE=UTF-8",
		"LC_MESSAGES=en_US",
	}

	result := FilterEnv(input)

	for _, want := range input {
		if !slices.Contains(result, want) {
			t.Errorf("expected LC_ var %q to be passed through", want)
		}
	}
}

func TestFilterEnv_NilInput(t *testing.T) {
	result := FilterEnv(nil)
	if result != nil {
		t.Errorf("expected nil for nil input, got %v", result)
	}
}

func TestFilterEnv_MissingEqualsSkipped(t *testing.T) {
	input := []string{"NOEQUALS", "PATH=/usr/bin"}
	result := FilterEnv(input)

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

func splitKV(kv string) (key, val string, ok bool) {
	for i := 0; i < len(kv); i++ {
		if kv[i] == '=' {
			return kv[:i], kv[i+1:], true
		}
	}
	return kv, "", false
}

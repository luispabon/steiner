package tui

import "testing"

func TestParseInputHandlesConfigCommand(t *testing.T) {
	action := parseInput("/config")
	if !action.inspectConfig {
		t.Fatal("inspectConfig = false, want true")
	}
	if action.submit != "" {
		t.Fatalf("submit = %q, want empty", action.submit)
	}
}

func TestBuildCompletionCandidatesIncludesConfig(t *testing.T) {
	got := buildCompletionCandidates("/co", nil, nil, false)
	if len(got) != 2 {
		t.Fatalf("candidates = %#v, want 2 candidates", got)
	}
	if got[0] != "/compact" || got[1] != "/config" {
		t.Fatalf("candidates = %#v, want [/compact, /config]", got)
	}
}

func TestParseInputHandlesResumeCommand(t *testing.T) {
	action := parseInput("/resume")
	if !action.requestSessionPicker {
		t.Fatal("requestSessionPicker = false, want true")
	}
	if action.submit != "" {
		t.Fatalf("submit = %q, want empty", action.submit)
	}
}

func TestParseInputHandlesListFiles(t *testing.T) {
	t.Run("no path defaults to working directory", func(t *testing.T) {
		action := parseInput("/ls")
		if !action.listFiles {
			t.Fatal("listFiles = false, want true")
		}
		if action.listFilesPath != "" {
			t.Fatalf("listFilesPath = %q, want empty", action.listFilesPath)
		}
	})

	t.Run("with path argument", func(t *testing.T) {
		action := parseInput("/ls internal/")
		if !action.listFiles {
			t.Fatal("listFiles = false, want true")
		}
		if action.listFilesPath != "internal/" {
			t.Fatalf("listFilesPath = %q, want internal/", action.listFilesPath)
		}
	})

	t.Run("submits as text without slash", func(t *testing.T) {
		action := parseInput("ls")
		if action.listFiles {
			t.Fatal("listFiles = true, want false for text without slash")
		}
		if action.submit != "ls" {
			t.Fatalf("submit = %q, want ls", action.submit)
		}
	})

	t.Run("buildCompletionCandidates includes ls", func(t *testing.T) {
		got := buildCompletionCandidates("/l", nil, nil, false)
		found := false
		for _, c := range got {
			if c == "/ls" {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("candidates = %#v, want /ls included", got)
		}
	})
}

func TestParseInputHandlesSkillInvocation(t *testing.T) {
	t.Run("direct invocation", func(t *testing.T) {
		action := parseInputWithSkills("/mykill", nil, []string{"mykill"})
		if action.invokeSkill != "mykill" {
			t.Fatalf("invokeSkill = %q, want mykill", action.invokeSkill)
		}
		if action.invokeSkillArgs != "" {
			t.Fatalf("invokeSkillArgs = %q, want empty", action.invokeSkillArgs)
		}
	})

	t.Run("invocation with args", func(t *testing.T) {
		action := parseInputWithSkills("/mykill some args here", nil, []string{"mykill"})
		if action.invokeSkill != "mykill" {
			t.Fatalf("invokeSkill = %q, want mykill", action.invokeSkill)
		}
		if action.invokeSkillArgs != "some args here" {
			t.Fatalf("invokeSkillArgs = %q, want 'some args here'", action.invokeSkillArgs)
		}
	})

	t.Run("unknown skill is submitted as text", func(t *testing.T) {
		action := parseInputWithSkills("/unknownSkill", nil, []string{"mykill"})
		if action.submit != "/unknownSkill" {
			t.Fatalf("submit = %q, want /unknownSkill", action.submit)
		}
		if action.invokeSkill != "" {
			t.Fatalf("invokeSkill = %q, want empty", action.invokeSkill)
		}
	})

	t.Run("prefix match does not invoke different skill", func(t *testing.T) {
		action := parseInputWithSkills("/mykill-extra", nil, []string{"mykill"})
		if action.invokeSkill != "" {
			t.Fatalf("invokeSkill = %q, want empty", action.invokeSkill)
		}
		if action.submit != "/mykill-extra" {
			t.Fatalf("submit = %q, want /mykill-extra", action.submit)
		}
	})

	t.Run("similar skill names resolve exactly", func(t *testing.T) {
		action := parseInputWithSkills("/reviewer arg", nil, []string{"review", "reviewer"})
		if action.invokeSkill != "reviewer" {
			t.Fatalf("invokeSkill = %q, want reviewer", action.invokeSkill)
		}
		if action.invokeSkillArgs != "arg" {
			t.Fatalf("invokeSkillArgs = %q, want arg", action.invokeSkillArgs)
		}
	})

	t.Run("slash-skill still works", func(t *testing.T) {
		action := parseInputWithSkills("/skill mykill", nil, []string{"mykill"})
		if action.toggleSkill != "mykill" {
			t.Fatalf("toggleSkill = %q, want mykill", action.toggleSkill)
		}
		if action.invokeSkill != "" {
			t.Fatalf("invokeSkill = %q, want empty", action.invokeSkill)
		}
	})
}

func TestBuildCompletionCandidatesAllowlistExcludesOneshot(t *testing.T) {
	got := buildCompletionCandidates("/", nil, nil, true)
	for _, c := range got {
		if c == "/oneshot" {
			t.Fatalf("candidates includes /oneshot in allowlist mode, got %#v", got)
		}
	}
	// Sanity: at least one allowlist item is present
	found := 0
	for _, c := range got {
		if c == "/exit" || c == "/thinking" || c == "/accent" ||
			c == "/accent amber" || c == "/accent rose" || c == "/accent magenta" ||
			c == "/accent violet" || c == "/accent cyan" || c == "/accent mint" ||
			c == "/accent lime" {
			found++
		}
	}
	if found == 0 {
		t.Fatalf("allowlist items absent from candidates, got %#v", got)
	}
}

func TestParseInputHandlesOneshotResumeCommand(t *testing.T) {
	action := parseInput("/oneshot-resume")
	if !action.requestOneshotResumePicker {
		t.Fatal("requestOneshotResumePicker = false, want true")
	}
	if action.submit != "" {
		t.Fatalf("submit = %q, want empty", action.submit)
	}
}

func TestParseInputHandlesCacheStatsCommand(t *testing.T) {
	action := parseInput("/cache-stats")
	if !action.openCacheStats {
		t.Fatal("openCacheStats = false, want true")
	}
	if action.submit != "" {
		t.Fatalf("submit = %q, want empty", action.submit)
	}
}

func TestBuildCompletionCandidatesIncludesOneshotResume(t *testing.T) {
	got := buildCompletionCandidates("/oneshot", nil, nil, false)
	found := false
	for _, c := range got {
		if c == "/oneshot-resume" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("candidates = %#v, want /oneshot-resume included", got)
	}
}

package tui

import "testing"

func TestParseInputHandlesContextCommand(t *testing.T) {
	action := parseInput("/context", nil)
	if !action.inspectContext {
		t.Fatal("inspectContext = false, want true")
	}
	if action.submit != "" {
		t.Fatalf("submit = %q, want empty", action.submit)
	}
}

func TestParseInputHandlesConfigCommand(t *testing.T) {
	action := parseInput("/config", nil)
	if !action.inspectConfig {
		t.Fatal("inspectConfig = false, want true")
	}
	if action.submit != "" {
		t.Fatalf("submit = %q, want empty", action.submit)
	}
}

func TestBuildCompletionCandidatesIncludesContext(t *testing.T) {
	got := buildCompletionCandidates("/co", nil, nil)
	if len(got) != 3 {
		t.Fatalf("candidates = %#v, want 3 candidates", got)
	}
	if got[0] != "/compact" || got[1] != "/config" || got[2] != "/context" {
		t.Fatalf("candidates = %#v, want [/compact, /config, /context]", got)
	}
}

func TestParseInputHandlesResumeCommand(t *testing.T) {
	action := parseInput("/resume", nil)
	if !action.requestSessionPicker {
		t.Fatal("requestSessionPicker = false, want true")
	}
	if action.submit != "" {
		t.Fatalf("submit = %q, want empty", action.submit)
	}
}

func TestParseInputHandlesListFiles(t *testing.T) {
	t.Run("no path defaults to working directory", func(t *testing.T) {
		action := parseInput("/ls", nil)
		if !action.listFiles {
			t.Fatal("listFiles = false, want true")
		}
		if action.listFilesPath != "" {
			t.Fatalf("listFilesPath = %q, want empty", action.listFilesPath)
		}
	})

	t.Run("with path argument", func(t *testing.T) {
		action := parseInput("/ls internal/", nil)
		if !action.listFiles {
			t.Fatal("listFiles = false, want true")
		}
		if action.listFilesPath != "internal/" {
			t.Fatalf("listFilesPath = %q, want internal/", action.listFilesPath)
		}
	})

	t.Run("submits as text without slash", func(t *testing.T) {
		action := parseInput("ls", nil)
		if action.listFiles {
			t.Fatal("listFiles = true, want false for text without slash")
		}
		if action.submit != "ls" {
			t.Fatalf("submit = %q, want ls", action.submit)
		}
	})

	t.Run("buildCompletionCandidates includes ls", func(t *testing.T) {
		got := buildCompletionCandidates("/l", nil, nil)
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

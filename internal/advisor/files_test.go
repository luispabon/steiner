package advisor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/tool"
)

func TestDecodeAdvisorInput(t *testing.T) {
	tests := []struct {
		name    string
		raw     map[string]any
		want    advisorInput
		wantErr bool
	}{
		{
			name: "nil map decodes to zero value",
			raw:  nil,
			want: advisorInput{},
		},
		{
			name: "empty map decodes to zero value",
			raw:  map[string]any{},
			want: advisorInput{},
		},
		{
			name: "question and files decode",
			raw: map[string]any{
				"question": "is this sound?",
				"files":    []any{"a.md", "b.yaml"},
			},
			want: advisorInput{Question: "is this sound?", Files: []string{"a.md", "b.yaml"}},
		},
		{
			name:    "non-string question rejected",
			raw:     map[string]any{"question": 5},
			wantErr: true,
		},
		{
			name:    "non-array files rejected",
			raw:     map[string]any{"files": "not-an-array"},
			wantErr: true,
		},
		{
			name:    "non-string file entry rejected",
			raw:     map[string]any{"files": []any{"ok.md", 5}},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := decodeAdvisorInput(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatal("decodeAdvisorInput() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("decodeAdvisorInput() error = %v", err)
			}
			if got.Question != tt.want.Question {
				t.Fatalf("Question = %q, want %q", got.Question, tt.want.Question)
			}
			if len(got.Files) != len(tt.want.Files) {
				t.Fatalf("Files = %#v, want %#v", got.Files, tt.want.Files)
			}
			for i := range got.Files {
				if got.Files[i] != tt.want.Files[i] {
					t.Fatalf("Files[%d] = %q, want %q", i, got.Files[i], tt.want.Files[i])
				}
			}
		})
	}
}

func TestLoadAdvisorFilesEmptyReturnsNilWithoutPolicy(t *testing.T) {
	files, err := loadAdvisorFiles("/work", nil, nil)
	if err != nil {
		t.Fatalf("loadAdvisorFiles() error = %v", err)
	}
	if files != nil {
		t.Fatalf("files = %#v, want nil", files)
	}
}

func TestLoadAdvisorFilesTooManyRejected(t *testing.T) {
	policy := tool.NewPathPolicy("/work", config.PathsConfig{})
	paths := make([]string, maxAdvisorFiles+1)
	for i := range paths {
		paths[i] = "f.md"
	}
	_, err := loadAdvisorFiles("/work", &policy, paths)
	if err == nil {
		t.Fatal("loadAdvisorFiles() error = nil, want too-many-files error")
	}
}

func TestLoadAdvisorFilesMissingFileRejected(t *testing.T) {
	workDir := t.TempDir()
	policy := tool.NewPathPolicy(workDir, config.PathsConfig{})
	_, err := loadAdvisorFiles(workDir, &policy, []string{"does-not-exist.md"})
	if err == nil {
		t.Fatal("loadAdvisorFiles() error = nil, want missing-file error")
	}
}

func TestLoadAdvisorFilesBlockedPathRejected(t *testing.T) {
	workDir := t.TempDir()
	blockedDir := filepath.Join(workDir, "secret")
	if err := os.Mkdir(blockedDir, 0o755); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}
	blockedFile := filepath.Join(blockedDir, "value.txt")
	if err := os.WriteFile(blockedFile, []byte("shh"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	policy := tool.NewPathPolicy(workDir, config.PathsConfig{BlockedPaths: []string{"secret"}})

	_, err := loadAdvisorFiles(workDir, &policy, []string{"secret/value.txt"})
	if err == nil {
		t.Fatal("loadAdvisorFiles() error = nil, want blocked-path error")
	}
}

func TestLoadAdvisorFilesReadsAndRendersRelativePath(t *testing.T) {
	workDir := t.TempDir()
	target := filepath.Join(workDir, "plan.yaml")
	if err := os.WriteFile(target, []byte("steps: []\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	policy := tool.NewPathPolicy(workDir, config.PathsConfig{})

	files, err := loadAdvisorFiles(workDir, &policy, []string{"plan.yaml"})
	if err != nil {
		t.Fatalf("loadAdvisorFiles() error = %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("len(files) = %d, want 1", len(files))
	}
	if files[0].DisplayPath != "plan.yaml" {
		t.Fatalf("DisplayPath = %q, want %q", files[0].DisplayPath, "plan.yaml")
	}
	if files[0].Content != "steps: []\n" {
		t.Fatalf("Content = %q, want file contents", files[0].Content)
	}
}

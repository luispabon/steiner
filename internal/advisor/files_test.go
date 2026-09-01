package advisor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
	"github.com/luispabon/steiner/internal/tool/builtin"
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
	if files[0].TotalBytes != len("steps: []\n") {
		t.Fatalf("TotalBytes = %d, want %d", files[0].TotalBytes, len("steps: []\n"))
	}
}

func TestLoadAdvisorFilesCapsReadWithoutLoadingWholeFile(t *testing.T) {
	workDir := t.TempDir()
	target := filepath.Join(workDir, "big.txt")
	realSize := maxAdvisorFileBytes + 5000
	if err := os.WriteFile(target, []byte(strings.Repeat("x", realSize)), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	policy := tool.NewPathPolicy(workDir, config.PathsConfig{})

	files, err := loadAdvisorFiles(workDir, &policy, []string{"big.txt"})
	if err != nil {
		t.Fatalf("loadAdvisorFiles() error = %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("len(files) = %d, want 1", len(files))
	}
	if len(files[0].Content) != maxAdvisorFileBytes {
		t.Fatalf("len(Content) = %d, want %d (capped read, not the full file)", len(files[0].Content), maxAdvisorFileBytes)
	}
	if files[0].TotalBytes != realSize {
		t.Fatalf("TotalBytes = %d, want %d (real on-disk size)", files[0].TotalBytes, realSize)
	}
}

// readToolMessage builds a provider.Message that mimics a "read" tool
// result, marshaling rr as its Content the same way a real read result
// would be encoded in the conversation snapshot.
func readToolMessage(t *testing.T, name string, rr builtin.ReadResult) provider.Message {
	t.Helper()
	data, err := json.Marshal(rr)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return provider.Message{
		Role:    provider.MessageRoleTool,
		Name:    name,
		Content: string(data),
	}
}

func TestFindFullFileReadHashNormalizesAbsoluteReadPathAgainstWorkDir(t *testing.T) {
	// read.go sets ReadResult.Path from an absolute path in the common
	// (non-sandboxed) case, while loadAdvisorFiles always relativizes
	// DisplayPath against workDir. findFullFileReadHash must normalize
	// rr.Path the same way before comparing, or dedup silently never fires.
	workDir := "/work/project"
	rr := builtin.ReadResult{
		Path: "/work/project/plan.yaml", StartLine: 1, EndLine: 1, TotalLines: 1, FileHash: "ABCD1234", Output: "steps: []\n",
	}
	snapshot := []provider.Message{readToolMessage(t, "read", rr)}

	hash, ok := findFullFileReadHash(snapshot, "plan.yaml", workDir)
	if !ok {
		t.Fatal("ok = false, want true (absolute read Path should normalize to match relative DisplayPath)")
	}
	if hash != rr.FileHash {
		t.Fatalf("hash = %q, want %q", hash, rr.FileHash)
	}
}

func TestFindFullFileReadHash(t *testing.T) {
	fullHash := "ABCD1234"
	tests := []struct {
		name     string
		snapshot []provider.Message
		path     string
		wantHash string
		wantOK   bool
	}{
		{
			name: "full match",
			snapshot: []provider.Message{
				readToolMessage(t, "read", builtin.ReadResult{
					Path: "a.go", StartLine: 1, EndLine: 50, TotalLines: 50, FileHash: fullHash, Output: "package a\n",
				}),
			},
			path:     "a.go",
			wantHash: fullHash,
			wantOK:   true,
		},
		{
			name: "partial range read is skipped",
			snapshot: []provider.Message{
				readToolMessage(t, "read", builtin.ReadResult{
					Path: "a.go", StartLine: 1, EndLine: 10, TotalLines: 50, FileHash: fullHash, Output: "package a\n",
				}),
			},
			path:   "a.go",
			wantOK: false,
		},
		{
			name: "wrong tool name is skipped",
			snapshot: []provider.Message{
				readToolMessage(t, "grep", builtin.ReadResult{
					Path: "a.go", StartLine: 1, EndLine: 50, TotalLines: 50, FileHash: fullHash, Output: "package a\n",
				}),
			},
			path:   "a.go",
			wantOK: false,
		},
		{
			name: "line-truncated output is skipped",
			snapshot: []provider.Message{
				readToolMessage(t, "read", builtin.ReadResult{
					Path: "a.go", StartLine: 1, EndLine: 50, TotalLines: 50, FileHash: fullHash,
					Output: "some line" + builtin.LineTruncationMarker,
				}),
			},
			path:   "a.go",
			wantOK: false,
		},
		{
			name: "missing file_hash still returns the empty hash (dedupeAgainstSnapshot fails safe on the compare)",
			snapshot: []provider.Message{
				readToolMessage(t, "read", builtin.ReadResult{
					Path: "a.go", StartLine: 1, EndLine: 50, TotalLines: 50, FileHash: "", Output: "package a\n",
				}),
			},
			path:     "a.go",
			wantHash: "",
			wantOK:   true,
		},
		{
			name:     "empty snapshot",
			snapshot: nil,
			path:     "a.go",
			wantOK:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hash, ok := findFullFileReadHash(tt.snapshot, tt.path, "")
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if ok && hash != tt.wantHash {
				t.Fatalf("hash = %q, want %q", hash, tt.wantHash)
			}
		})
	}
}

func TestDedupeAgainstSnapshot(t *testing.T) {
	content := "package a\n\nfunc A() {}\n"
	hash := builtin.FileContentHash([]byte(content))

	tests := []struct {
		name        string
		files       []advisorFile
		snapshot    []provider.Message
		wantDeduped bool
		wantContent string
	}{
		{
			name: "full match dedupes",
			files: []advisorFile{
				{DisplayPath: "a.go", Content: content, TotalBytes: len(content)},
			},
			snapshot: []provider.Message{
				readToolMessage(t, "read", builtin.ReadResult{
					Path: "a.go", StartLine: 1, EndLine: 3, TotalLines: 3, FileHash: hash, Output: content,
				}),
			},
			wantDeduped: true,
			wantContent: "",
		},
		{
			name: "stale hash does not dedupe",
			files: []advisorFile{
				{DisplayPath: "a.go", Content: content, TotalBytes: len(content)},
			},
			snapshot: []provider.Message{
				readToolMessage(t, "read", builtin.ReadResult{
					Path: "a.go", StartLine: 1, EndLine: 3, TotalLines: 3, FileHash: "STALE0000", Output: content,
				}),
			},
			wantDeduped: false,
			wantContent: content,
		},
		{
			name: "capped read skips dedup even with matching snapshot entry",
			files: []advisorFile{
				{DisplayPath: "big.go", Content: content, TotalBytes: maxAdvisorFileBytes + 1},
			},
			snapshot: []provider.Message{
				readToolMessage(t, "read", builtin.ReadResult{
					Path: "big.go", StartLine: 1, EndLine: 3, TotalLines: 3, FileHash: hash, Output: content,
				}),
			},
			wantDeduped: false,
			wantContent: content,
		},
		{
			name: "no matching snapshot entry",
			files: []advisorFile{
				{DisplayPath: "a.go", Content: content, TotalBytes: len(content)},
			},
			snapshot:    nil,
			wantDeduped: false,
			wantContent: content,
		},
		{
			name: "missing file_hash in snapshot fails safe (never matches a real content hash)",
			files: []advisorFile{
				{DisplayPath: "a.go", Content: content, TotalBytes: len(content)},
			},
			snapshot: []provider.Message{
				readToolMessage(t, "read", builtin.ReadResult{
					Path: "a.go", StartLine: 1, EndLine: 3, TotalLines: 3, FileHash: "", Output: content,
				}),
			},
			wantDeduped: false,
			wantContent: content,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := dedupeAgainstSnapshot(tt.files, tt.snapshot, "")
			if len(got) != 1 {
				t.Fatalf("len(files) = %d, want 1", len(got))
			}
			if got[0].Deduped != tt.wantDeduped {
				t.Fatalf("Deduped = %v, want %v", got[0].Deduped, tt.wantDeduped)
			}
			if got[0].Content != tt.wantContent {
				t.Fatalf("Content = %q, want %q", got[0].Content, tt.wantContent)
			}
		})
	}
}

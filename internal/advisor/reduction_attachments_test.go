package advisor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool/builtin"
)

func attachmentReadMessage(t *testing.T, callID, output string, adjust func(*builtin.ReadResult)) provider.Message {
	t.Helper()
	result := builtin.ReadResult{Path: "main.go", StartLine: 1, EndLine: 1, TotalLines: 1, Output: output}
	if adjust != nil {
		adjust(&result)
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return provider.Message{Role: provider.MessageRoleTool, Name: "read", ToolCallID: callID, Content: string(data)}
}

func TestDedupeAttachmentsRequiresStrictFullRead(t *testing.T) {
	workDir := t.TempDir()
	content := "package main\n"
	files := []advisorFile{{DisplayPath: "main.go", Content: content, TotalBytes: len(content)}}
	truncated := content + builtin.LineTruncationMarker
	image := builtin.ImageBlock{ID: "img", MediaType: "image/png", Data: "AAAA"}

	cases := []struct {
		name       string
		message    provider.Message
		stages     []reductionStage
		files      []advisorFile
		wantDedupe bool
	}{
		{
			name:       "full read dedupes",
			message:    attachmentReadMessage(t, "r1", content, nil),
			files:      files,
			wantDedupe: true,
		},
		{
			name:    "image result does not dedupe",
			message: attachmentReadMessage(t, "r1", content, func(r *builtin.ReadResult) { r.Image = &image }),
			files:   files,
		},
		{
			name:    "paginated read does not dedupe",
			message: attachmentReadMessage(t, "r1", content, func(r *builtin.ReadResult) { r.NextOffset = 2 }),
			files:   files,
		},
		{
			name:    "empty file read does not dedupe",
			message: attachmentReadMessage(t, "r1", "", func(r *builtin.ReadResult) { r.TotalLines = 0; r.EndLine = 0 }),
			files:   []advisorFile{{DisplayPath: "main.go", Content: "", TotalBytes: 0}},
		},
		{
			name:    "partial read does not dedupe",
			message: attachmentReadMessage(t, "r1", content, func(r *builtin.ReadResult) { r.StartLine = 2 }),
			files:   files,
		},
		{
			name:    "line range gap does not dedupe",
			message: attachmentReadMessage(t, "r1", content, func(r *builtin.ReadResult) { r.EndLine = 0 }),
			files:   files,
		},
		{
			name:    "truncation marker does not dedupe",
			message: attachmentReadMessage(t, "r1", truncated, nil),
			files:   []advisorFile{{DisplayPath: "main.go", Content: truncated, TotalBytes: len(truncated)}},
		},
		{
			name:    "reduced read does not dedupe",
			message: attachmentReadMessage(t, "r1", content, nil),
			stages:  []reductionStage{reductionStageClip},
			files:   files,
		},
		{
			name:    "capped file does not dedupe",
			message: attachmentReadMessage(t, "r1", content, nil),
			files:   []advisorFile{{DisplayPath: "main.go", Content: content, TotalBytes: maxAdvisorFileBytes + 1}},
		},
		{
			name:       "short stages still dedupe",
			message:    attachmentReadMessage(t, "r1", content, nil),
			stages:     []reductionStage{},
			files:      files,
			wantDedupe: true,
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got := dedupeAttachments(tt.files, []provider.Message{tt.message}, tt.stages, workDir)
			if got[0].Deduped != tt.wantDedupe {
				t.Fatalf("Deduped = %v, want %v", got[0].Deduped, tt.wantDedupe)
			}
			if !tt.wantDedupe && got[0].Content != tt.files[0].Content {
				t.Fatalf("content = %q, want untouched", got[0].Content)
			}
		})
	}
}

func TestDedupeAttachmentsMatchesEarlierReadsAndDuplicatePaths(t *testing.T) {
	workDir := t.TempDir()
	matching := "package main\n"
	other := "package other\n"

	t.Run("earlier matching read wins over a later different read", func(t *testing.T) {
		files := []advisorFile{{DisplayPath: "main.go", Content: matching, TotalBytes: len(matching)}}
		transcript := []provider.Message{
			attachmentReadMessage(t, "r1", matching, nil),
			attachmentReadMessage(t, "r2", other, nil),
		}
		got := dedupeAttachments(files, transcript, nil, workDir)
		if !got[0].Deduped {
			t.Fatal("matching earlier read did not dedupe the attachment")
		}
	})

	t.Run("duplicate paths only dedupe byte-equal content", func(t *testing.T) {
		files := []advisorFile{
			{DisplayPath: "main.go", Content: matching, TotalBytes: len(matching)},
			{DisplayPath: "main.go", Content: other, TotalBytes: len(other)},
		}
		got := dedupeAttachments(files, []provider.Message{attachmentReadMessage(t, "r1", matching, nil)}, nil, workDir)
		if !got[0].Deduped {
			t.Fatal("matching duplicate path was not deduped")
		}
		if got[1].Deduped || got[1].Content != other {
			t.Fatalf("non-matching duplicate path = %#v, want untouched", got[1])
		}
	})

	t.Run("single pass dedupes every matching duplicate path", func(t *testing.T) {
		files := []advisorFile{
			{DisplayPath: "main.go", Content: matching, TotalBytes: len(matching)},
			{DisplayPath: "main.go", Content: other, TotalBytes: len(other)},
		}
		transcript := []provider.Message{
			attachmentReadMessage(t, "r1", matching, nil),
			attachmentReadMessage(t, "r2", other, nil),
		}
		got := dedupeAttachments(files, transcript, nil, workDir)
		for i, file := range got {
			if !file.Deduped {
				t.Fatalf("file %d was not deduped", i)
			}
		}
	})
}

func TestAdvisorFilesRenderedLenMatchesRender(t *testing.T) {
	big := strings.Repeat("a", maxAdvisorFileBytes+100)
	files := []advisorFile{
		{DisplayPath: "small.go", Content: "package small\n", TotalBytes: len("package small\n")},
		{DisplayPath: "big.go", Content: big, TotalBytes: len(big) + 500},
		{DisplayPath: "dup.go", Deduped: true},
	}
	many := make([]advisorFile, 0, maxAdvisorFiles)
	for i := 0; i < maxAdvisorFiles; i++ {
		many = append(many, advisorFile{DisplayPath: fmt.Sprintf("f%d.go", i), Content: strings.Repeat("b", 20000), TotalBytes: 20000})
	}
	for _, caseFiles := range [][]advisorFile{files, many, nil} {
		if got, want := advisorFilesRenderedLen(caseFiles), len(renderAdvisorFiles(caseFiles)); got != want {
			t.Fatalf("advisorFilesRenderedLen() = %d, want %d", got, want)
		}
	}
}

func TestPlanReductionProtectsMutateReceipts(t *testing.T) {
	huge := strings.Repeat("mutation receipt\n", 400)
	bigBash := func(callID string) provider.Message {
		data, err := json.Marshal(builtin.BashResult{ArtifactStatus: "none", Output: strings.Repeat("z", 8000)})
		if err != nil {
			t.Fatalf("json.Marshal() error = %v", err)
		}
		return provider.Message{Role: provider.MessageRoleTool, Name: "bash", ToolCallID: callID, Content: string(data)}
	}
	mutate := func(callID, name string) provider.Message {
		return provider.Message{Role: provider.MessageRoleTool, Name: name, ToolCallID: callID, Content: huge}
	}
	transcript := []provider.Message{
		{Role: provider.MessageRoleUser, Content: "apply the edits"},
		{Role: provider.MessageRoleAssistant, ToolCalls: []provider.ToolCall{
			{ID: "b1", Name: "bash"}, {ID: "m1", Name: "mutate"}, {ID: "m2", Name: "mutate"},
			{ID: "b2", Name: "bash"}, {ID: "m3", Name: "mutate"},
		}},
		bigBash("b1"),
		mutate("m1", ""),
		mutate("m2", "mutate"),
		mutate("", "mutate"),
		bigBash("b2"),
		mutate("m3", "mutate"),
	}
	plan := planReduction(reductionPlanInput{
		transcript: transcript, stages: make([]reductionStage, len(transcript)),
		tuning: reductionTuning{enabled: true, trigger: 1000, target: 0, tail: 1000},
		ratio:  0.25, initialTokens: 10000,
	})
	for _, index := range []int{3, 4, 5, 7} {
		if plan.transcript[index].Content != huge || plan.stages[index] != reductionStageNone {
			t.Fatalf("mutate receipt %d changed: stage=%v content=%q", index, plan.stages[index], plan.transcript[index].Content)
		}
	}
	if plan.stages[2] == reductionStageNone {
		t.Fatal("ordinary control result older than the protected tail was not reduced")
	}
}

func TestReductionDisabledKeepsLegacyPayload(t *testing.T) {
	workDir, policy := newAdvisorTestPolicy(t)
	content := "package main\n\nfunc main() {}\n"
	if err := os.WriteFile(filepath.Join(workDir, "main.go"), []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	snapshot := []provider.Message{
		{Role: provider.MessageRoleUser, Content: "review the change"},
		assistantCall("r1", "read"),
		attachmentReadMessage(t, "r1", content, func(r *builtin.ReadResult) {
			r.EndLine = 3
			r.TotalLines = 3
			r.FileHash = builtin.FileContentHash([]byte(content))
		}),
	}
	files, err := loadAdvisorFiles(workDir, policy, []string{"main.go"})
	if err != nil {
		t.Fatalf("loadAdvisorFiles() error = %v", err)
	}
	deps := HandlerDeps{Model: provider.ResolvedModel{BackendModelID: "advisor-model"}, WorkDir: workDir, PathPolicy: policy}
	req := reductionRequest{snapshot: snapshot, files: files, question: "check it"}
	state := &handlerState{shared: NewSharedState(), cacheKey: "legacy-disabled"}
	got := state.reduceAdvisorMessages(context.Background(), deps, req)
	_, want := legacyReductionPayload(deps, req)
	if !messagesEqual(got.messages, want) {
		t.Fatal("disabled reduction payload differs from the legacy payload")
	}
	if files[0].Deduped {
		t.Fatal("legacy dedupe mutated the caller's loaded files")
	}
}

func TestSplitTrailingAssistantTrimsOnlyInFlightText(t *testing.T) {
	snapshot := []provider.Message{
		{Role: provider.MessageRoleUser, Content: "fix it"},
		{Role: provider.MessageRoleAssistant, Content: "  note \n\t"},
	}
	base, inFlight := splitTrailingAssistant(snapshot)
	if len(base) != 1 || base[0].Content != "fix it" {
		t.Fatalf("base transcript = %#v, want the user message", base)
	}
	if inFlight == nil || inFlight.Content != "  note \n\t" {
		t.Fatalf("in-flight message = %#v, want the untrimmed assistant message", inFlight)
	}
	messages := buildMessages(snapshot, "", nil)
	if len(messages) != 3 {
		t.Fatalf("len(messages) = %d, want 3", len(messages))
	}
	want := inFlightLabel + "note\n\n" + advisorUserPrompt
	if got := messages[len(messages)-1].Content; got != want {
		t.Fatalf("final user message = %q, want %q", got, want)
	}
}

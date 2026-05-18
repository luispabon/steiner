package output

import (
	"encoding/json"
	"strings"
)

func buildApplyPatchPreview(result string) ToolPreview {
	// Always return ToolPreviewKindPatch so bodyKind is set even before the
	// tool finishes (start-time call has an empty result string).
	return buildPatchPreview(result, patchPreviewSpec{})
}

func buildMutatePreview(result string) ToolPreview {
	return buildPatchPreview(result, patchPreviewSpec{
		addedField:   func(payload patchPreviewPayload) []string { return payload.Created },
		appliedCount: func(payload patchPreviewPayload) int { return payload.OperationsApplied },
		failedCount:  func(payload patchPreviewPayload) int { return payload.OperationsFailed },
	})
}

type patchPreviewMove struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type patchPreviewPayload struct {
	Added             []string           `json:"added"`
	Created           []string           `json:"created"`
	Modified          []string           `json:"modified"`
	Deleted           []string           `json:"deleted"`
	Moved             []patchPreviewMove `json:"moved"`
	HunksApplied      int                `json:"hunks_applied"`
	HunksFailed       int                `json:"hunks_failed"`
	OperationsApplied int                `json:"operations_applied"`
	OperationsFailed  int                `json:"operations_failed"`
}

type patchPreviewSpec struct {
	addedField   func(patchPreviewPayload) []string
	appliedCount func(patchPreviewPayload) int
	failedCount  func(patchPreviewPayload) int
}

func buildPatchPreview(result string, spec patchPreviewSpec) ToolPreview {
	preview := ToolPreview{Kind: ToolPreviewKindPatch}
	if strings.TrimSpace(result) == "" {
		return preview
	}
	payload := patchPreviewPayload{}
	if err := json.Unmarshal([]byte(result), &payload); err != nil {
		return preview
	}
	if spec.addedField == nil {
		spec.addedField = func(payload patchPreviewPayload) []string { return payload.Added }
	}
	if spec.appliedCount == nil {
		spec.appliedCount = func(payload patchPreviewPayload) int { return payload.HunksApplied }
	}
	if spec.failedCount == nil {
		spec.failedCount = func(payload patchPreviewPayload) int { return payload.HunksFailed }
	}
	preview.PatchAdded = spec.addedField(payload)
	preview.PatchModified = payload.Modified
	preview.PatchDeleted = payload.Deleted
	preview.PatchMoved = buildPatchMoves(payload.Moved)
	preview.HunksApplied = spec.appliedCount(payload)
	preview.HunksFailed = spec.failedCount(payload)
	return preview
}

func buildPatchMoves(moves []patchPreviewMove) []ToolPreviewPatchMove {
	previewMoves := make([]ToolPreviewPatchMove, 0, len(moves))
	for _, move := range moves {
		previewMoves = append(previewMoves, ToolPreviewPatchMove(move))
	}
	return previewMoves
}

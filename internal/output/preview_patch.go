package output

import (
	"encoding/json"
	"strings"
)

func buildApplyPatchPreview(result string) ToolPreview {
	// Always return ToolPreviewKindPatch so bodyKind is set even before the
	// tool finishes (start-time call has an empty result string).
	preview := ToolPreview{Kind: ToolPreviewKindPatch}
	if strings.TrimSpace(result) == "" {
		return preview
	}
	var payload struct {
		Added    []string `json:"added"`
		Modified []string `json:"modified"`
		Deleted  []string `json:"deleted"`
		Moved    []struct {
			From string `json:"from"`
			To   string `json:"to"`
		} `json:"moved"`
		HunksApplied int `json:"hunks_applied"`
		HunksFailed  int `json:"hunks_failed"`
	}
	if err := json.Unmarshal([]byte(result), &payload); err != nil {
		return preview
	}
	moved := make([]ToolPreviewPatchMove, 0, len(payload.Moved))
	for _, m := range payload.Moved {
		moved = append(moved, ToolPreviewPatchMove{From: m.From, To: m.To})
	}
	preview.PatchAdded = payload.Added
	preview.PatchModified = payload.Modified
	preview.PatchDeleted = payload.Deleted
	preview.PatchMoved = moved
	preview.HunksApplied = payload.HunksApplied
	preview.HunksFailed = payload.HunksFailed
	return preview
}

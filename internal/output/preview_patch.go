package output

func buildMutatePreview(arguments map[string]any, result string) ToolPreview {
	return buildMutatePreviewImpl(arguments, result)
}

type patchPreviewMove struct {
	From string `json:"from"`
	To   string `json:"to"`
}

func buildPatchMoves(moves []patchPreviewMove) []ToolPreviewPatchMove {
	previewMoves := make([]ToolPreviewPatchMove, 0, len(moves))
	for _, move := range moves {
		previewMoves = append(previewMoves, ToolPreviewPatchMove(move))
	}
	return previewMoves
}

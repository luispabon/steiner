// Package patchdoc holds the Codex-style patch document scaffolding.
package patchdoc

type Patch struct {
	Hunks []Hunk
}

type Hunk interface {
	hunk()
	Path() string
	AffectedPath() string
}

type AddFile struct {
	PathValue string
	Contents  string
}

func (AddFile) hunk() {}

func (h AddFile) Path() string {
	return h.PathValue
}

func (h AddFile) AffectedPath() string {
	return h.PathValue
}

type DeleteFile struct {
	PathValue string
}

func (DeleteFile) hunk() {}

func (h DeleteFile) Path() string {
	return h.PathValue
}

func (h DeleteFile) AffectedPath() string {
	return h.PathValue
}

type UpdateFile struct {
	PathValue string
	MovePath  string
	Chunks    []UpdateFileChunk
}

func (UpdateFile) hunk() {}

func (h UpdateFile) Path() string {
	return h.PathValue
}

func (h UpdateFile) AffectedPath() string {
	if h.MovePath != "" {
		return h.MovePath
	}
	return h.PathValue
}

type UpdateFileChunk struct {
	ChangeContext string
	HasContext    bool
	OldLines      []string
	NewLines      []string
	EndOfFile     bool
}

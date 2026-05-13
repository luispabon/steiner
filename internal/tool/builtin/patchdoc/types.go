// Package patchdoc holds the Codex-style patch document scaffolding.
package patchdoc

// Patch groups the file-level hunks that make up one patch document.
type Patch struct {
	Hunks []Hunk
}

// Hunk describes one file-level patch operation.
type Hunk interface {
	hunk()
	Path() string
	AffectedPath() string
}

// AddFile adds a new file with the provided contents.
type AddFile struct {
	PathValue string
	Contents  string
}

func (AddFile) hunk() {}

// Path returns the source path referenced by the hunk.
func (h AddFile) Path() string {
	return h.PathValue
}

// AffectedPath returns the path affected by the hunk.
func (h AddFile) AffectedPath() string {
	return h.PathValue
}

// DeleteFile deletes an existing file.
type DeleteFile struct {
	PathValue string
}

func (DeleteFile) hunk() {}

// Path returns the source path referenced by the hunk.
func (h DeleteFile) Path() string {
	return h.PathValue
}

// AffectedPath returns the path affected by the hunk.
func (h DeleteFile) AffectedPath() string {
	return h.PathValue
}

// UpdateFile updates an existing file and may move it.
type UpdateFile struct {
	PathValue string
	MovePath  string
	Chunks    []UpdateFileChunk
}

func (UpdateFile) hunk() {}

// Path returns the source path referenced by the hunk.
func (h UpdateFile) Path() string {
	return h.PathValue
}

// AffectedPath returns the final path affected by the hunk.
func (h UpdateFile) AffectedPath() string {
	if h.MovePath != "" {
		return h.MovePath
	}
	return h.PathValue
}

// UpdateFileChunk describes one textual change chunk within an updated file.
type UpdateFileChunk struct {
	ChangeContext string
	HasContext    bool
	OldLines      []string
	NewLines      []string
	EndOfFile     bool
}

package tui

import (
	"testing"
	"unsafe"
)

// modelSizeThreshold bounds the memory Bubble Tea copies by value on every
// rendered frame and every dispatched event, since Model.View and
// Model.Update are value receivers. Large by-value fields (especially
// theme.Styles, previously embedded by value in ~15 sub-components) must be
// shared by pointer instead of re-embedded, or every frame pays for a fresh
// copy of the whole struct.
const modelSizeThreshold = 65536

func TestModelSizeStaysBounded(t *testing.T) {
	size := unsafe.Sizeof(Model{})
	if size >= modelSizeThreshold {
		t.Errorf("unsafe.Sizeof(Model{}) = %d, want < %d; Model is copied by value on every "+
			"frame and every event (Model.View/Model.Update are value receivers), so large "+
			"by-value fields must not be re-embedded — share them by pointer instead "+
			"(e.g. theme.Styles as *theme.Styles)", size, modelSizeThreshold)
	}
}

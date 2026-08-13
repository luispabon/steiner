package tui

import (
	"testing"
	"unsafe"
)

// modelSizeThreshold bounds the Model struct size. Model.View and Model.Update
// use pointer receivers, so the struct is no longer copied on every rendered
// frame and every dispatched event, but large by-value fields (especially
// theme.Styles, previously embedded by value in ~15 sub-components) must still
// be shared by pointer instead of re-embedded: any remaining by-value copy
// (test helpers, Bubble Tea internals) would pay for the whole struct.
const modelSizeThreshold = 65536

func TestModelSizeStaysBounded(t *testing.T) {
	size := unsafe.Sizeof(Model{})
	if size >= modelSizeThreshold {
		t.Errorf("unsafe.Sizeof(Model{}) = %d, want < %d; Model is copied by value whenever a "+
			"by-value copy happens (test helpers, value returns), so large "+
			"by-value fields must not be re-embedded — share them by pointer instead "+
			"(e.g. theme.Styles as *theme.Styles)", size, modelSizeThreshold)
	}
}

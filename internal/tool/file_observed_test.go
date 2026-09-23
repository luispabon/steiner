package tool

import (
	"context"
	"testing"
)

func TestFileReadLookupContextRoundTrip(t *testing.T) {
	want := FileReadState{Observed: true, StartLine: 2, EndLine: 4, TotalLines: 9, TurnsSinceRead: 3}
	ctx := WithFileReadLookup(context.Background(), func(path string) FileReadState {
		if path != "a.go" {
			t.Errorf("lookup path = %q, want a.go", path)
		}
		return want
	})
	lookup := FileReadLookupFromContext(ctx)
	if lookup == nil {
		t.Fatal("FileReadLookupFromContext() = nil, want lookup")
	}
	if got := lookup("a.go"); got != want {
		t.Errorf("lookup state = %+v, want %+v", got, want)
	}
}

func TestFileReadLookupFromContextAbsent(t *testing.T) {
	if lookup := FileReadLookupFromContext(context.Background()); lookup != nil {
		t.Errorf("FileReadLookupFromContext() = %v, want nil when absent", lookup)
	}
}

package tui

import (
	"reflect"
	"strings"
	"testing"
	"unsafe"

	"github.com/luispabon/steiner/internal/tui/theme"
)

// TestRenderStatusCachesOnIdenticalInput proves the memoized wrapper returns
// the exact same cached string object, rather than a freshly recomputed one,
// when statusState and width are unchanged between calls. Comparing the
// backing data pointer (not just content equality, which a fresh render
// would also satisfy since view() is deterministic) is what distinguishes an
// actual cache hit from a coincidentally-identical recomputation.
func TestRenderStatusCachesOnIdenticalInput(t *testing.T) {
	t.Parallel()
	m := &Model{status: statusState{model: "gpt", styles: testStyles(theme.AccentAmber)}}

	first := m.renderStatus(80)
	second := m.renderStatus(80)
	if unsafe.StringData(first) != unsafe.StringData(second) {
		t.Fatalf("renderStatus recomputed instead of returning the cached string\nfirst:  %q\nsecond: %q", first, second)
	}
	if !m.statusViewCacheSet {
		t.Fatal("statusViewCacheSet should be true after a render")
	}
}

// TestRenderStatusNeverServesStale walks every field of statusState,
// mutates it in turn, and asserts the memoized wrapper's output matches a
// fresh direct call to statusState.view. This is a differential check
// rather than a "must differ" check: some fields (e.g. streaming, mode,
// context) are not read by view() at all, so asserting the output changes
// for every field would be false. What must hold for every field, used or
// not, is that the memo never serves a render that doesn't match what a
// fresh call would produce.
func TestRenderStatusNeverServesStale(t *testing.T) {
	t.Parallel()
	styles := testStyles(theme.AccentAmber)
	base := statusState{
		model:          "gpt",
		context:        "ctx 10/100",
		mode:           "running",
		execMode:       "build",
		styles:         styles,
		streaming:      false,
		approvalActive: false,
		promptUsed:     10,
		contextBudget:  100,
		oneshotPhase:   "plan",
		sandboxStatus:  "active",
	}

	typ := reflect.TypeOf(base)
	fieldsWalked := 0
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if field.Name == "styles" {
			// styles is a shared pointer, not an independently variable input
			// here; changing it is exercised by the accent-change path
			// elsewhere. Mutating it to a different pointer is still safe to
			// skip: the cache key comparison would catch a stale pointer too.
			continue
		}
		fieldsWalked++

		t.Run(field.Name, func(t *testing.T) {
			m := &Model{status: base}
			m.renderStatus(80) // warm the cache with the base value

			mutated := mutateStatusField(t, base, field)
			m.status = mutated

			got := m.renderStatus(80)
			want := mutated.view(80)
			if got != want {
				t.Errorf("renderStatus served a stale render after mutating field %q\ngot:  %q\nwant: %q", field.Name, got, want)
			}
		})
	}

	// Positive control: this test is only meaningful if it actually walked
	// fields. If a future refactor makes statusState's fields inaccessible to
	// reflection (or the type changes shape), this catches the walk silently
	// covering zero fields.
	if fieldsWalked == 0 {
		t.Fatal("field walk covered zero fields; test is vacuous")
	}
}

// mutateStatusField returns a copy of base with the named field changed to a
// value distinct from base's, so the copy is guaranteed to produce a
// different cache key.
func mutateStatusField(t *testing.T, base statusState, field reflect.StructField) statusState {
	t.Helper()
	mutated := base
	raw := reflect.ValueOf(&mutated).Elem().FieldByName(field.Name)
	// FieldByName on an unexported field returns a read-only Value even from
	// within the declaring package; reflect.NewAt over its address bypasses
	// that so the walk can mutate every field generically.
	v := reflect.NewAt(raw.Type(), unsafe.Pointer(raw.UnsafeAddr())).Elem()
	switch v.Kind() {
	case reflect.String:
		v.SetString(v.String() + "-mutated")
	case reflect.Bool:
		v.SetBool(!v.Bool())
	case reflect.Int:
		v.SetInt(v.Int() + 1)
	default:
		t.Fatalf("mutateStatusField: unhandled kind %s for field %q; extend this test", v.Kind(), field.Name)
	}
	return mutated
}

func TestStatusBarRendersPhaseSegment(t *testing.T) {
	t.Parallel()
	s := statusState{oneshotPhase: "plan", styles: testStyles(theme.AccentAmber)}
	result := s.view(80)
	if !strings.Contains(result, "phase") || !strings.Contains(result, "plan") {
		t.Errorf("status bar should contain 'phase' and 'plan', got: %s", result)
	}
}

func TestStatusBarOmitsPhaseWhenEmpty(t *testing.T) {
	t.Parallel()
	s := statusState{oneshotPhase: "", styles: testStyles(theme.AccentAmber)}
	result := s.view(80)
	if strings.Contains(result, "phase ·") {
		t.Errorf("status bar should not contain 'phase ·', got: %s", result)
	}
}

func TestRenderModeBadge(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		mode  string
		want  string
		empty bool
	}{
		{name: "plan", mode: "plan", want: "plan "},
		{name: "build", mode: "build", want: "build"},
		{name: "unset", mode: "", empty: true},
		{name: "unrecognized", mode: "bogus", empty: true},
	}
	styles := testStyles(theme.AccentAmber)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := renderModeBadge(styles, tc.mode)
			if tc.empty {
				if got != "" {
					t.Errorf("renderModeBadge(%q) = %q, want empty", tc.mode, got)
				}
				return
			}
			if !strings.Contains(got, tc.want) {
				t.Errorf("renderModeBadge(%q) = %q, want to contain %q", tc.mode, got, tc.want)
			}
		})
	}
}

func TestStatusBarRendersModeBadge(t *testing.T) {
	t.Parallel()
	styles := testStyles(theme.AccentAmber)
	s := statusState{execMode: "plan", styles: styles}
	result := s.view(120)
	if !strings.Contains(result, "plan") {
		t.Errorf("status bar should contain mode badge 'plan', got: %s", result)
	}
}

func TestStatusBarOmitsModeBadgeWhenUnset(t *testing.T) {
	t.Parallel()
	styles := testStyles(theme.AccentAmber)
	s := statusState{execMode: "", styles: styles}
	result := s.view(120)
	// When execMode is unset, renderModeBadge returns "" so no styled mode
	// label appears. "plan " (with trailing space) guards against the badge;
	// the bare word "plan" may legitimately appear in the oneshot phase segment.
	if strings.Contains(result, "plan ") || strings.Contains(result, "build") {
		t.Errorf("status bar should not contain mode badge when unset, got: %s", result)
	}
}

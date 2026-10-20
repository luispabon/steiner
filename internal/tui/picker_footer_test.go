package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/tui/theme"
)

// Room for every visible row so the footer is never clipped by the overlay.
const (
	pickerFooterWidth  = 120
	pickerFooterHeight = 60
)

func pickerFooterNames(n int) []string {
	names := make([]string, n)
	for i := range names {
		names[i] = fmt.Sprintf("item-%02d", i)
	}
	return names
}

// TestPickerFooterReportsRemainingItems renders each picker with more items
// than it shows and checks the shared "… and N more" footer. Every overlay is
// rendered at scroll position 0, so N is total minus the visible row limit.
func TestPickerFooterReportsRemainingItems(t *testing.T) {
	t.Parallel()
	styles := testStyles("#ff0000")
	cases := []struct {
		name   string
		limit  int
		total  int
		render func(t *testing.T) string
	}{
		{
			name:  "accent picker",
			limit: maxDisplay,
			// Open appends "random" to the accent presets.
			total: len(theme.SortedAccentPresetNames()) + 1,
			render: func(*testing.T) string {
				p := newAccentPickerOverlay(styles)
				p.width, p.height = pickerFooterWidth, pickerFooterHeight
				return p.Open("").View()
			},
		},
		{
			name:  "model picker",
			limit: maxDisplay,
			total: 12,
			render: func(*testing.T) string {
				m := newModelPickerOverlay(styles)
				m.width, m.height = pickerFooterWidth, pickerFooterHeight
				return m.Open(pickerFooterNames(12), "").View()
			},
		},
		{
			name:  "plan picker",
			limit: maxDisplay,
			total: 11,
			render: func(*testing.T) string {
				p := newPlanPickerOverlay(styles)
				p.width, p.height = pickerFooterWidth, pickerFooterHeight
				// Open reads .steiner/plans from the process cwd, so set the
				// candidate list directly and open the shell.
				names := pickerFooterNames(11)
				p.OverlayShell = p.openShell()
				p.allNames = names
				p.candidates = append([]string(nil), names...)
				p.matchIndexes = make([][]int, len(names))
				return p.View()
			},
		},
		{
			name:  "profile picker",
			limit: maxDisplay,
			total: 12,
			render: func(*testing.T) string {
				p := newProfilePickerOverlay(styles)
				p.width, p.height = pickerFooterWidth, pickerFooterHeight
				return p.Open(pickerFooterNames(12), "").View()
			},
		},
		{
			name:  "slash overlay",
			limit: slashOverlayMaxDisplay,
			total: 14,
			render: func(*testing.T) string {
				s := newSlashOverlay(styles)
				s.width, s.height = pickerFooterWidth, pickerFooterHeight
				items := make([]slashOverlayItem, 14)
				for i := range items {
					items[i] = slashOverlayItem{command: fmt.Sprintf("/cmd-%02d", i), name: "Command", desc: "does things"}
				}
				return s.Open(items).View()
			},
		},
		{
			name: "file list",
			// The file list shows 30 rows, a local constant in its View.
			limit: 30,
			total: 35,
			render: func(t *testing.T) string {
				root := t.TempDir()
				for i := range 35 {
					if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("file-%02d.txt", i)), nil, 0o600); err != nil {
						t.Fatal(err)
					}
				}
				f := newFileListOverlay(styles)
				f.width, f.height = pickerFooterWidth, pickerFooterHeight
				return f.Open(root).View()
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if tc.total <= tc.limit {
				t.Fatalf("fixture has %d items, want more than %d visible", tc.total, tc.limit)
			}
			view := stripANSI(tc.render(t))
			want := fmt.Sprintf("… and %d more", tc.total-tc.limit)
			if !strings.Contains(view, want) {
				t.Errorf("view missing footer %q:\n%s", want, view)
			}
		})
	}
}

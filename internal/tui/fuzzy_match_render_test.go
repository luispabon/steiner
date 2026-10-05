package tui

import (
	"image/color"
	"math/rand"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/luispabon/steiner/internal/tui/theme"
)

// renderMatchedTextPerRune is the reference implementation: one styled
// segment per rune.
func renderMatchedTextPerRune(text string, matchedIndexes []int, baseStyle lipgloss.Style, matchedColor color.Color) string {
	matched := make(map[int]struct{}, len(matchedIndexes))
	for _, idx := range matchedIndexes {
		matched[idx] = struct{}{}
	}
	hl := lipgloss.NewStyle().Bold(true).Underline(true).Foreground(lipgloss.Color(theme.ColorHex(matchedColor)))
	var b strings.Builder
	for idx, r := range text {
		if _, ok := matched[idx]; ok {
			b.WriteString(hl.Render(string(r)))
			continue
		}
		b.WriteString(baseStyle.Render(string(r)))
	}
	return b.String()
}

func cellGrid(s string, width int) []*uv.Cell {
	scr := uv.NewScreenBuffer(width, 1)
	uv.NewStyledString(s).Draw(scr, uv.Rect(0, 0, width, 1))
	cells := make([]*uv.Cell, width)
	for x := range cells {
		cells[x] = scr.CellAt(x, 0)
	}
	return cells
}

func firstCellDiff(old, got string) (int, bool) {
	width := ansi.StringWidth(old) + 1
	oldCells, gotCells := cellGrid(old, width), cellGrid(got, width)
	for x := range oldCells {
		o, g := oldCells[x], gotCells[x]
		if (o == nil) != (g == nil) || (o != nil && !o.Equal(g)) {
			return x, true
		}
	}
	return 0, false
}

func assertCellIdentical(t *testing.T, old, got string) {
	t.Helper()
	if a, b := ansi.Strip(old), ansi.Strip(got); a != b {
		t.Fatalf("stripped text differs: old %q new %q", a, b)
	}
	if x, differs := firstCellDiff(old, got); differs {
		t.Fatalf("cell %d differs\nold %q\nnew %q", x, old, got)
	}
}

func TestRenderMatchedTextCellIdentical(t *testing.T) {
	accent := lipgloss.Color("#d97757")
	styles := map[string]lipgloss.Style{
		"fg":     lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Fg)),
		"fgmute": lipgloss.NewStyle().Foreground(lipgloss.Color(theme.FgMute)),
		"accent": lipgloss.NewStyle().Foreground(accent),
	}
	alphabet := []rune("abcXYZ09 _-/.\u00e9\u00fc\u4e16\u754c\U0001F600\U0001F680")

	randomText := func(rng *rand.Rand) string {
		n := 1 + rng.Intn(24)
		var sb strings.Builder
		for range n {
			sb.WriteRune(alphabet[rng.Intn(len(alphabet))])
		}
		return sb.String()
	}
	byteOffsets := func(text string) []int {
		var offs []int
		for i := range text {
			offs = append(offs, i)
		}
		return offs
	}

	type tc struct {
		name    string
		text    string
		indexes []int
	}
	cases := []tc{
		{"empty", "", nil},
		{"none matched", "hello world", nil},
		{"all matched", "hello world", byteOffsets("hello world")},
		{"adjacent", "hello world", []int{0, 1, 2}},
		{"separated", "hello world", []int{0, 4, 10}},
		{"spaces in run", "a b c d", []int{1, 2, 3}},
		{"leading and trailing spaces", " ab ", []int{0, 3}},
		{"multibyte", "h\u00e9llo \u4e16\u754c", []int{1, 7, 10}},
		{"emoji", "go \U0001F680 now \U0001F600", []int{3, 12}},
		{"unsorted", "hello", []int{3, 1, 0}},
		{"out of range and duplicates", "abc", []int{1, 1, 99}},
	}
	rng := rand.New(rand.NewSource(42))
	for range 200 {
		text := randomText(rng)
		offs := byteOffsets(text)
		var picked []int
		for _, o := range offs {
			if rng.Intn(3) == 0 {
				picked = append(picked, o)
			}
		}
		cases = append(cases, tc{name: "random", text: text, indexes: picked})
	}

	for sname, style := range styles {
		for _, c := range cases {
			t.Run(sname+"/"+c.name, func(t *testing.T) {
				old := renderMatchedTextPerRune(c.text, c.indexes, style, accent)
				got := renderMatchedText(c.text, c.indexes, style, accent)
				assertCellIdentical(t, old, got)
			})
		}
	}
}

func TestCellComparisonDetectsAttributeLoss(t *testing.T) {
	accent := lipgloss.Color("#d97757")
	base := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Fg))
	want := renderMatchedTextPerRune("a b", []int{0, 1, 2}, base, accent)

	tests := []struct {
		name string
		got  string
	}{
		{"underline dropped", lipgloss.NewStyle().Bold(true).Foreground(accent).Render("a b")},
		{"bold dropped", lipgloss.NewStyle().Underline(true).Foreground(accent).Render("a b")},
		{"fg changed", lipgloss.NewStyle().Bold(true).Underline(true).Foreground(lipgloss.Color("#00ff00")).Render("a b")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, differs := firstCellDiff(want, tt.got); !differs {
				t.Fatal("cell comparison did not notice the difference")
			}
		})
	}
}

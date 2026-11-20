package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

const (
	// tokenStatsDocumentMargin is the horizontal margin glamour's dark style
	// sheet puts around a report body: one indent column plus a matching right
	// gutter.
	tokenStatsDocumentMargin = 2
	// tokenStatsCellPadding is the horizontal padding glamour puts around every
	// table cell: one column on each side.
	tokenStatsCellPadding = 2
)

// tokenStatsTableLimit returns the width glamour allots a markdown table in a
// report rendered at reportWidth: the wrap width renderMarkdownBlock asks for,
// less the document margin on both sides. A table whose contents fit the limit
// renders whole; a wider one is shrunk cell by cell.
func tokenStatsTableLimit(reportWidth int) int {
	return max(0, reportWidth-markdownRenderPadding-2*tokenStatsDocumentMargin)
}

// tokenStatsOptionalColumn names a table column that can be dropped when the
// tables do not fit the report width. The zero value marks a column that is
// always shown.
type tokenStatsOptionalColumn int

const (
	tokenStatsRequired tokenStatsOptionalColumn = iota
	tokenStatsOptionalReason
	tokenStatsOptionalWrite
	tokenStatsOptionalHit
)

// tokenStatsColumn describes one table column.
type tokenStatsColumn struct {
	header   string
	optional tokenStatsOptionalColumn
	right    bool
	// flex marks the Model column: its cells are shortened when even the
	// narrowest column set needs more room than the report has.
	flex bool
}

// tokenStatsColumnSet is the set of optional columns one report keeps. Columns
// marked required are always shown.
type tokenStatsColumnSet struct {
	reason bool
	write  bool
	hit    bool
}

// tokenStatsColumnSets lists the candidate sets widest first: Reason is dropped
// before Write, and Write before Hit, so Cost and the count columns keep their
// place.
var tokenStatsColumnSets = []tokenStatsColumnSet{
	{reason: true, write: true, hit: true},
	{write: true, hit: true},
	{hit: true},
	{},
}

// keeps reports whether the set shows a column.
func (s tokenStatsColumnSet) keeps(optional tokenStatsOptionalColumn) bool {
	switch optional {
	case tokenStatsOptionalReason:
		return s.reason
	case tokenStatsOptionalWrite:
		return s.write
	case tokenStatsOptionalHit:
		return s.hit
	default:
		return true
	}
}

// tokenStatsRow is one table row: plain-text cells in column order. bold
// renders the first cell as markdown strong text, which the Total row uses.
type tokenStatsRow struct {
	cells []string
	bold  bool
}

// tokenStatsTable is one table: its full column list and its rows. A report's
// plan decides which optional columns are written.
type tokenStatsTable struct {
	columns []tokenStatsColumn
	rows    []tokenStatsRow
}

// tokenStatsPlan is the column set and Model cell width one report renders all
// of its tables with. A zero modelWidth leaves Model cells whole.
type tokenStatsPlan struct {
	columns    tokenStatsColumnSet
	modelWidth int
}

// keptColumns returns the indices of the table's columns the plan keeps.
func (p tokenStatsPlan) keptColumns(columns []tokenStatsColumn) []int {
	kept := make([]int, 0, len(columns))
	for i, column := range columns {
		if p.columns.keeps(column.optional) {
			kept = append(kept, i)
		}
	}
	return kept
}

// cell returns one cell's rendered text, shortened when a Model cell needs more
// room than the plan gives it.
func (p tokenStatsPlan) cell(column tokenStatsColumn, value string) string {
	if p.modelWidth <= 0 || !column.flex {
		return value
	}
	return ansi.Truncate(value, p.modelWidth, "…")
}

// planTokenStatsTables picks the widest column set whose tables all fit limit.
// A table fits when its printed width, the cell contents plus one padding
// column on each side and one separator between columns, stays within limit.
// When even the narrowest set overflows, Model cells are shortened to the room
// the other columns leave so Cost keeps its place; below the width those other
// columns need, the tables still overflow and glamour shrinks them.
func planTokenStatsTables(tables []tokenStatsTable, limit int) tokenStatsPlan {
	var plan tokenStatsPlan
	for _, set := range tokenStatsColumnSets {
		plan = tokenStatsPlan{columns: set}
		if width, _ := tokenStatsTablesFit(tables, plan); width <= limit {
			return plan
		}
	}
	_, fixedWidth := tokenStatsTablesFit(tables, plan)
	plan.modelWidth = max(1, limit-fixedWidth)
	return plan
}

// tokenStatsTablesFit returns the printed width of the widest table for the
// plan's columns, and, over the tables that have a Model column, the widest of
// them with that column's content left out. Tables without a Model column (the
// by-source one) do not bound the Model cell width: shortening it cannot make
// them fit.
func tokenStatsTablesFit(tables []tokenStatsTable, plan tokenStatsPlan) (width, fixedWidth int) {
	for _, table := range tables {
		tableWidth, tableFixedWidth := tokenStatsTableFit(table, plan)
		width = max(width, tableWidth)
		if table.flexible() {
			fixedWidth = max(fixedWidth, tableFixedWidth)
		}
	}
	return width, fixedWidth
}

// flexible reports whether the table has the Model column the plan can
// shorten.
func (t tokenStatsTable) flexible() bool {
	for _, column := range t.columns {
		if column.flex {
			return true
		}
	}
	return false
}

// tokenStatsTableFit returns one table's printed width and the width it takes
// without its Model column's content.
func tokenStatsTableFit(table tokenStatsTable, plan tokenStatsPlan) (width, fixedWidth int) {
	kept := plan.keptColumns(table.columns)
	if len(kept) == 0 {
		return 0, 0
	}
	chrome := tokenStatsCellPadding*len(kept) + len(kept) - 1
	width, fixedWidth = chrome, chrome
	for _, j := range kept {
		column := table.columns[j]
		content := lipgloss.Width(column.header)
		for _, row := range table.rows {
			content = max(content, lipgloss.Width(row.cells[j]))
		}
		if plan.modelWidth > 0 && column.flex {
			content = min(content, plan.modelWidth)
		}
		width += content
		if !column.flex {
			fixedWidth += content
		}
	}
	return width, fixedWidth
}

// writeTokenStatsTable writes one markdown table with the plan's columns.
func writeTokenStatsTable(sb *strings.Builder, table tokenStatsTable, plan tokenStatsPlan) {
	kept := plan.keptColumns(table.columns)
	headers := make([]string, len(kept))
	alignments := make([]string, len(kept))
	for i, j := range kept {
		column := table.columns[j]
		headers[i] = plan.cell(column, column.header)
		alignments[i] = "---"
		if column.right {
			alignments[i] = "---:"
		}
	}
	writeTokenStatsCells(sb, headers, false)
	// The alignment line carries no cell padding, matching the compact
	// separator row glamour expects.
	sb.WriteString("|" + strings.Join(alignments, "|") + "|\n")
	for _, row := range table.rows {
		cells := make([]string, len(kept))
		for i, j := range kept {
			cells[i] = plan.cell(table.columns[j], row.cells[j])
		}
		writeTokenStatsCells(sb, cells, row.bold)
	}
}

// writeTokenStatsCells writes one markdown table line.
func writeTokenStatsCells(sb *strings.Builder, cells []string, boldFirst bool) {
	sb.WriteString("|")
	for i, cell := range cells {
		sb.WriteString(" ")
		if i == 0 && boldFirst {
			sb.WriteString("**")
			sb.WriteString(cell)
			sb.WriteString("**")
		} else {
			sb.WriteString(cell)
		}
		sb.WriteString(" |")
	}
	sb.WriteString("\n")
}

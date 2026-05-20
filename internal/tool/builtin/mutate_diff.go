package builtin

import (
	"fmt"
	"strings"
)

type diffOp int

const (
	diffEqual  diffOp = iota
	diffInsert        // line present in b, absent in a
	diffDelete        // line present in a, absent in b
)

type diffEdit struct {
	Op   diffOp
	Line string
}

// computeLineDiff returns the shortest edit script turning a into b using
// Myers' algorithm on line-level tokens.
func computeLineDiff(a, b []string) []diffEdit {
	n, m := len(a), len(b)
	if n == 0 && m == 0 {
		return nil
	}
	if n == 0 {
		return insertAll(b)
	}
	if m == 0 {
		return deleteAll(a)
	}
	trace, foundD := myersForward(a, b)
	steps := myersBacktrack(trace, foundD, n, m, n+m)
	return myersReplay(a, b, steps, n)
}

func insertAll(lines []string) []diffEdit {
	edits := make([]diffEdit, len(lines))
	for i, l := range lines {
		edits[i] = diffEdit{Op: diffInsert, Line: l}
	}
	return edits
}

func deleteAll(lines []string) []diffEdit {
	edits := make([]diffEdit, len(lines))
	for i, l := range lines {
		edits[i] = diffEdit{Op: diffDelete, Line: l}
	}
	return edits
}

// myersForward runs the Myers forward pass and returns per-step v snapshots.
func myersForward(a, b []string) (trace [][]int, foundD int) {
	n, m := len(a), len(b)
	maxEdit := n + m
	off := maxEdit
	sz := 2*maxEdit + 1
	v := make([]int, sz)
	foundD = -1

	for d := 0; d <= maxEdit; d++ {
		snap := make([]int, sz)
		copy(snap, v)
		trace = append(trace, snap)

		for k := -d; k <= d; k += 2 {
			ki := k + off
			x := myersSelectX(v, ki, k, d)
			y := x - k
			for x < n && y < m && a[x] == b[y] {
				x++
				y++
			}
			v[ki] = x
			if x == n && y == m {
				foundD = d
				break
			}
		}
		if foundD >= 0 {
			break
		}
	}
	return trace, foundD
}

func myersSelectX(v []int, ki, k, d int) int {
	if k == -d || (k != d && v[ki-1] < v[ki+1]) {
		return v[ki+1]
	}
	return v[ki-1] + 1
}

type editStep struct {
	op   diffOp
	x, y int // position in a, b at which this edit occurs
}

// myersBacktrack traces the Myers edit path backwards through the snapshots.
func myersBacktrack(trace [][]int, foundD, n, m, off int) []editStep {
	steps := make([]editStep, foundD)
	x, y := n, m
	for d := foundD; d > 0; d-- {
		prev := trace[d]
		k := x - y
		ki := k + off
		prevK := myersPrevDiagonal(prev, ki, k, d)
		snakeEndX := prev[prevK+off]
		snakeEndY := snakeEndX - prevK
		op := diffDelete
		if prevK != k-1 {
			op = diffInsert
		}
		steps[d-1] = editStep{op: op, x: snakeEndX, y: snakeEndY}
		x, y = snakeEndX, snakeEndY
	}
	return steps
}

func myersPrevDiagonal(prev []int, ki, k, d int) int {
	if k == -d || (k != d && prev[ki-1] < prev[ki+1]) {
		return k + 1
	}
	return k - 1
}

// myersReplay converts edit steps back into a linear diffEdit slice.
func myersReplay(a, b []string, steps []editStep, n int) []diffEdit {
	var edits []diffEdit
	cx, cy := 0, 0
	for _, s := range steps {
		for cx < s.x && cy < s.y {
			edits = append(edits, diffEdit{Op: diffEqual, Line: a[cx]})
			cx++
			cy++
		}
		if s.op == diffDelete {
			edits = append(edits, diffEdit{Op: diffDelete, Line: a[cx]})
			cx++
		} else {
			edits = append(edits, diffEdit{Op: diffInsert, Line: b[cy]})
			cy++
		}
	}
	for cx < n {
		edits = append(edits, diffEdit{Op: diffEqual, Line: a[cx]})
		cx++
		cy++
	}
	return edits
}

type hunk struct {
	oldStart, oldLen int
	newStart, newLen int
	lines            []string
}

// formatUnifiedHunks renders edits as a unified diff with contextLines of
// surrounding context. Hunks whose gaps are <= 2*contextLines are merged.
// Returns an empty string when there are no changes.
func formatUnifiedHunks(path string, a, b []string, contextLines int) string {
	edits := computeLineDiff(a, b)
	if !diffHasChanges(edits) {
		return ""
	}
	regions := diffChangeRegions(edits, contextLines)
	hunks := renderHunks(edits, regions)

	var sb strings.Builder
	fmt.Fprintf(&sb, "--- %s\n+++ %s\n", path, path)
	for _, h := range hunks {
		fmt.Fprintf(&sb, "@@ -%d,%d +%d,%d @@\n", h.oldStart, h.oldLen, h.newStart, h.newLen)
		for _, l := range h.lines {
			sb.WriteString(l)
			sb.WriteByte('\n')
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

func diffHasChanges(edits []diffEdit) bool {
	for _, e := range edits {
		if e.Op != diffEqual {
			return true
		}
	}
	return false
}

type editRegion struct{ lo, hi int }

func diffChangeRegions(edits []diffEdit, contextLines int) []editRegion {
	n := len(edits)
	var regions []editRegion
	for i := 0; i < n; {
		if edits[i].Op == diffEqual {
			i++
			continue
		}
		start := i
		for i < n && edits[i].Op != diffEqual {
			i++
		}
		lo := start - contextLines
		if lo < 0 {
			lo = 0
		}
		hi := i + contextLines
		if hi > n {
			hi = n
		}
		if len(regions) > 0 && lo <= regions[len(regions)-1].hi {
			regions[len(regions)-1].hi = hi
		} else {
			regions = append(regions, editRegion{lo, hi})
		}
	}
	return regions
}

func renderHunks(edits []diffEdit, regions []editRegion) []hunk {
	hunks := make([]hunk, 0, len(regions))
	for _, er := range regions {
		hunks = append(hunks, renderHunk(edits, er))
	}
	return hunks
}

func renderHunk(edits []diffEdit, er editRegion) hunk {
	oldLine, newLine := 1, 1
	for i := 0; i < er.lo; i++ {
		switch edits[i].Op {
		case diffEqual:
			oldLine++
			newLine++
		case diffDelete:
			oldLine++
		case diffInsert:
			newLine++
		}
	}
	var h hunk
	h.oldStart = oldLine
	h.newStart = newLine
	for i := er.lo; i < er.hi; i++ {
		e := edits[i]
		switch e.Op {
		case diffEqual:
			h.lines = append(h.lines, " "+e.Line)
			h.oldLen++
			h.newLen++
		case diffDelete:
			h.lines = append(h.lines, "-"+e.Line)
			h.oldLen++
		case diffInsert:
			h.lines = append(h.lines, "+"+e.Line)
			h.newLen++
		}
	}
	return h
}

// This file incorporates work from Dive (https://github.com/deepnoodle-ai/dive)
// which is licensed under Apache 2.0. The original work has been modified.
//
// Copyright 2024 DeepNoodle Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package builtin

import (
	"fmt"
	"sort"
	"strings"

	"github.com/luispabon/steiner/internal/tool"
)

// grepRenderedLineBudget caps the rendered lines of one grep page. File
// headers, context lines, separators and notices all count toward it.
const grepRenderedLineBudget = 200

func buildGrepResult(files []grepFileResult, mode string, showLines bool, beforeContext, afterContext, offset, headLimit int) GrepResult {
	// files_with_matches and count render one line per logical result.
	lineLimit := min(headLimit, grepRenderedLineBudget)
	switch mode {
	case "files_with_matches":
		rows := grepFilesWithMatches(files)
		page := paginateGrepRows(rows, offset, lineLimit)
		output := renderGrepFilesWithMatches(page)
		return buildGrepResultFromPage(len(rows), len(page), offset, output)
	case "count":
		rows := grepCountRows(files)
		page := paginateGrepRows(rows, offset, lineLimit)
		output := renderGrepCountRows(page)
		return buildGrepResultFromPage(len(rows), len(page), offset, output)
	case "content":
		fallthrough
	default:
		rows := grepContentSelections(files)
		candidates := paginateGrepRows(rows, offset, headLimit)
		page, output := fitGrepContentPage(files, candidates, showLines, beforeContext, afterContext)
		result := buildGrepResultFromPage(len(rows), len(page), offset, output)
		// Apply line bounding for content mode only; never truncate
		// file paths in files_with_matches or path:count rows.
		if result.Output != "" && len(page) > 0 {
			lines := strings.Split(result.Output, "\n")
			bounded := boundLines(lines, lineBoundingConfig{})
			result.Output = strings.Join(bounded, "\n")
		}
		return result
	}
}

func buildGrepResultFromPage(total, returned, offset int, output string) GrepResult {
	hasMore := offset+returned < total
	truncated := total > 0 && (offset > 0 || hasMore)
	nextOffset := 0
	if hasMore {
		nextOffset = offset + returned
	}
	if total == 0 {
		output = "No matches found"
	}

	result := GrepResult{
		Shown:      returned,
		NextOffset: nextOffset,
		Output:     output,
	}
	if truncated {
		result.Matches = total
	}
	return result
}

func grepFilesWithMatches(files []grepFileResult) []string {
	rows := make([]string, 0, len(files))
	for _, file := range files {
		rows = append(rows, file.file)
	}
	return rows
}

func grepCountRows(files []grepFileResult) []grepCountRow {
	rows := make([]grepCountRow, 0, len(files))
	for _, file := range files {
		rows = append(rows, grepCountRow{
			file:  file.file,
			count: len(file.matches),
		})
	}
	return rows
}

func grepContentSelections(files []grepFileResult) []grepContentSelection {
	rows := make([]grepContentSelection, 0)
	for _, file := range files {
		for _, match := range file.matches {
			rows = append(rows, grepContentSelection{
				file:       file.file,
				lineNumber: match.lineNumber,
			})
		}
	}
	return rows
}

func paginateGrepRows[T any](rows []T, offset, limit int) []T {
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 {
		return nil
	}
	if offset >= len(rows) {
		return nil
	}
	end := offset + limit
	if end > len(rows) {
		end = len(rows)
	}
	return rows[offset:end]
}

func renderGrepFilesWithMatches(files []string) string {
	if len(files) == 0 {
		return ""
	}
	return strings.TrimSpace(strings.Join(files, "\n"))
}

func renderGrepCountRows(rows []grepCountRow) string {
	if len(rows) == 0 {
		return ""
	}
	var b strings.Builder
	for _, row := range rows {
		_, _ = fmt.Fprintf(&b, "%s:%d\n", row.file, row.count)
	}
	return strings.TrimSpace(b.String())
}

func renderGrepContent(files []grepFileResult, selected []grepContentSelection, showLines bool, beforeContext, afterContext int) string {
	if len(selected) == 0 {
		return ""
	}

	filesByName := make(map[string]grepFileResult, len(files))
	for _, file := range files {
		filesByName[file.file] = file
	}

	fileOrder := make([]string, 0)
	selectedByFile := make(map[string][]int)
	for _, sel := range selected {
		if _, ok := selectedByFile[sel.file]; !ok {
			fileOrder = append(fileOrder, sel.file)
		}
		selectedByFile[sel.file] = append(selectedByFile[sel.file], sel.lineNumber)
	}

	var b strings.Builder
	for fileIndex, fileName := range fileOrder {
		file, ok := filesByName[fileName]
		if !ok {
			continue
		}

		windows := mergeGrepWindows(selectedByFile[fileName], len(file.lines), beforeContext, afterContext)
		if len(windows) == 0 {
			continue
		}

		_, _ = fmt.Fprintf(&b, "## %s\n", file.file)
		renderGrepFileWindows(&b, file, windows, selectedByFile[fileName], showLines)
		if fileIndex < len(fileOrder)-1 {
			b.WriteString("\n")
		}
	}

	return strings.TrimSpace(b.String())
}

// fitGrepContentPage returns the longest prefix of candidates whose rendered
// output fits grepRenderedLineBudget, with that output. Rendered line count is
// non-decreasing in prefix length, so a binary search finds the boundary. When
// the first candidate alone exceeds the budget, it is returned as a bounded
// excerpt so that pagination still advances past it.
func fitGrepContentPage(files []grepFileResult, candidates []grepContentSelection, showLines bool, beforeContext, afterContext int) ([]grepContentSelection, string) {
	if len(candidates) == 0 {
		return nil, ""
	}
	fit := 0
	output := ""
	lo, hi := 1, len(candidates)
	for lo <= hi {
		mid := lo + (hi-lo)/2
		rendered := renderGrepContent(files, candidates[:mid], showLines, beforeContext, afterContext)
		if grepLineCount(rendered) <= grepRenderedLineBudget {
			fit, output = mid, rendered
			lo = mid + 1
		} else {
			hi = mid - 1
		}
	}
	if fit == 0 {
		return candidates[:1], renderGrepExcerpt(files, candidates[0], showLines, beforeContext, afterContext)
	}
	return candidates[:fit], output
}

// renderGrepExcerpt renders one logical result whose window alone exceeds the
// rendered-line budget. The excerpt keeps the match line and ends with a
// truncation marker. Callers must only pass results that exceed the budget.
func renderGrepExcerpt(files []grepFileResult, sel grepContentSelection, showLines bool, beforeContext, afterContext int) string {
	var file grepFileResult
	for _, candidate := range files {
		if candidate.file == sel.file {
			file = candidate
			break
		}
	}
	windows := mergeGrepWindows([]int{sel.lineNumber}, len(file.lines), beforeContext, afterContext)
	if len(windows) == 0 {
		return ""
	}
	window := windows[0]
	total := window.end - window.start + 1
	// Reserve one line for the file header and one for the marker.
	maxLines := grepRenderedLineBudget - 2
	if total > maxLines {
		start := max(window.start, sel.lineNumber-maxLines/2)
		window = grepWindow{start: start, end: min(window.end, start+maxLines-1)}
	}

	var b strings.Builder
	_, _ = fmt.Fprintf(&b, "## %s\n", file.file)
	renderGrepFileWindows(&b, file, []grepWindow{window}, []int{sel.lineNumber}, showLines)
	b.WriteString(tool.TruncationMarker(window.end-window.start+1, total))
	return b.String()
}

// grepLineCount returns the number of lines in rendered grep output.
func grepLineCount(rendered string) int {
	if rendered == "" {
		return 0
	}
	return strings.Count(rendered, "\n") + 1
}

// renderGrepFileWindows renders the lines of each window. Only selected lines
// get match columns. Other matches inside a window render as context, so a
// result that belongs to a later page is not reported as emitted here.
func renderGrepFileWindows(b *strings.Builder, file grepFileResult, windows []grepWindow, selected []int, showLines bool) {
	lineColumns := grepLineColumns(file.matches)
	isSelected := make(map[int]bool, len(selected))
	for _, lineNumber := range selected {
		isSelected[lineNumber] = true
	}
	for windowIndex, window := range windows {
		for lineNumber := window.start; lineNumber <= window.end; lineNumber++ {
			line := ""
			if lineNumber-1 >= 0 && lineNumber-1 < len(file.lines) {
				line = strings.TrimRight(file.lines[lineNumber-1], "\r")
			}
			column := 0
			if isSelected[lineNumber] {
				column = lineColumns[lineNumber]
			}
			renderGrepLine(b, lineNumber, line, column, showLines)
		}
		if windowIndex < len(windows)-1 {
			b.WriteString("\n")
		}
	}
}

func grepLineColumns(matches []grepMatch) map[int]int {
	lineColumns := make(map[int]int, len(matches))
	for _, m := range matches {
		if _, exists := lineColumns[m.lineNumber]; !exists {
			lineColumns[m.lineNumber] = m.column
		}
	}
	return lineColumns
}

func renderGrepLine(b *strings.Builder, lineNumber int, line string, column int, showLines bool) {
	switch {
	case !showLines:
		_, _ = fmt.Fprintf(b, "%s\n", line)
	case column > 0:
		_, _ = fmt.Fprintf(b, "%d:%d: %s\n", lineNumber, column, line)
	default:
		_, _ = fmt.Fprintf(b, "%d: %s\n", lineNumber, line)
	}
}

func mergeGrepWindows(lineNumbers []int, totalLines, beforeContext, afterContext int) []grepWindow {
	if len(lineNumbers) == 0 || totalLines == 0 {
		return nil
	}

	sortedNumbers := append([]int(nil), lineNumbers...)
	sort.Ints(sortedNumbers)

	windows := make([]grepWindow, 0, len(sortedNumbers))
	for _, lineNumber := range sortedNumbers {
		start := lineNumber - beforeContext
		if start < 1 {
			start = 1
		}
		end := lineNumber + afterContext
		if end > totalLines {
			end = totalLines
		}

		if len(windows) == 0 {
			windows = append(windows, grepWindow{start: start, end: end})
			continue
		}

		last := &windows[len(windows)-1]
		if start <= last.end+1 {
			if end > last.end {
				last.end = end
			}
			continue
		}

		windows = append(windows, grepWindow{start: start, end: end})
	}

	return windows
}

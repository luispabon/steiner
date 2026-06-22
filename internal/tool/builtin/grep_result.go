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
)

func buildGrepResult(files []grepFileResult, mode string, showLines bool, beforeContext, afterContext, offset, headLimit int) GrepResult {
	switch mode {
	case "files_with_matches":
		rows := grepFilesWithMatches(files)
		page := paginateGrepRows(rows, offset, headLimit)
		output := renderGrepFilesWithMatches(page)
		return buildGrepResultFromPage(len(rows), len(page), len(page), offset, output)
	case "count":
		rows := grepCountRows(files)
		page := paginateGrepRows(rows, offset, headLimit)
		output := renderGrepCountRows(page)
		compat := 0
		for _, row := range page {
			compat += row.count
		}
		return buildGrepResultFromPage(len(rows), len(page), compat, offset, output)
	case "content":
		fallthrough
	default:
		rows := grepContentSelections(files)
		page := paginateGrepRows(rows, offset, headLimit)
		output := renderGrepContent(files, page, showLines, beforeContext, afterContext)
		result := buildGrepResultFromPage(len(rows), len(page), len(page), offset, output)
		// Apply line bounding for content mode only; never truncate
		// file paths in files_with_matches or path:count rows.
		if result.Output != "" && result.Matches > 0 {
			lines := strings.Split(result.Output, "\n")
			bounded, boundedReasons := boundLines(lines, lineBoundingConfig{})
			result.Output = strings.Join(bounded, "\n")
			result.TruncationReasons = append(boundedReasons, result.TruncationReasons...)
		}
		return result
	}
}

func buildGrepResultFromPage(total, returned, compatMatches, offset int, output string) GrepResult {
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
		Matches:    compatMatches,
		Returned:   returned,
		Truncated:  truncated,
		HasMore:    hasMore,
		NextOffset: nextOffset,
		Output:     output,
	}
	if truncated {
		result.TruncationReasons = []string{"paged"}
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
		for windowIndex, window := range windows {
			for lineNumber := window.start; lineNumber <= window.end; lineNumber++ {
				line := ""
				if lineNumber-1 >= 0 && lineNumber-1 < len(file.lines) {
					line = strings.TrimRight(file.lines[lineNumber-1], "\r")
				}
				if showLines {
					_, _ = fmt.Fprintf(&b, "%d: %s\n", lineNumber, line)
				} else {
					_, _ = fmt.Fprintf(&b, "%s\n", line)
				}
			}
			if windowIndex < len(windows)-1 {
				b.WriteString("\n")
			}
		}
		if fileIndex < len(fileOrder)-1 {
			b.WriteString("\n")
		}
	}

	return strings.TrimSpace(b.String())
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

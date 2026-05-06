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
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/gobwas/glob"
	"github.com/luispabon/steiner/internal/tool"
)

type grepMatch struct {
	file       string
	lineNumber int
	line       string
}

type grepFileResult struct {
	file    string
	lines   []string
	matches []grepMatch
}

type grepContentSelection struct {
	file       string
	lineNumber int
}

type grepCountRow struct {
	file  string
	count int
}

type grepWindow struct {
	start int
	end   int
}

var grepTypeToExt = map[string][]string{
	"go":     {".go"},
	"ts":     {".ts", ".tsx"},
	"js":     {".js", ".jsx"},
	"py":     {".py"},
	"rust":   {".rs"},
	"java":   {".java"},
	"c":      {".c", ".h"},
	"cpp":    {".cpp", ".cc", ".cxx", ".hpp", ".hh"},
	"rb":     {".rb"},
	"php":    {".php"},
	"swift":  {".swift"},
	"kotlin": {".kt", ".kts"},
	"scala":  {".scala"},
	"md":     {".md", ".markdown"},
	"json":   {".json"},
	"yaml":   {".yaml", ".yml"},
	"xml":    {".xml"},
	"html":   {".html", ".htm"},
	"css":    {".css", ".scss", ".sass", ".less"},
	"sql":    {".sql"},
	"sh":     {".sh", ".bash"},
}

func grepSearch(ctx context.Context, root, displayPath, pattern string, caseInsens, multiline bool, fileGlob, fileType string, excluder *tool.PathExcluder) ([]grepFileResult, error) {
	p := pattern
	if caseInsens {
		p = "(?i)" + p
	}
	if multiline {
		p = "(?s)" + p
	}
	re, err := regexp.Compile(p)
	if err != nil {
		return nil, fmt.Errorf("invalid regex: %w", err)
	}

	var filterGlob glob.Glob
	if fileGlob != "" {
		filterGlob, err = glob.Compile(fileGlob, '/')
		if err != nil {
			return nil, fmt.Errorf("invalid glob: %w", err)
		}
	}

	var exts []string
	if fileType != "" {
		exts = grepTypeToExt[fileType]
	}

	rootInfo, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("stat root: %w", err)
	}

	if !rootInfo.IsDir() {
		relPath := filepath.ToSlash(filepath.Clean(displayPath))
		if relPath == "." || relPath == "" {
			relPath = filepath.Base(root)
		}
		if excluder != nil && excluder.ShouldExclude(relPath) {
			return nil, nil
		}
		if filterGlob != nil && !filterGlob.Match(relPath) {
			return nil, nil
		}
		if exts != nil {
			ext := filepath.Ext(root)
			found := false
			for _, e := range exts {
				if strings.EqualFold(ext, e) {
					found = true
					break
				}
			}
			if !found {
				return nil, nil
			}
		}
		file, hasMatches, err := grepSearchFile(ctx, root, relPath, re, multiline)
		if err != nil {
			return nil, err
		}
		if !hasMatches {
			return nil, nil
		}
		return []grepFileResult{file}, nil
	}

	var results []grepFileResult

	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		select {
		case <-ctx.Done():
			return filepath.SkipAll
		default:
		}

		relPath, _ := filepath.Rel(root, path)
		relPath = filepath.ToSlash(relPath)

		if excluder != nil && excluder.ShouldExclude(relPath) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}

		if filterGlob != nil && !filterGlob.Match(relPath) {
			return nil
		}

		if exts != nil {
			ext := filepath.Ext(path)
			found := false
			for _, e := range exts {
				if strings.EqualFold(ext, e) {
					found = true
					break
				}
			}
			if !found {
				return nil
			}
		}

		file, hasMatches, err := grepSearchFile(ctx, path, relPath, re, multiline)
		if err != nil {
			return err
		}
		if hasMatches {
			results = append(results, file)
		}
		return nil
	})

	if walkErr != nil && walkErr != filepath.SkipAll {
		return nil, fmt.Errorf("walk: %w", walkErr)
	}

	return results, nil
}

func grepSearchFile(ctx context.Context, path, relPath string, re *regexp.Regexp, multiline bool) (grepFileResult, bool, error) {
	select {
	case <-ctx.Done():
		return grepFileResult{}, false, ctx.Err()
	default:
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return grepFileResult{}, false, fmt.Errorf("read %s: %w", relPath, err)
	}

	checkLen := 512
	if len(content) < checkLen {
		checkLen = len(content)
	}
	if bytes.Contains(content[:checkLen], []byte{0}) {
		return grepFileResult{}, false, nil
	}

	contentText := string(content)
	lines := strings.Split(contentText, "\n")
	matches := make([]grepMatch, 0)

	if multiline {
		lineStarts := make([]int, len(lines))
		offset := 0
		for i, line := range lines {
			lineStarts[i] = offset
			offset += len(line)
			if i < len(lines)-1 {
				offset++
			}
		}

		lineMatched := make([]bool, len(lines))
		matchIndexes := re.FindAllStringIndex(contentText, -1)
		for _, matchIndex := range matchIndexes {
			matchStart := matchIndex[0]
			matchEnd := matchIndex[1]
			if matchEnd == matchStart {
				matchEnd++
			}
			for i, line := range lines {
				lineStart := lineStarts[i]
				lineEnd := lineStart + len(line)
				if i < len(lines)-1 {
					lineEnd++
				}
				if lineStart < matchEnd && matchStart < lineEnd {
					lineMatched[i] = true
				}
			}
		}

		for i, matched := range lineMatched {
			if !matched {
				continue
			}
			matches = append(matches, grepMatch{
				file:       relPath,
				lineNumber: i + 1,
				line:       strings.TrimRight(lines[i], "\r"),
			})
		}
	} else {
		for i, line := range lines {
			if re.MatchString(line) {
				matches = append(matches, grepMatch{
					file:       relPath,
					lineNumber: i + 1,
					line:       strings.TrimRight(line, "\r"),
				})
			}
		}
	}

	if len(matches) == 0 {
		return grepFileResult{}, false, nil
	}

	return grepFileResult{
		file:    relPath,
		lines:   lines,
		matches: matches,
	}, true, nil
}

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
		return buildGrepResultFromPage(len(rows), len(page), len(page), offset, output)
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
	return GrepResult{
		Matches:    compatMatches,
		Returned:   returned,
		Truncated:  truncated,
		HasMore:    hasMore,
		NextOffset: nextOffset,
		Output:     output,
	}
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
		b.WriteString(fmt.Sprintf("%s:%d\n", row.file, row.count))
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

		b.WriteString(fmt.Sprintf("## %s\n", file.file))
		for windowIndex, window := range windows {
			for lineNumber := window.start; lineNumber <= window.end; lineNumber++ {
				line := ""
				if lineNumber-1 >= 0 && lineNumber-1 < len(file.lines) {
					line = strings.TrimRight(file.lines[lineNumber-1], "\r")
				}
				if showLines {
					b.WriteString(fmt.Sprintf("%d: %s\n", lineNumber, line))
				} else {
					b.WriteString(fmt.Sprintf("%s\n", line))
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

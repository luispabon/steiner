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
	"errors"
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
	re, err := compileGrepPattern(pattern, caseInsens, multiline)
	if err != nil {
		return nil, err
	}

	filterGlob, err := compileGrepGlob(fileGlob)
	if err != nil {
		return nil, err
	}
	exts := grepExtensions(fileType)

	rootInfo, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("stat root: %w", err)
	}

	if !rootInfo.IsDir() {
		return grepSearchPath(ctx, root, normalizedDisplayPath(root, displayPath), re, multiline, filterGlob, exts, excluder)
	}

	var results []grepFileResult

	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if err := ctx.Err(); err != nil {
			return err
		}

		relPath, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return fmt.Errorf("rel path: %w", relErr)
		}
		relPath = filepath.ToSlash(relPath)

		skipDir, skipFile := grepShouldSkipEntry(path, relPath, d, excluder, filterGlob, exts)
		if skipDir {
			return filepath.SkipDir
		}
		if skipFile {
			return nil
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

	if walkErr != nil {
		if errors.Is(walkErr, context.Canceled) || errors.Is(walkErr, context.DeadlineExceeded) {
			return nil, walkErr
		}
		return nil, fmt.Errorf("walk: %w", walkErr)
	}

	return results, nil
}

func compileGrepPattern(pattern string, caseInsens, multiline bool) (*regexp.Regexp, error) {
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
	return re, nil
}

func compileGrepGlob(fileGlob string) (glob.Glob, error) {
	if fileGlob == "" {
		return nil, nil
	}
	filterGlob, err := glob.Compile(fileGlob, '/')
	if err != nil {
		return nil, fmt.Errorf("invalid glob: %w", err)
	}
	return filterGlob, nil
}

func grepExtensions(fileType string) []string {
	if fileType == "" {
		return nil
	}
	return grepTypeToExt[fileType]
}

func normalizedDisplayPath(root, displayPath string) string {
	relPath := filepath.ToSlash(filepath.Clean(displayPath))
	if relPath == "." || relPath == "" {
		return filepath.Base(root)
	}
	return relPath
}

func grepSearchPath(ctx context.Context, path, relPath string, re *regexp.Regexp, multiline bool, filterGlob glob.Glob, exts []string, excluder *tool.PathExcluder) ([]grepFileResult, error) {
	if grepPathExcluded(path, relPath, filterGlob, exts, excluder) {
		return nil, nil
	}
	file, hasMatches, err := grepSearchFile(ctx, path, relPath, re, multiline)
	if err != nil {
		return nil, err
	}
	if !hasMatches {
		return nil, nil
	}
	return []grepFileResult{file}, nil
}

func grepShouldSkipEntry(path, relPath string, d fs.DirEntry, excluder *tool.PathExcluder, filterGlob glob.Glob, exts []string) (bool, bool) {
	if excluder != nil && excluder.ShouldExclude(relPath) {
		return d.IsDir(), true
	}
	if d.IsDir() {
		return false, true
	}
	if !d.Type().IsRegular() {
		return false, true
	}
	if grepPathExcluded(path, relPath, filterGlob, exts, excluder) {
		return false, true
	}
	return false, false
}

func grepPathExcluded(path, relPath string, filterGlob glob.Glob, exts []string, excluder *tool.PathExcluder) bool {
	if excluder != nil && excluder.ShouldExclude(relPath) {
		return true
	}
	if filterGlob != nil && !filterGlob.Match(relPath) {
		return true
	}
	return !grepMatchesExtension(path, exts)
}

func grepMatchesExtension(path string, exts []string) bool {
	if exts == nil {
		return true
	}
	ext := filepath.Ext(path)
	for _, candidate := range exts {
		if strings.EqualFold(ext, candidate) {
			return true
		}
	}
	return false
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
	matches := grepLineMatches(lines, contentText, relPath, re, multiline)
	if len(matches) == 0 {
		return grepFileResult{}, false, nil
	}

	return grepFileResult{
		file:    relPath,
		lines:   lines,
		matches: matches,
	}, true, nil
}

func grepLineMatches(lines []string, contentText, relPath string, re *regexp.Regexp, multiline bool) []grepMatch {
	if multiline {
		return grepMultilineMatches(lines, contentText, relPath, re)
	}
	return grepSingleLineMatches(lines, relPath, re)
}

func grepSingleLineMatches(lines []string, relPath string, re *regexp.Regexp) []grepMatch {
	matches := make([]grepMatch, 0)
	for i, line := range lines {
		if !re.MatchString(line) {
			continue
		}
		matches = append(matches, grepMatch{
			file:       relPath,
			lineNumber: i + 1,
			line:       strings.TrimRight(line, "\r"),
		})
	}
	return matches
}

func grepMultilineMatches(lines []string, contentText, relPath string, re *regexp.Regexp) []grepMatch {
	lineMatched := make([]bool, len(lines))
	lineStarts := grepLineStarts(lines)
	matchIndexes := re.FindAllStringIndex(contentText, -1)
	for _, matchIndex := range matchIndexes {
		grepMarkMatchedLines(lineMatched, lines, lineStarts, matchIndex[0], matchIndex[1])
	}

	matches := make([]grepMatch, 0)
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
	return matches
}

func grepLineStarts(lines []string) []int {
	lineStarts := make([]int, len(lines))
	offset := 0
	for i, line := range lines {
		lineStarts[i] = offset
		offset += len(line)
		if i < len(lines)-1 {
			offset++
		}
	}
	return lineStarts
}

func grepMarkMatchedLines(lineMatched []bool, lines []string, lineStarts []int, matchStart, matchEnd int) {
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

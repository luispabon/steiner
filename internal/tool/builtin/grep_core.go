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
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/gobwas/glob"

	"github.com/luispabon/steiner/internal/tool"
)

type grepMatch struct {
	file       string
	lineNumber int
	column     int
	line       string
}

// runeColumn converts a byte offset within line into a 1-based rune column.
func runeColumn(line string, byteOffset int) int {
	return utf8.RuneCountInString(line[:byteOffset]) + 1
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

// grepTypeAliases maps accepted type names to their canonical grepTypeToExt key.
var grepTypeAliases = map[string]string{
	"golang": "go",
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

// grepSearchParams holds the parameters for grepSearch.
type grepSearchParams struct {
	root        string
	displayPath string
	pattern     string
	caseInsens  bool
	multiline   bool
	fileGlob    string
	fileType    string
	excluder    *tool.WorkspaceExcluder
	policy      *tool.PathPolicy
}

func grepSearch(ctx context.Context, p grepSearchParams) ([]grepFileResult, error) {
	re, err := compileGrepPattern(p.pattern, p.caseInsens, p.multiline)
	if err != nil {
		return nil, err
	}

	filterGlob, err := compileGrepFileGlob(p.fileGlob)
	if err != nil {
		return nil, err
	}
	exts := grepExtensions(p.fileType)

	rootInfo, err := os.Stat(p.root)
	if err != nil {
		return nil, fmt.Errorf("stat root: %w", err)
	}

	if !rootInfo.IsDir() {
		if grepRootExcluded(p.root, p.root, false, p.excluder) {
			return nil, nil
		}
		dp := p.displayPath
		if p.policy != nil && p.policy.IsSandboxTmpPath(p.root) {
			dp = p.policy.DisplayPath(p.root)
		}
		return grepSearchPath(ctx, p.root, normalizedDisplayPath(p.root, dp), re, p.multiline, filterGlob, exts, p.excluder)
	}

	walkRoot, err := resolveSearchRoot(p.root, p.policy)
	if err != nil {
		return nil, err
	}

	// The root may be a symlink: validate exclusions against both the path the
	// caller named and the resolved walk root it points at.
	if grepRootExcluded(walkRoot, p.root, true, p.excluder) {
		return nil, nil
	}

	return grepWalkDir(ctx, walkRoot, p.root, re, p.multiline, filterGlob, exts, p.excluder, p.policy)
}

func grepWalkDir(ctx context.Context, root, lexicalRoot string, re *regexp.Regexp, multiline bool, filterGlob glob.Glob, exts []string, excluder *tool.WorkspaceExcluder, policy *tool.PathPolicy) ([]grepFileResult, error) {
	var results []grepFileResult

	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if handled, walkErr := walkDirEntryError(path, root, d, err); handled {
			return walkErr
		}

		if err := ctx.Err(); err != nil {
			return err
		}

		if searchPathDenied(policy, path) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		relPath, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return fmt.Errorf("rel path: %w", relErr)
		}
		relPath = filepath.ToSlash(relPath)

		skipDir, skipFile := grepShouldSkipEntry(root, lexicalRoot, path, relPath, d, excluder, filterGlob, exts)
		if skipDir {
			return filepath.SkipDir
		}
		if skipFile {
			return nil
		}

		file, hasMatches, err := grepSearchFile(ctx, path, relPath, re, multiline)
		if err != nil {
			if skippedEntryError(err) {
				return nil
			}
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

func walkDirEntryError(path, root string, d fs.DirEntry, err error) (bool, error) {
	if err == nil {
		return false, nil
	}
	if path == root {
		return true, err
	}
	switch {
	case walkEntryError(d, err):
		return true, filepath.SkipDir
	case skippedEntryError(err):
		return true, nil
	default:
		return true, err
	}
}

func walkEntryError(d fs.DirEntry, err error) bool {
	return d != nil && d.IsDir() && skippedEntryError(err)
}

func skippedEntryError(err error) bool {
	return errors.Is(err, fs.ErrPermission) || errors.Is(err, fs.ErrNotExist)
}

// grepRootExcluded reports whether an explicit search root is hidden by
// configured exclusions. The root is its own walk root, so built-in directory
// names never hide an explicitly targeted directory; only workspace-anchored
// configured paths and patterns apply, checked against both the lexical root
// and the resolved target a symlink root points at.
func grepRootExcluded(walkRoot, lexicalRoot string, isDir bool, excluder *tool.WorkspaceExcluder) bool {
	return excludeSearchEntry(excluder, walkRoot, lexicalRoot, walkRoot, isDir)
}

// resolveSearchRoot resolves a directory root that is itself a symlink to its
// target directory so walkers descend into it. Nested symlinks are not
// followed: filepath.WalkDir never descends into symlink entries. The resolved
// target is validated and normalized through policy, including the sandbox
// /tmp rewrite, so a symlink cannot redirect a search into a denied location
// or out of the sandbox. A root that cannot be inspected is returned unchanged
// so the calling tool reports its own missing-path error.
func resolveSearchRoot(root string, policy *tool.PathPolicy) (string, error) {
	info, err := os.Lstat(root)
	if err != nil {
		return root, nil
	}
	if info.Mode()&fs.ModeSymlink == 0 {
		return root, nil
	}
	target, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve symlink root: %w", err)
	}
	if policy == nil {
		return target, nil
	}
	resolved, err := policy.ResolveReadPath(target)
	if err != nil {
		return "", err
	}
	return resolved, nil
}

// searchPathDenied reports whether policy denies path. Resolution errors deny
// the path so a search fails closed rather than leaking a denied entry.
func searchPathDenied(policy *tool.PathPolicy, path string) bool {
	if policy == nil {
		return false
	}
	_, err := policy.ResolveReadPath(path)
	return err != nil
}

func compileGrepPattern(pattern string, caseInsens, multiline bool) (*regexp.Regexp, error) {
	p := pattern
	if caseInsens {
		p = "(?i)" + p
	}
	if multiline {
		p = "(?ms)" + p
	}
	re, err := regexp.Compile(p)
	if err != nil {
		return nil, fmt.Errorf("invalid regex: %w", err)
	}
	return re, nil
}

func grepExtensions(fileType string) []string {
	if fileType == "" {
		return nil
	}
	canonical, ok := canonicalGrepType(fileType)
	if !ok {
		return nil
	}
	return grepTypeToExt[canonical]
}

func canonicalGrepType(fileType string) (string, bool) {
	name := strings.ToLower(strings.TrimSpace(fileType))
	if name == "" {
		return "", true
	}
	if canonical, ok := grepTypeAliases[name]; ok {
		name = canonical
	}
	if _, ok := grepTypeToExt[name]; !ok {
		return "", false
	}
	return name, true
}

// grepTypeNames returns every accepted type name in deterministic order.
func grepTypeNames() []string {
	names := make([]string, 0, len(grepTypeToExt)+len(grepTypeAliases))
	for name := range grepTypeToExt {
		names = append(names, name)
	}
	for alias := range grepTypeAliases {
		names = append(names, alias)
	}
	sort.Strings(names)
	return names
}

func normalizedDisplayPath(root, displayPath string) string {
	relPath := filepath.ToSlash(filepath.Clean(displayPath))
	if relPath == "." || relPath == "" {
		return filepath.Base(root)
	}
	return relPath
}

func grepSearchPath(ctx context.Context, path, relPath string, re *regexp.Regexp, multiline bool, filterGlob glob.Glob, exts []string, excluder *tool.WorkspaceExcluder) ([]grepFileResult, error) {
	if grepPathExcluded(path, path, path, relPath, filterGlob, exts, excluder) {
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

func grepShouldSkipEntry(walkRoot, lexicalRoot, path, relPath string, d fs.DirEntry, excluder *tool.WorkspaceExcluder, filterGlob glob.Glob, exts []string) (bool, bool) {
	if excludeSearchEntry(excluder, walkRoot, lexicalRoot, path, d.IsDir()) {
		return d.IsDir(), true
	}
	if d.IsDir() {
		return false, true
	}
	if !d.Type().IsRegular() {
		return false, true
	}
	if grepPathExcluded(walkRoot, lexicalRoot, path, relPath, filterGlob, exts, excluder) {
		return false, true
	}
	return false, false
}

// grepPathExcluded reports whether an explicit file path is excluded by
// workspace exclusions or the grep file filters. walkRoot is the directory the
// search started from and lexicalRoot the path the caller named.
func grepPathExcluded(walkRoot, lexicalRoot, path, relPath string, filterGlob glob.Glob, exts []string, excluder *tool.WorkspaceExcluder) bool {
	if excludeSearchEntry(excluder, walkRoot, lexicalRoot, path, false) {
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

	if isBinary(content) {
		return grepFileResult{}, false, nil
	}

	contentText := normalizeGrepContent(content)
	lines := grepContentLines(contentText)
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

const grepUTF8BOM = "\xef\xbb\xbf"

// normalizeGrepContent strips a leading UTF-8 BOM and normalizes CRLF line
// endings before matching so anchors and displayed text agree.
func normalizeGrepContent(content []byte) string {
	text := strings.TrimPrefix(string(content), grepUTF8BOM)
	return strings.ReplaceAll(text, "\r\n", "\n")
}

// grepContentLines splits normalized content into real lines. A trailing
// newline terminates the last line but does not create an extra empty line.
func grepContentLines(contentText string) []string {
	if contentText == "" {
		return nil
	}
	lines := strings.Split(contentText, "\n")
	if strings.HasSuffix(contentText, "\n") {
		return lines[:len(lines)-1]
	}
	return lines
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
		loc := re.FindStringIndex(line)
		if loc == nil {
			continue
		}
		matches = append(matches, grepMatch{
			file:       relPath,
			lineNumber: i + 1,
			column:     runeColumn(line, loc[0]),
			line:       strings.TrimRight(line, "\r"),
		})
	}
	return matches
}

func grepMultilineMatches(lines []string, contentText, relPath string, re *regexp.Regexp) []grepMatch {
	lineMatched := make([]bool, len(lines))
	lineColumn := make([]int, len(lines))
	lineStarts := grepLineStarts(lines)
	matchIndexes := re.FindAllStringIndex(contentText, -1)
	for _, matchIndex := range matchIndexes {
		if !grepMarkMatchedLines(lineMatched, lines, lineStarts, matchIndex[0], matchIndex[1]) {
			continue
		}
		startLine := grepLineForOffset(lineStarts, matchIndex[0])
		if lineColumn[startLine] == 0 {
			lineColumn[startLine] = runeColumn(lines[startLine], matchIndex[0]-lineStarts[startLine])
		}
	}

	matches := make([]grepMatch, 0)
	for i, matched := range lineMatched {
		if !matched {
			continue
		}
		matches = append(matches, grepMatch{
			file:       relPath,
			lineNumber: i + 1,
			column:     lineColumn[i],
			line:       strings.TrimRight(lines[i], "\r"),
		})
	}
	return matches
}

// grepLineForOffset returns the index of the line containing the given byte
// offset into the joined content text, given each line's starting offset.
func grepLineForOffset(lineStarts []int, offset int) int {
	if len(lineStarts) == 0 {
		return 0
	}
	line := sort.Search(len(lineStarts), func(i int) bool {
		return lineStarts[i] > offset
	}) - 1
	if line < 0 {
		return 0
	}
	return line
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

func grepMarkMatchedLines(lineMatched []bool, lines []string, lineStarts []int, matchStart, matchEnd int) bool {
	if len(lines) == 0 || len(lineStarts) == 0 {
		return false
	}
	finalLine := len(lines) - 1
	contentEnd := lineStarts[finalLine] + len(lines[finalLine])
	if matchStart > contentEnd {
		return false
	}
	if matchStart == contentEnd {
		lineMatched[finalLine] = true
		return true
	}
	if matchEnd == matchStart {
		matchEnd++
	}

	firstLine := grepLineForOffset(lineStarts, matchStart)
	lastOffset := matchEnd - 1
	if lastOffset >= contentEnd {
		lastOffset = contentEnd - 1
	}
	lastLine := grepLineForOffset(lineStarts, lastOffset)
	for i := firstLine; i <= lastLine; i++ {
		lineMatched[i] = true
	}
	return true
}

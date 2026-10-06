/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package docs keeps the Markdown documentation honest: the Kubernetes
// objects in its YAML blocks must be accepted by the CRDs, and its relative
// links must resolve. See docs/README.md, "The docs are tested".
package docs

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// repositoryRoot is the checkout's root, seen from this package's directory.
var repositoryRoot = filepath.Join("..", "..")

// skipMarker, on the line right before a fence, excludes that block from the
// object check.
const skipMarker = "<!-- docs-test: skip"

// codeBlock is one fenced code block of a Markdown file.
type codeBlock struct {
	language       string
	content        string
	startLine      int  // line number of the opening fence, 1-based
	markedToSkip   bool // the line before the fence is a skip marker
	languageNumber int  // 1-based position among the file's blocks of the same language
}

// markdownFile is a Markdown file split into its code blocks and its prose
// (every line outside a code block, with code blocks blanked out).
type markdownFile struct {
	path   string
	blocks []codeBlock
	prose  []string
}

var fencePattern = regexp.MustCompile("^(\\s*)(```+|~~~+)\\s*([^\\s`]*)")

func readMarkdownFile(t *testing.T, path string) markdownFile {
	t.Helper()
	file, err := parseMarkdownFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return file
}

func parseMarkdownFile(path string) (markdownFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return markdownFile{}, err
	}
	lines := strings.Split(string(data), "\n")
	file := markdownFile{path: path}
	countByLanguage := map[string]int{}
	var open *codeBlock
	var fence, indentation string
	var body []string
	for index, line := range lines {
		if open == nil {
			match := fencePattern.FindStringSubmatch(line)
			if match == nil {
				file.prose = append(file.prose, line)
				continue
			}
			indentation, fence = match[1], match[2]
			language := strings.ToLower(match[3])
			countByLanguage[language]++
			open = &codeBlock{
				language:       language,
				startLine:      index + 1,
				languageNumber: countByLanguage[language],
				markedToSkip: index > 0 &&
					strings.HasPrefix(strings.TrimSpace(lines[index-1]), skipMarker),
			}
			body = nil
			file.prose = append(file.prose, "")
			continue
		}
		file.prose = append(file.prose, "")
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, fence[:1]) && strings.Trim(trimmed, fence[:1]) == "" &&
			len(trimmed) >= len(fence) {
			open.content = strings.Join(body, "\n")
			file.blocks = append(file.blocks, *open)
			open = nil
			continue
		}
		body = append(body, strings.TrimPrefix(line, indentation))
	}
	if open != nil {
		return markdownFile{}, fmt.Errorf("%s:%d: code block is never closed", relativeName(path), open.startLine)
	}
	return file, nil
}

// documentationFiles returns every Markdown file under docs/, sorted.
func documentationFiles(t *testing.T) []string {
	t.Helper()
	var paths []string
	err := filepath.WalkDir(filepath.Join(repositoryRoot, "docs"),
		func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !entry.IsDir() && strings.HasSuffix(path, ".md") {
				paths = append(paths, path)
			}
			return nil
		})
	if err != nil {
		t.Fatalf("listing docs: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("no Markdown files under docs/")
	}
	return paths
}

// relativeName is a path as the repository names it, for messages.
func relativeName(path string) string {
	name, err := filepath.Rel(repositoryRoot, path)
	if err != nil {
		return path
	}
	return filepath.ToSlash(name)
}

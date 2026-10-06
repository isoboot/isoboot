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

package docs

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var (
	// [text](target) and [text](target "title"); images too.
	markdownLink = regexp.MustCompile(`\]\(([^)\s]+)(?:\s+"[^"]*")?\)`)
	// href="target" and src="target" in inline HTML.
	htmlLink   = regexp.MustCompile(`(?:href|src)="([^"]+)"`)
	inlineCode = regexp.MustCompile("`+[^`]*`+")
	heading    = regexp.MustCompile(`^#{1,6}\s+(.*?)\s*#*\s*$`)
	// GitHub keeps letters, marks, digits, connector punctuation (such as
	// _), spaces and hyphens in a heading's anchor.
	notInAnchor        = regexp.MustCompile(`[^\p{L}\p{M}\p{N}\p{Pc} -]`)
	linkInHeading      = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	externalLinkPrefix = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*:`)
)

// TestDocumentationLinksResolve checks every relative link in docs/ and the
// project README: the file must exist and an #anchor must name a heading.
func TestDocumentationLinksResolve(t *testing.T) {
	paths := append(documentationFiles(t), filepath.Join(repositoryRoot, "README.md"))
	anchorsByFile := map[string]map[string]bool{}
	checked := 0
	for _, path := range paths {
		file := readMarkdownFile(t, path)
		for lineIndex, line := range file.prose {
			for _, target := range linkTargets(line) {
				checked++
				if err := checkLink(path, target, anchorsByFile); err != nil {
					t.Errorf("%s:%d: link %q: %v", relativeName(path), lineIndex+1, target, err)
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("found no links to check")
	}
	t.Logf("checked %d links in %d files", checked, len(paths))
}

func linkTargets(line string) []string {
	line = inlineCode.ReplaceAllString(line, "")
	var targets []string
	for _, pattern := range []*regexp.Regexp{markdownLink, htmlLink} {
		for _, match := range pattern.FindAllStringSubmatch(line, -1) {
			if !externalLinkPrefix.MatchString(match[1]) {
				targets = append(targets, match[1])
			}
		}
	}
	return targets
}

func checkLink(fromPath, target string, anchorsByFile map[string]map[string]bool) error {
	targetPath, anchor, _ := strings.Cut(target, "#")
	unescaped, err := url.PathUnescape(targetPath)
	if err != nil {
		return err
	}
	resolved := fromPath
	if unescaped != "" {
		resolved = filepath.Join(filepath.Dir(fromPath), filepath.FromSlash(unescaped))
		if _, err := os.Stat(resolved); err != nil {
			return fmt.Errorf("no such file %s", relativeName(resolved))
		}
	}
	if anchor == "" {
		return nil
	}
	if !strings.HasSuffix(resolved, ".md") {
		return fmt.Errorf("anchor on a file that is not Markdown")
	}
	anchors, ok := anchorsByFile[resolved]
	if !ok {
		anchors, err = headingAnchors(resolved)
		if err != nil {
			return err
		}
		anchorsByFile[resolved] = anchors
	}
	if !anchors[anchor] {
		return fmt.Errorf("no heading with anchor #%s in %s", anchor, relativeName(resolved))
	}
	return nil
}

// headingAnchors returns the anchors GitHub gives the headings of a
// Markdown file: lower case, punctuation dropped, spaces to hyphens, and
// -1, -2, ... appended to repeats.
func headingAnchors(path string) (map[string]bool, error) {
	file, err := parseMarkdownFile(path)
	if err != nil {
		return nil, err
	}
	anchors := map[string]bool{}
	seen := map[string]int{}
	for _, line := range file.prose {
		match := heading.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		text := linkInHeading.ReplaceAllString(match[1], "$1")
		text = strings.ReplaceAll(text, "`", "")
		anchor := strings.ReplaceAll(notInAnchor.ReplaceAllString(strings.ToLower(text), ""), " ", "-")
		if count := seen[anchor]; count > 0 {
			anchors[fmt.Sprintf("%s-%d", anchor, count)] = true
		} else {
			anchors[anchor] = true
		}
		seen[anchor]++
	}
	return anchors, nil
}

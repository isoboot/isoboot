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

package controller

import (
	"fmt"
	"net/url"
	"path"
	"path/filepath"
	"strings"
)

// ResourceType identifies the kind of boot resource being downloaded.
type ResourceType string

const (
	ResourceKernel   ResourceType = "kernel"
	ResourceInitrd   ResourceType = "initrd"
	ResourceFirmware ResourceType = "firmware"
	ResourceISO      ResourceType = "iso"
)

// DownloadPath computes the host-local file path where a downloaded resource
// should be stored. The layout is:
//
//	<baseDir>/<namespace>/<name>/<resourceType>/<filename>
//
// The filename is extracted from the URL path component. Query parameters and
// fragments are ignored when determining the filename.
func DownloadPath(baseDir, namespace, name string, rt ResourceType, rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("invalid URL %q: %w", rawURL, err)
	}

	filename := path.Base(u.Path)
	if filename == "" || filename == "." || filename == "/" {
		return "", fmt.Errorf("URL %q has no filename", rawURL)
	}

	result := filepath.Join(baseDir, namespace, name, string(rt), filename)

	// Guard against path traversal via crafted namespace, name, or filename.
	if !strings.HasPrefix(result, filepath.Clean(baseDir)+string(filepath.Separator)) {
		return "", fmt.Errorf("resolved path %q escapes base directory %q", result, baseDir)
	}

	return result, nil
}

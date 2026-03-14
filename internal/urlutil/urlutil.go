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

package urlutil

import (
	"net/url"
	"path"
)

// FilenameFromURL extracts the filename from a URL path.
// It returns "artifact" if the URL cannot be parsed or contains no filename.
func FilenameFromURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "artifact"
	}
	name := path.Base(u.Path)
	if name == "" || name == "." || name == "/" || name == ".." {
		return "artifact"
	}
	return name
}

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

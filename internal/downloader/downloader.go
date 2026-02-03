package downloader

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

// Download fetches the URL content and writes it atomically to destPath.
// It first writes to a temporary file in the same directory, then renames
// it to the final path, ensuring no partial files are left on failure.
//
// Security: destPath is used as provided and is not validated or sanitized.
// Callers must ensure that destPath is a trusted, safe path and does not
// allow path traversal (e.g., "../../../...") or otherwise escape any
// intended directory boundaries.
func Download(ctx context.Context, url, destPath string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("downloading %s: %w", url, err)
	}
	defer resp.Body.Close() //nolint:errcheck // best-effort close on HTTP response

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("downloading %s: HTTP %d", url, resp.StatusCode)
	}

	dir := filepath.Dir(destPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating destination directory: %w", err)
	}

	tmp, err := os.CreateTemp(dir, ".download-*")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpPath := tmp.Name()
	closed := false

	// Clean up the temp file on any error.
	defer func() {
		if !closed {
			tmp.Close() //nolint:errcheck // best-effort close on cleanup
		}
		if tmpPath != "" {
			os.Remove(tmpPath) //nolint:errcheck // best-effort cleanup
		}
	}()

	if _, err := io.Copy(tmp, resp.Body); err != nil {
		return fmt.Errorf("writing to temp file: %w", err)
	}

	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing temp file: %w", err)
	}
	closed = true

	if err := os.Rename(tmpPath, destPath); err != nil {
		return fmt.Errorf("renaming temp file: %w", err)
	}

	// Clear tmpPath so the deferred cleanup doesn't remove the final file.
	tmpPath = ""
	return nil
}

// FetchContent fetches a URL and returns its body as bytes.
// Intended for small files like shasum files.
func FetchContent(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching %s: %w", url, err)
	}
	defer resp.Body.Close() //nolint:errcheck // best-effort close on HTTP response

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetching %s: HTTP %d", url, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}

	return body, nil
}

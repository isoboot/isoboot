package downloader

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// NewDefaultClient returns an *http.Client with timeouts suitable for
// downloading boot media over the network. It sets a 30-second dial
// timeout, a 10-second TLS handshake timeout, and a 30-second response
// header timeout. No overall client timeout is set so that large file
// downloads are not interrupted; callers should use context deadlines for
// per-request time limits.
func NewDefaultClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			DialContext:           (&net.Dialer{Timeout: 30 * time.Second}).DialContext,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
		},
	}
}

// Download fetches the URL content and writes it atomically to destPath.
// It first writes to a temporary file in the same directory, then renames
// it to the final path, ensuring no partial files are left on failure.
//
// If client is nil, a default client with sensible timeouts is used
// (see NewDefaultClient).
//
// Security: destPath is used as provided and is not validated or sanitized.
// Callers must ensure that destPath is a trusted, safe path and does not
// allow path traversal (e.g., "../../../...") or otherwise escape any
// intended directory boundaries.
func Download(ctx context.Context, client *http.Client, url, destPath string) error {
	if client == nil {
		client = NewDefaultClient()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}

	resp, err := client.Do(req)
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

// maxFetchSize is the maximum response size FetchContent will read (10 MB).
// This prevents memory exhaustion from unexpectedly large responses.
const maxFetchSize = 10 << 20

// FetchContent fetches a URL and returns its body as bytes.
// Intended for small files like shasum files. Responses larger than 10 MB
// are rejected.
//
// If client is nil, a default client with sensible timeouts is used
// (see NewDefaultClient).
func FetchContent(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	if client == nil {
		client = NewDefaultClient()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching %s: %w", url, err)
	}
	defer resp.Body.Close() //nolint:errcheck // best-effort close on HTTP response

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetching %s: HTTP %d", url, resp.StatusCode)
	}

	limited := io.LimitReader(resp.Body, maxFetchSize+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}
	if len(body) > maxFetchSize {
		return nil, fmt.Errorf("fetching %s: response exceeds %d byte limit", url, maxFetchSize)
	}

	return body, nil
}

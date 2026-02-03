package downloader

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDownload_HappyPath(t *testing.T) {
	expected := "file content for download test"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if _, err := w.Write([]byte(expected)); err != nil {
			t.Errorf("writing response: %v", err)
		}
	}))
	defer ts.Close()

	dir := t.TempDir()
	destPath := filepath.Join(dir, "downloaded-file")

	err := Download(context.Background(), ts.URL, destPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("reading downloaded file: %v", err)
	}
	if string(data) != expected {
		t.Errorf("expected %q, got %q", expected, string(data))
	}
}

func TestDownload_CreatesDirectory(t *testing.T) {
	expected := "data"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if _, err := w.Write([]byte(expected)); err != nil {
			t.Errorf("writing response: %v", err)
		}
	}))
	defer ts.Close()

	dir := t.TempDir()
	destPath := filepath.Join(dir, "sub", "dir", "file")

	err := Download(context.Background(), ts.URL, destPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("reading downloaded file: %v", err)
	}
	if string(data) != expected {
		t.Errorf("expected %q, got %q", expected, string(data))
	}
}

func TestDownload_404(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	dir := t.TempDir()
	destPath := filepath.Join(dir, "should-not-exist")

	err := Download(context.Background(), ts.URL, destPath)
	if err == nil {
		t.Fatal("expected error for 404 response")
	}

	if _, statErr := os.Stat(destPath); !os.IsNotExist(statErr) {
		t.Error("destPath should not exist after failed download")
	}
}

func TestDownload_AtomicNoPartialFiles(t *testing.T) {
	// Use Hijacker to forcibly close the TCP connection mid-response,
	// guaranteeing the HTTP client sees an error.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("server does not support hijacking")
		}
		w.Header().Set("Content-Length", "1000000")
		w.WriteHeader(http.StatusOK)
		// Flush the headers so the client starts reading.
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		// Hijack and close the raw connection immediately.
		conn, _, err := hj.Hijack()
		if err != nil {
			t.Fatalf("hijack failed: %v", err)
		}
		conn.Close() //nolint:errcheck // intentional abrupt close for test
	}))
	defer ts.Close()

	dir := t.TempDir()
	destPath := filepath.Join(dir, "should-not-exist")

	err := Download(context.Background(), ts.URL, destPath)
	if err == nil {
		t.Fatal("expected error for truncated download")
	}

	if _, statErr := os.Stat(destPath); !os.IsNotExist(statErr) {
		t.Error("destPath should not exist after failed download")
	}
}

func TestFetchContent_HappyPath(t *testing.T) {
	expected := "shasum file content"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if _, err := w.Write([]byte(expected)); err != nil {
			t.Errorf("writing response: %v", err)
		}
	}))
	defer ts.Close()

	data, err := FetchContent(context.Background(), ts.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(data) != expected {
		t.Errorf("expected %q, got %q", expected, string(data))
	}
}

func TestFetchContent_404(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	_, err := FetchContent(context.Background(), ts.URL)
	if err == nil {
		t.Fatal("expected error for 404 response")
	}
}

func TestDownload_ContextCanceled(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if _, err := w.Write([]byte("data")); err != nil {
			t.Errorf("writing response: %v", err)
		}
	}))
	defer ts.Close()

	dir := t.TempDir()
	destPath := filepath.Join(dir, "should-not-exist")

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	err := Download(ctx, ts.URL, destPath)
	if err == nil {
		t.Fatal("expected error for canceled context")
	}

	if _, statErr := os.Stat(destPath); !os.IsNotExist(statErr) {
		t.Error("destPath should not exist after canceled download")
	}
}

func TestFetchContent_ExceedsSizeLimit(t *testing.T) {
	// Respond with more than maxFetchSize bytes.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if _, err := w.Write([]byte(strings.Repeat("x", maxFetchSize+1))); err != nil {
			// Write may fail if client disconnects early; that's fine.
			if !isConnectionReset(err) {
				t.Errorf("writing response: %v", err)
			}
		}
	}))
	defer ts.Close()

	_, err := FetchContent(context.Background(), ts.URL)
	if err == nil {
		t.Fatal("expected error for oversized response")
	}
}

func TestFetchContent_ContextCanceled(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if _, err := w.Write([]byte("data")); err != nil {
			t.Errorf("writing response: %v", err)
		}
	}))
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := FetchContent(ctx, ts.URL)
	if err == nil {
		t.Fatal("expected error for canceled context")
	}
}

func isConnectionReset(err error) bool {
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return opErr.Err.Error() == "write: broken pipe" ||
			strings.Contains(opErr.Err.Error(), "connection reset")
	}
	return false
}

package downloader

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	// Server sends some data then closes the connection abruptly.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Set content-length higher than what we actually send to simulate truncation.
		w.Header().Set("Content-Length", "1000000")
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte("partial data")); err != nil {
			t.Errorf("writing response: %v", err)
		}
		// The handler returns, closing the connection before content-length is satisfied.
	}))
	defer ts.Close()

	dir := t.TempDir()
	destPath := filepath.Join(dir, "should-not-exist")

	err := Download(context.Background(), ts.URL, destPath)
	// The error may or may not happen depending on how the HTTP client handles
	// the premature close. But if there IS an error, destPath must not exist.
	if err != nil {
		if _, statErr := os.Stat(destPath); !os.IsNotExist(statErr) {
			t.Error("destPath should not exist after failed download")
		}
	}
	// If no error (e.g., the HTTP client didn't detect the truncation), the file
	// should still be atomic (fully written via rename).
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

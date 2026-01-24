package downloader

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestEnsureFile_AlreadyExists(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "downloader-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create existing file
	existingFile := filepath.Join(tmpDir, "existing.iso")
	if err := os.WriteFile(existingFile, []byte("existing content"), 0644); err != nil {
		t.Fatalf("Failed to create existing file: %v", err)
	}

	d := New()

	// Should return immediately without downloading
	err = d.EnsureFile(existingFile, "http://should-not-be-called.invalid")
	if err != nil {
		t.Errorf("Expected no error for existing file, got: %v", err)
	}
}

func TestEnsureFile_Download(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "downloader-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create test server
	testContent := []byte("test iso content 1234567890")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(testContent)
	}))
	defer server.Close()

	d := New()
	destPath := filepath.Join(tmpDir, "downloaded.iso")

	err = d.EnsureFile(destPath, server.URL)
	if err != nil {
		t.Fatalf("Failed to download: %v", err)
	}

	// Verify file exists
	content, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("Failed to read downloaded file: %v", err)
	}

	if string(content) != string(testContent) {
		t.Errorf("Content mismatch: expected %q, got %q", testContent, content)
	}

	// Verify no .tmp or .downloading files left
	if _, err := os.Stat(destPath + ".tmp"); !os.IsNotExist(err) {
		t.Error("Expected .tmp file to be removed")
	}
	if _, err := os.Stat(destPath + ".downloading"); !os.IsNotExist(err) {
		t.Error("Expected .downloading file to be removed")
	}
}

func TestEnsureFile_ConcurrentDownload(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "downloader-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Track how many times server was called
	var downloadCount int
	var mu sync.Mutex

	testContent := []byte("test content for concurrent download")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		downloadCount++
		mu.Unlock()
		w.Write(testContent)
	}))
	defer server.Close()

	d := New()
	destPath := filepath.Join(tmpDir, "concurrent.iso")

	// Start 5 concurrent downloads
	var wg sync.WaitGroup
	errors := make(chan error, 5)

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := d.EnsureFile(destPath, server.URL); err != nil {
				errors <- err
			}
		}()
	}

	wg.Wait()
	close(errors)

	// Check for errors
	for err := range errors {
		t.Errorf("Concurrent download error: %v", err)
	}

	// Should only have downloaded once
	mu.Lock()
	count := downloadCount
	mu.Unlock()

	if count != 1 {
		t.Errorf("Expected 1 download, got %d", count)
	}

	// Verify file content
	content, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("Failed to read file: %v", err)
	}
	if string(content) != string(testContent) {
		t.Errorf("Content mismatch")
	}
}

func TestEnsureFile_DownloadError(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "downloader-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Server returns 404
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	d := New()
	destPath := filepath.Join(tmpDir, "notfound.iso")

	err = d.EnsureFile(destPath, server.URL)
	if err == nil {
		t.Error("Expected error for 404 response")
	}

	// File should not exist
	if _, err := os.Stat(destPath); !os.IsNotExist(err) {
		t.Error("File should not exist after failed download")
	}

	// No leftover files
	if _, err := os.Stat(destPath + ".tmp"); !os.IsNotExist(err) {
		t.Error("Expected .tmp file to be cleaned up")
	}
	if _, err := os.Stat(destPath + ".downloading"); !os.IsNotExist(err) {
		t.Error("Expected .downloading file to be cleaned up")
	}
}

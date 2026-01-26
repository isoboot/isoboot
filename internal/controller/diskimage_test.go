package controller

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseChecksumFile(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected map[string]string
	}{
		{
			name: "standard format with two spaces",
			input: `abc123  file1.iso
def456  file2.iso`,
			expected: map[string]string{
				"file1.iso": "abc123",
				"file2.iso": "def456",
			},
		},
		{
			name: "binary mode with asterisk",
			input: `abc123 *file1.iso
def456 *file2.iso`,
			expected: map[string]string{
				"file1.iso": "abc123",
				"file2.iso": "def456",
			},
		},
		{
			name: "with path prefix",
			input: `abc123  ./subdir/file1.iso
def456  subdir/file2.iso`,
			expected: map[string]string{
				"subdir/file1.iso": "abc123",
				"file1.iso":        "abc123",
				"subdir/file2.iso": "def456",
				"file2.iso":        "def456",
			},
		},
		{
			name: "with comments and empty lines",
			input: `# This is a comment
abc123  file1.iso

def456  file2.iso
# Another comment`,
			expected: map[string]string{
				"file1.iso": "abc123",
				"file2.iso": "def456",
			},
		},
		{
			name:     "empty file",
			input:    "",
			expected: map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseChecksumFile(strings.NewReader(tt.input))

			for key, expectedValue := range tt.expected {
				if got, ok := result[key]; !ok {
					t.Errorf("missing key %q", key)
				} else if got != expectedValue {
					t.Errorf("key %q: expected %q, got %q", key, expectedValue, got)
				}
			}
		})
	}
}

func TestVerifyChecksum(t *testing.T) {
	// Create a test hash
	testData := []byte("test content")
	hash := sha256.New()
	hash.Write(testData)
	actualHash := fmt.Sprintf("%x", hash.Sum(nil))

	tests := []struct {
		name      string
		checksums map[string]map[string]string
		hashType  string
		filename  string
		expected  string
	}{
		{
			name: "matching checksum",
			checksums: map[string]map[string]string{
				"sha256": {"test.iso": actualHash},
			},
			hashType: "sha256",
			filename: "test.iso",
			expected: "verified",
		},
		{
			name: "matching checksum case insensitive",
			checksums: map[string]map[string]string{
				"sha256": {"test.iso": strings.ToUpper(actualHash)},
			},
			hashType: "sha256",
			filename: "test.iso",
			expected: "verified",
		},
		{
			name: "mismatched checksum",
			checksums: map[string]map[string]string{
				"sha256": {"test.iso": "wronghash"},
			},
			hashType: "sha256",
			filename: "test.iso",
			expected: "failed",
		},
		{
			name: "hash type not found",
			checksums: map[string]map[string]string{
			},
			hashType: "sha256",
			filename: "test.iso",
			expected: "not_found",
		},
		{
			name: "filename not found",
			checksums: map[string]map[string]string{
				"sha256": {"other.iso": actualHash},
			},
			hashType: "sha256",
			filename: "test.iso",
			expected: "not_found",
		},
		{
			name:      "empty checksums",
			checksums: map[string]map[string]string{},
			hashType:  "sha256",
			filename:  "test.iso",
			expected:  "not_found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hash := sha256.New()
			hash.Write(testData)

			result := verifyChecksum(tt.checksums, tt.hashType, hash, tt.filename)
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestVerifyExistingFile(t *testing.T) {
	// Create a temp directory
	tmpDir, err := os.MkdirTemp("", "diskimage-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create a test file with known content
	testContent := []byte("test file content for verification")
	testFile := filepath.Join(tmpDir, "test.iso")
	if err := os.WriteFile(testFile, testContent, 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	// Calculate expected checksums
	sha256Hash := sha256.New()
	sha256Hash.Write(testContent)
	expectedSha256 := fmt.Sprintf("%x", sha256Hash.Sum(nil))


	ctrl := &Controller{}

	t.Run("valid file with matching checksums", func(t *testing.T) {
		checksums := map[string]map[string]string{
			"sha256": {"test.iso": expectedSha256},
		}

		result := ctrl.verifyExistingFile(testFile, int64(len(testContent)), checksums, "test.iso")
		if result == nil {
			t.Fatal("expected verification result, got nil")
		}
		if result.FileSizeMatch != "verified" {
			t.Errorf("expected FileSizeMatch=verified, got %s", result.FileSizeMatch)
		}
		if result.DigestSha256 != "verified" {
			t.Errorf("expected DigestSha256=verified, got %s", result.DigestSha256)
		}
	})

	t.Run("file does not exist", func(t *testing.T) {
		checksums := map[string]map[string]string{}
		result := ctrl.verifyExistingFile(filepath.Join(tmpDir, "nonexistent.iso"), 100, checksums, "nonexistent.iso")
		if result != nil {
			t.Error("expected nil for nonexistent file")
		}
	})

	t.Run("file size mismatch", func(t *testing.T) {
		checksums := map[string]map[string]string{}
		result := ctrl.verifyExistingFile(testFile, 999999, checksums, "test.iso")
		if result != nil {
			t.Error("expected nil for size mismatch")
		}
	})

	t.Run("checksum mismatch", func(t *testing.T) {
		checksums := map[string]map[string]string{
			"sha256": {"test.iso": "wronghash"},
		}
		result := ctrl.verifyExistingFile(testFile, int64(len(testContent)), checksums, "test.iso")
		if result != nil {
			t.Error("expected nil for checksum mismatch")
		}
	})

	t.Run("no checksums available (passes)", func(t *testing.T) {
		checksums := map[string]map[string]string{}
		result := ctrl.verifyExistingFile(testFile, int64(len(testContent)), checksums, "test.iso")
		if result == nil {
			t.Fatal("expected verification result when no checksums available")
		}
		if result.DigestSha256 != "not_found" {
			t.Errorf("expected DigestSha256=not_found, got %s", result.DigestSha256)
		}
	})
}

func TestDiscoverChecksums(t *testing.T) {
	// Create a test server that serves checksum files
	mux := http.NewServeMux()

	// SHA256SUMS in the same directory
	mux.HandleFunc("/images/SHA256SUMS", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "abc123def456  mini.iso\n")
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	ctrl := &Controller{}

	t.Run("discovers checksums from directory", func(t *testing.T) {
		fileURL := server.URL + "/images/mini.iso"
		checksums := ctrl.discoverChecksums(fileURL)

		if sha256, ok := checksums["sha256"]; !ok {
			t.Error("expected sha256 checksums")
		} else if hash, ok := sha256["mini.iso"]; !ok || hash != "abc123def456" {
			t.Errorf("expected sha256 hash abc123def456, got %v", sha256)
		}
	})

	t.Run("handles missing checksum files gracefully", func(t *testing.T) {
		// Use a separate server with no checksum files
		emptyServer := httptest.NewServer(http.NewServeMux())
		defer emptyServer.Close()

		fileURL := emptyServer.URL + "/some/path/file.iso"
		checksums := ctrl.discoverChecksums(fileURL)

		// Should return empty map, not error
		if len(checksums) != 0 {
			t.Errorf("expected empty checksums for missing files, got %v", checksums)
		}
	})

	t.Run("handles invalid URL", func(t *testing.T) {
		checksums := ctrl.discoverChecksums("not-a-valid-url")
		if len(checksums) != 0 {
			t.Errorf("expected empty checksums for invalid URL, got %v", checksums)
		}
	})
}

func TestDownloadAndVerify(t *testing.T) {
	// Create test content
	testContent := []byte("test ISO content for download verification")

	// Calculate checksums
	sha256Hash := sha256.New()
	sha256Hash.Write(testContent)
	expectedSha256 := fmt.Sprintf("%x", sha256Hash.Sum(nil))

	// Create test server
	mux := http.NewServeMux()

	mux.HandleFunc("/test.iso", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(testContent)))
			return
		}
		w.Write(testContent)
	})

	mux.HandleFunc("/SHA256SUMS", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s  test.iso\n", expectedSha256)
	})

	mux.HandleFunc("/404.iso", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	// Create temp directory for downloads
	tmpDir, err := os.MkdirTemp("", "download-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	ctrl := &Controller{}

	t.Run("successful download with verification", func(t *testing.T) {
		destPath := filepath.Join(tmpDir, "downloaded.iso")
		result, err := ctrl.downloadAndVerify(context.Background(), server.URL+"/test.iso", destPath)

		if err != nil {
			t.Fatalf("download failed: %v", err)
		}

		if result.FileSizeMatch != "verified" {
			t.Errorf("expected FileSizeMatch=verified, got %s", result.FileSizeMatch)
		}
		if result.DigestSha256 != "verified" {
			t.Errorf("expected DigestSha256=verified, got %s", result.DigestSha256)
		}

		// Verify file was written
		content, err := os.ReadFile(destPath)
		if err != nil {
			t.Fatalf("failed to read downloaded file: %v", err)
		}
		if string(content) != string(testContent) {
			t.Error("downloaded content doesn't match")
		}
	})

	t.Run("skips download for valid existing file", func(t *testing.T) {
		destPath := filepath.Join(tmpDir, "existing.iso")
		// Pre-create the file
		if err := os.WriteFile(destPath, testContent, 0644); err != nil {
			t.Fatalf("failed to create existing file: %v", err)
		}

		result, err := ctrl.downloadAndVerify(context.Background(), server.URL+"/test.iso", destPath)

		if err != nil {
			t.Fatalf("verification failed: %v", err)
		}
		if result.FileSizeMatch != "verified" {
			t.Errorf("expected FileSizeMatch=verified, got %s", result.FileSizeMatch)
		}
	})

	t.Run("handles HTTP 404", func(t *testing.T) {
		destPath := filepath.Join(tmpDir, "notfound.iso")
		result, err := ctrl.downloadAndVerify(context.Background(), server.URL+"/404.iso", destPath)

		if err == nil {
			t.Error("expected error for 404")
		}
		if result.FileSizeMatch != "failed" {
			t.Errorf("expected FileSizeMatch=failed, got %s", result.FileSizeMatch)
		}
	})

	t.Run("handles connection error", func(t *testing.T) {
		destPath := filepath.Join(tmpDir, "connfail.iso")
		result, err := ctrl.downloadAndVerify(context.Background(), "http://localhost:99999/test.iso", destPath)

		if err == nil {
			t.Error("expected error for connection failure")
		}
		if result.FileSizeMatch != "failed" {
			t.Errorf("expected FileSizeMatch=failed, got %s", result.FileSizeMatch)
		}
	})
}

package controller

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/isoboot/isoboot/internal/k8s"
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
				"subdir/file2.iso": "def456",
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
		name     string
		sources  []checksumSource
		hashType string
		fileURL  string
		expected string
	}{
		{
			name: "matching checksum",
			sources: []checksumSource{
				{hashType: "sha256", checksumURL: "http://example.com/SHA256SUMS", checksums: map[string]string{"test.iso": actualHash}},
			},
			hashType: "sha256",
			fileURL:  "http://example.com/test.iso",
			expected: "verified",
		},
		{
			name: "matching checksum case insensitive",
			sources: []checksumSource{
				{hashType: "sha256", checksumURL: "http://example.com/SHA256SUMS", checksums: map[string]string{"test.iso": strings.ToUpper(actualHash)}},
			},
			hashType: "sha256",
			fileURL:  "http://example.com/test.iso",
			expected: "verified",
		},
		{
			name: "mismatched checksum",
			sources: []checksumSource{
				{hashType: "sha256", checksumURL: "http://example.com/SHA256SUMS", checksums: map[string]string{"test.iso": "wronghash1234567890abcdef"}},
			},
			hashType: "sha256",
			fileURL:  "http://example.com/test.iso",
			expected: "failed",
		},
		{
			name: "hash type not found",
			sources: []checksumSource{
				{hashType: "sha512", checksumURL: "http://example.com/SHA512SUMS", checksums: map[string]string{"test.iso": "somehash"}},
			},
			hashType: "sha256",
			fileURL:  "http://example.com/test.iso",
			expected: "not_found",
		},
		{
			name: "filename not found",
			sources: []checksumSource{
				{hashType: "sha256", checksumURL: "http://example.com/SHA256SUMS", checksums: map[string]string{"other.iso": actualHash}},
			},
			hashType: "sha256",
			fileURL:  "http://example.com/test.iso",
			expected: "not_found",
		},
		{
			name:     "empty checksums",
			sources:  []checksumSource{},
			hashType: "sha256",
			fileURL:  "http://example.com/test.iso",
			expected: "not_found",
		},
		{
			name: "relative path match",
			sources: []checksumSource{
				{hashType: "sha256", checksumURL: "http://example.com/images/SHA256SUMS", checksums: map[string]string{"netboot/test.iso": actualHash}},
			},
			hashType: "sha256",
			fileURL:  "http://example.com/images/netboot/test.iso",
			expected: "verified",
		},
		{
			name: "relative path with ./ prefix",
			sources: []checksumSource{
				{hashType: "sha256", checksumURL: "http://example.com/images/SHA256SUMS", checksums: map[string]string{"./netboot/test.iso": actualHash}},
			},
			hashType: "sha256",
			fileURL:  "http://example.com/images/netboot/test.iso",
			expected: "verified",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hash := sha256.New()
			hash.Write(testData)

			result := verifyChecksum(tt.sources, tt.hashType, hash, tt.fileURL)
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
	if err := os.WriteFile(testFile, testContent, 0o600); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	// Calculate expected checksums
	sha256Hash := sha256.New()
	sha256Hash.Write(testContent)
	expectedSha256 := fmt.Sprintf("%x", sha256Hash.Sum(nil))

	ctrl := &Controller{httpClient: http.DefaultClient}

	t.Run("valid file with matching checksums", func(t *testing.T) {
		checksums := []checksumSource{
			{hashType: "sha256", checksumURL: "http://example.com/SHA256SUMS", checksums: map[string]string{"test.iso": expectedSha256}},
		}

		result := ctrl.verifyExistingFile(testFile, int64(len(testContent)), checksums, "http://example.com/test.iso")
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
		checksums := []checksumSource{}
		result := ctrl.verifyExistingFile(filepath.Join(tmpDir, "nonexistent.iso"), 100, checksums, "http://example.com/nonexistent.iso")
		if result != nil {
			t.Error("expected nil for nonexistent file")
		}
	})

	t.Run("file size mismatch", func(t *testing.T) {
		checksums := []checksumSource{}
		result := ctrl.verifyExistingFile(testFile, 999999, checksums, "http://example.com/test.iso")
		if result != nil {
			t.Error("expected nil for size mismatch")
		}
	})

	t.Run("checksum mismatch", func(t *testing.T) {
		checksums := []checksumSource{
			{hashType: "sha256", checksumURL: "http://example.com/SHA256SUMS", checksums: map[string]string{"test.iso": "wronghash"}},
		}
		result := ctrl.verifyExistingFile(testFile, int64(len(testContent)), checksums, "http://example.com/test.iso")
		if result != nil {
			t.Error("expected nil for checksum mismatch")
		}
	})

	t.Run("no checksums available (triggers re-download)", func(t *testing.T) {
		checksums := []checksumSource{}
		result := ctrl.verifyExistingFile(testFile, int64(len(testContent)), checksums, "http://example.com/test.iso")
		if result != nil {
			t.Error("expected nil when no checksums available (should trigger re-download)")
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

	ctrl := &Controller{httpClient: http.DefaultClient}

	t.Run("discovers checksums from directory", func(t *testing.T) {
		fileURL := server.URL + "/images/mini.iso"
		sources := ctrl.discoverChecksums(context.Background(), fileURL)

		if len(sources) == 0 {
			t.Fatal("expected at least one checksum source")
		}

		// Find the SHA256 source
		var found bool
		for _, src := range sources {
			if src.hashType == "sha256" {
				found = true
				if hash, ok := src.checksums["mini.iso"]; !ok || hash != "abc123def456" {
					t.Errorf("expected SHA256 hash abc123def456, got %v", src.checksums)
				}
				// Verify checksumURL is correct
				expectedURL := server.URL + "/images/SHA256SUMS"
				if src.checksumURL != expectedURL {
					t.Errorf("expected checksumURL %s, got %s", expectedURL, src.checksumURL)
				}
			}
		}
		if !found {
			t.Error("expected SHA256 checksum source")
		}
	})

	t.Run("handles missing checksum files gracefully", func(t *testing.T) {
		// Use a separate server with no checksum files
		emptyServer := httptest.NewServer(http.NewServeMux())
		defer emptyServer.Close()

		fileURL := emptyServer.URL + "/some/path/file.iso"
		sources := ctrl.discoverChecksums(context.Background(), fileURL)

		// Should return empty slice, not error
		if len(sources) != 0 {
			t.Errorf("expected empty sources for missing files, got %v", sources)
		}
	})

	t.Run("handles invalid URL", func(t *testing.T) {
		sources := ctrl.discoverChecksums(context.Background(), "not-a-valid-url")
		if len(sources) != 0 {
			t.Errorf("expected empty sources for invalid URL, got %v", sources)
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

	ctrl := &Controller{httpClient: http.DefaultClient}

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
		if err := os.WriteFile(destPath, testContent, 0o600); err != nil {
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
		// Use a closed httptest.Server to reliably force a connection error
		connFailServer := httptest.NewServer(http.NewServeMux())
		connFailServer.Close()

		destPath := filepath.Join(tmpDir, "connfail.iso")
		result, err := ctrl.downloadAndVerify(context.Background(), connFailServer.URL+"/test.iso", destPath)

		if err == nil {
			t.Error("expected error for connection failure")
		}
		if result.FileSizeMatch != "failed" {
			t.Errorf("expected FileSizeMatch=failed, got %s", result.FileSizeMatch)
		}
	})

	t.Run("handles HEAD request 4xx error", func(t *testing.T) {
		// Create server that returns 403 Forbidden on HEAD
		forbiddenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
		}))
		defer forbiddenServer.Close()

		destPath := filepath.Join(tmpDir, "forbidden.iso")
		result, err := ctrl.downloadAndVerify(context.Background(), forbiddenServer.URL+"/forbidden.iso", destPath)

		if err == nil {
			t.Error("expected error for 403 Forbidden")
		}
		if !strings.Contains(err.Error(), "403") {
			t.Errorf("expected error to mention 403, got: %v", err)
		}
		if result.FileSizeMatch != "failed" {
			t.Errorf("expected FileSizeMatch=failed, got %s", result.FileSizeMatch)
		}
	})

	t.Run("handles HEAD request 5xx error", func(t *testing.T) {
		// Create server that returns 500 Internal Server Error on HEAD
		errorServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer errorServer.Close()

		destPath := filepath.Join(tmpDir, "servererror.iso")
		result, err := ctrl.downloadAndVerify(context.Background(), errorServer.URL+"/error.iso", destPath)

		if err == nil {
			t.Error("expected error for 500 Internal Server Error")
		}
		if !strings.Contains(err.Error(), "500") {
			t.Errorf("expected error to mention 500, got: %v", err)
		}
		if result.FileSizeMatch != "failed" {
			t.Errorf("expected FileSizeMatch=failed, got %s", result.FileSizeMatch)
		}
	})

	t.Run("cleans up temp file on checksum failure", func(t *testing.T) {
		// Create server with wrong checksum (use subdirectory to avoid double-slash URL issues)
		badChecksumContent := []byte("content with wrong checksum")
		badMux := http.NewServeMux()
		badMux.HandleFunc("/downloads/SHA256SUMS", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, "wronghash  badchecksum.iso\n")
		})
		badMux.HandleFunc("/downloads/badchecksum.iso", func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodHead {
				w.Header().Set("Content-Length", fmt.Sprintf("%d", len(badChecksumContent)))
				return
			}
			w.Write(badChecksumContent)
		})
		badServer := httptest.NewServer(badMux)
		defer badServer.Close()

		destPath := filepath.Join(tmpDir, "badchecksum.iso")
		tmpPath := destPath + ".tmp"

		_, err := ctrl.downloadAndVerify(context.Background(), badServer.URL+"/downloads/badchecksum.iso", destPath)

		if err == nil {
			t.Error("expected error for checksum failure")
		}
		if !strings.Contains(err.Error(), "checksum verification failed") {
			t.Errorf("expected checksum failure error, got: %v", err)
		}

		// Verify temp file was cleaned up
		if _, statErr := os.Stat(tmpPath); !os.IsNotExist(statErr) {
			t.Error("expected temp file to be removed after checksum failure")
		}

		// Verify final file was not created
		if _, statErr := os.Stat(destPath); !os.IsNotExist(statErr) {
			t.Error("expected final file to not exist after checksum failure")
		}
	})
}

func TestFilenameFromURL(t *testing.T) {
	tests := []struct {
		name        string
		url         string
		expected    string
		expectError bool
	}{
		{
			name:        "normal URL with filename",
			url:         "http://example.com/path/to/file.iso",
			expected:    "file.iso",
			expectError: false,
		},
		{
			name:        "URL ending with slash returns last component",
			url:         "http://example.com/path/to/",
			expected:    "to", // filepath.Base("/path/to/") = "to"
			expectError: false,
		},
		{
			name:        "URL with only root path",
			url:         "http://example.com/",
			expected:    "",
			expectError: true, // filepath.Base("/") = "/"
		},
		{
			name:        "URL with no path",
			url:         "http://example.com",
			expected:    "",
			expectError: true, // filepath.Base("") = "."
		},
		{
			name:        "URL with query string",
			url:         "http://example.com/file.iso?token=abc",
			expected:    "file.iso",
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := filenameFromURL(tt.url)
			if tt.expectError {
				if err == nil {
					t.Errorf("expected error for URL %q, got result %q", tt.url, result)
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error for URL %q: %v", tt.url, err)
				}
				if result != tt.expected {
					t.Errorf("expected %q, got %q", tt.expected, result)
				}
			}
		})
	}
}

func TestParseChecksumFileDuplicateBaseFilenames(t *testing.T) {
	// Tests that all full paths are stored (exact relative path matching uses these)
	input := `abc123  dir1/file.iso
def456  dir2/file.iso
ghi789  dir3/file.iso`

	result := parseChecksumFile(strings.NewReader(input))

	// Full paths should all be present
	if hash, ok := result["dir1/file.iso"]; !ok || hash != "abc123" {
		t.Errorf("expected dir1/file.iso=abc123, got %v", result["dir1/file.iso"])
	}
	if hash, ok := result["dir2/file.iso"]; !ok || hash != "def456" {
		t.Errorf("expected dir2/file.iso=def456, got %v", result["dir2/file.iso"])
	}
	if hash, ok := result["dir3/file.iso"]; !ok || hash != "ghi789" {
		t.Errorf("expected dir3/file.iso=ghi789, got %v", result["dir3/file.iso"])
	}

	// Base filename should NOT be stored (exact relative path matching is used)
	if _, ok := result["file.iso"]; ok {
		t.Errorf("base filename file.iso should not be stored, got %v", result["file.iso"])
	}
}

func TestRelativePathFromChecksumURL(t *testing.T) {
	tests := []struct {
		name        string
		checksumURL string
		fileURL     string
		expected    string
	}{
		{
			name:        "file in same directory",
			checksumURL: "http://example.com/images/SHA256SUMS",
			fileURL:     "http://example.com/images/mini.iso",
			expected:    "mini.iso",
		},
		{
			name:        "file in subdirectory",
			checksumURL: "http://example.com/images/SHA256SUMS",
			fileURL:     "http://example.com/images/netboot/mini.iso",
			expected:    "netboot/mini.iso",
		},
		{
			name:        "file in nested subdirectory",
			checksumURL: "http://example.com/dists/trixie/main/SHA256SUMS",
			fileURL:     "http://example.com/dists/trixie/main/installer-amd64/current/images/netboot/mini.iso",
			expected:    "installer-amd64/current/images/netboot/mini.iso",
		},
		{
			name:        "file not under checksum directory",
			checksumURL: "http://example.com/images/SHA256SUMS",
			fileURL:     "http://example.com/other/mini.iso",
			expected:    "",
		},
		{
			name:        "different hosts",
			checksumURL: "http://example.com/images/SHA256SUMS",
			fileURL:     "http://other.com/images/mini.iso",
			expected:    "", // Different host, path prefix doesn't apply
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := relativePathFromChecksumURL(tt.checksumURL, tt.fileURL)
			if result != tt.expected {
				t.Errorf("relativePathFromChecksumURL(%q, %q) = %q, want %q",
					tt.checksumURL, tt.fileURL, result, tt.expected)
			}
		})
	}
}

func TestLookupChecksumByRelativePath(t *testing.T) {
	checksums := map[string]string{
		"mini.iso":               "hash1",
		"netboot/mini.iso":       "hash2",
		"./netboot/gtk/mini.iso": "hash3",
	}

	tests := []struct {
		name         string
		relativePath string
		expectedHash string
		expectedOK   bool
	}{
		{
			name:         "exact match",
			relativePath: "mini.iso",
			expectedHash: "hash1",
			expectedOK:   true,
		},
		{
			name:         "path match",
			relativePath: "netboot/mini.iso",
			expectedHash: "hash2",
			expectedOK:   true,
		},
		{
			name:         "match with ./ prefix in checksums",
			relativePath: "netboot/gtk/mini.iso",
			expectedHash: "hash3",
			expectedOK:   true,
		},
		{
			name:         "not found",
			relativePath: "other.iso",
			expectedHash: "",
			expectedOK:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hash, ok := lookupChecksumByRelativePath(checksums, tt.relativePath)
			if hash != tt.expectedHash || ok != tt.expectedOK {
				t.Errorf("lookupChecksumByRelativePath(%q) = (%q, %v), want (%q, %v)",
					tt.relativePath, hash, ok, tt.expectedHash, tt.expectedOK)
			}
		})
	}

	// Tests for basename fallback when checksum file only has base filenames
	t.Run("basename fallback - unique match", func(t *testing.T) {
		// Checksum file only lists base filename, file is in subdirectory
		cs := map[string]string{
			"installer.iso": "uniquehash",
		}
		hash, ok := lookupChecksumByRelativePath(cs, "subdir/installer.iso")
		if !ok || hash != "uniquehash" {
			t.Errorf("expected basename fallback to match, got (%q, %v)", hash, ok)
		}
	})

	t.Run("basename fallback - ambiguous", func(t *testing.T) {
		// Multiple entries with same basename - should not match
		cs := map[string]string{
			"a/file.iso": "hash_a",
			"b/file.iso": "hash_b",
		}
		hash, ok := lookupChecksumByRelativePath(cs, "c/file.iso")
		if ok {
			t.Errorf("expected no match for ambiguous basename, got (%q, %v)", hash, ok)
		}
	})
}

func TestFormatHashMismatch(t *testing.T) {
	tests := []struct {
		name     string
		expected string
		actual   string
		want     string
	}{
		{
			name:     "first 4 chars differ",
			expected: "abcd1234567890abcdef",
			actual:   "1234abcd567890abcdef",
			want:     "expected abcd..., got 1234...",
		},
		{
			name:     "last 4 chars differ",
			expected: "abcd1234567890abcd",
			actual:   "abcd1234567890efgh",
			want:     "expected ...abcd, got ...efgh",
		},
		{
			name:     "middle differs",
			expected: "abcd1111111111efgh",
			actual:   "abcd2222222222efgh",
			want:     "hash mismatch",
		},
		{
			name:     "short hashes",
			expected: "abc",
			actual:   "def",
			want:     "expected abc, got def",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatHashMismatch(tt.expected, tt.actual)
			if got != tt.want {
				t.Errorf("expected %q, got %q", tt.want, got)
			}
		})
	}
}

// fakeHTTPDoer implements HTTPDoer for testing.
type fakeHTTPDoer struct {
	doFunc func(req *http.Request) (*http.Response, error)
}

func (f *fakeHTTPDoer) Do(req *http.Request) (*http.Response, error) {
	return f.doFunc(req)
}

// httpResponse is a helper to build an *http.Response for mock tests.
func httpResponse(status int, body string, contentLength int64) *http.Response {
	return &http.Response{
		StatusCode:    status,
		Body:          io.NopCloser(strings.NewReader(body)),
		ContentLength: contentLength,
		Header:        make(http.Header),
	}
}

// httpResponseBytes is like httpResponse but takes a byte slice body.
func httpResponseBytes(status int, body []byte) *http.Response {
	return &http.Response{
		StatusCode:    status,
		Body:          io.NopCloser(bytes.NewReader(body)),
		ContentLength: int64(len(body)),
		Header:        make(http.Header),
	}
}

func TestReconcileDiskImage_InitializePending(t *testing.T) {
	fake := newFakeK8sClient()
	fake.diskImages["test-iso"] = &k8s.DiskImage{
		Name: "test-iso",
		ISO:  "http://example.com/images/test.iso",
	}

	ctrl := &Controller{k8sClient: fake, httpClient: http.DefaultClient}
	di := fake.diskImages["test-iso"]
	ctrl.reconcileDiskImage(context.Background(), di)

	status, ok := fake.getDiskImageStatus("test-iso")
	if !ok {
		t.Fatal("expected DiskImage status to be updated")
	}
	if status.Phase != "Pending" {
		t.Errorf("expected phase Pending, got %q", status.Phase)
	}
	if status.Message != "Waiting for download" {
		t.Errorf("expected message 'Waiting for download', got %q", status.Message)
	}
	if status.ISO == nil {
		t.Fatal("expected ISO verification status")
	}
	if status.ISO.FileSizeMatch != "pending" {
		t.Errorf("expected ISO FileSizeMatch=pending, got %s", status.ISO.FileSizeMatch)
	}
	if status.Firmware != nil {
		t.Error("expected no Firmware verification status when firmware not specified")
	}
}

func TestReconcileDiskImage_InitializePendingWithFirmware(t *testing.T) {
	fake := newFakeK8sClient()
	fake.diskImages["test-iso"] = &k8s.DiskImage{
		Name:     "test-iso",
		ISO:      "http://example.com/images/test.iso",
		Firmware: "http://example.com/images/firmware/initrd.gz",
	}

	ctrl := &Controller{k8sClient: fake, httpClient: http.DefaultClient}
	di := fake.diskImages["test-iso"]
	ctrl.reconcileDiskImage(context.Background(), di)

	status, ok := fake.getDiskImageStatus("test-iso")
	if !ok {
		t.Fatal("expected DiskImage status to be updated")
	}
	if status.Phase != "Pending" {
		t.Errorf("expected phase Pending, got %q", status.Phase)
	}
	if status.ISO == nil {
		t.Fatal("expected ISO verification status")
	}
	if status.Firmware == nil {
		t.Fatal("expected Firmware verification status when firmware specified")
	}
	if status.Firmware.FileSizeMatch != "pending" {
		t.Errorf("expected Firmware FileSizeMatch=pending, got %s", status.Firmware.FileSizeMatch)
	}
}

func TestReconcileDiskImage_CompleteIsNoop(t *testing.T) {
	fake := newFakeK8sClient()
	fake.diskImages["test-iso"] = &k8s.DiskImage{
		Name:   "test-iso",
		ISO:    "http://example.com/images/test.iso",
		Status: k8s.DiskImageStatus{Phase: "Complete"},
	}

	ctrl := &Controller{k8sClient: fake, httpClient: http.DefaultClient}
	di := fake.diskImages["test-iso"]
	ctrl.reconcileDiskImage(context.Background(), di)

	if _, ok := fake.getDiskImageStatus("test-iso"); ok {
		t.Error("expected no status update for Complete DiskImage")
	}
}

func TestReconcileDiskImage_FailedIsNoop(t *testing.T) {
	fake := newFakeK8sClient()
	fake.diskImages["test-iso"] = &k8s.DiskImage{
		Name:   "test-iso",
		ISO:    "http://example.com/images/test.iso",
		Status: k8s.DiskImageStatus{Phase: "Failed"},
	}

	ctrl := &Controller{k8sClient: fake, httpClient: http.DefaultClient}
	di := fake.diskImages["test-iso"]
	ctrl.reconcileDiskImage(context.Background(), di)

	if _, ok := fake.getDiskImageStatus("test-iso"); ok {
		t.Error("expected no status update for Failed DiskImage")
	}
}

func TestDownloadDiskImage_NoISOBasePath(t *testing.T) {
	fake := newFakeK8sClient()
	fake.diskImages["test-iso"] = &k8s.DiskImage{
		Name: "test-iso",
		ISO:  "http://example.com/images/test.iso",
	}

	ctrl := &Controller{
		k8sClient:   fake,
		httpClient:  http.DefaultClient,
		isoBasePath: "", // not configured
	}

	di := fake.diskImages["test-iso"]
	ctrl.downloadDiskImage(context.Background(), di)

	status, ok := fake.getDiskImageStatus("test-iso")
	if !ok {
		t.Fatal("expected DiskImage status to be updated")
	}
	if status.Phase != "Failed" {
		t.Errorf("expected phase Failed, got %q", status.Phase)
	}
	if !strings.Contains(status.Message, "isoBasePath not configured") {
		t.Errorf("expected message about isoBasePath, got %q", status.Message)
	}
}

func TestDownloadDiskImage_InvalidISOURL(t *testing.T) {
	fake := newFakeK8sClient()
	fake.diskImages["test-iso"] = &k8s.DiskImage{
		Name: "test-iso",
		ISO:  "http://example.com/", // no filename
	}

	tmpDir, err := os.MkdirTemp("", "diskimage-mock-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	ctrl := &Controller{
		k8sClient:   fake,
		httpClient:  http.DefaultClient,
		isoBasePath: tmpDir,
	}

	di := fake.diskImages["test-iso"]
	ctrl.downloadDiskImage(context.Background(), di)

	status, ok := fake.getDiskImageStatus("test-iso")
	if !ok {
		t.Fatal("expected DiskImage status to be updated")
	}
	if status.Phase != "Failed" {
		t.Errorf("expected phase Failed, got %q", status.Phase)
	}
	if !strings.Contains(status.Message, "Invalid ISO URL") {
		t.Errorf("expected message about invalid URL, got %q", status.Message)
	}
}

func TestDownloadDiskImage_SuccessfulDownload(t *testing.T) {
	isoContent := []byte("fake ISO content for mock download test")
	sha256Hash := sha256.New()
	sha256Hash.Write(isoContent)
	expectedSha256 := fmt.Sprintf("%x", sha256Hash.Sum(nil))

	fake := newFakeK8sClient()
	fake.diskImages["test-iso"] = &k8s.DiskImage{
		Name: "test-iso",
		ISO:  "http://example.com/images/test.iso",
	}

	tmpDir, err := os.MkdirTemp("", "diskimage-mock-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// discoverChecksums tries the ISO's directory and up to 2 parent directories,
	// so /images/SHA256SUMS and /SHA256SUMS will be requested. Only serve the
	// same-directory one; the rest return 404.
	mockHTTP := &fakeHTTPDoer{
		doFunc: func(req *http.Request) (*http.Response, error) {
			switch req.URL.Path {
			case "/images/test.iso":
				return httpResponseBytes(200, isoContent), nil
			case "/images/SHA256SUMS":
				body := fmt.Sprintf("%s  test.iso\n", expectedSha256)
				return httpResponse(200, body, -1), nil
			default:
				return httpResponse(404, "not found", -1), nil
			}
		},
	}

	ctrl := &Controller{
		k8sClient:   fake,
		httpClient:  mockHTTP,
		isoBasePath: tmpDir,
	}

	di := fake.diskImages["test-iso"]
	ctrl.downloadDiskImage(context.Background(), di)

	status, ok := fake.getDiskImageStatus("test-iso")
	if !ok {
		t.Fatal("expected DiskImage status to be updated")
	}
	if status.Phase != "Complete" {
		t.Fatalf("expected phase Complete, got %q (message: %s)", status.Phase, status.Message)
	}
	if status.Progress != 100 {
		t.Errorf("expected progress 100, got %d", status.Progress)
	}
	if status.ISO == nil {
		t.Fatal("expected ISO verification status")
	}
	if status.ISO.FileSizeMatch != "verified" {
		t.Errorf("expected ISO FileSizeMatch=verified, got %s", status.ISO.FileSizeMatch)
	}
	if status.ISO.DigestSha256 != "verified" {
		t.Errorf("expected ISO DigestSha256=verified, got %s", status.ISO.DigestSha256)
	}
	if status.ISO.DigestSha512 != "not_found" {
		t.Errorf("expected ISO DigestSha512=not_found (no SHA512SUMS served), got %s", status.ISO.DigestSha512)
	}

	// Verify file was written to disk
	isoPath := filepath.Join(tmpDir, "test-iso", "test.iso")
	content, err := os.ReadFile(isoPath)
	if err != nil {
		t.Fatalf("failed to read downloaded ISO: %v", err)
	}
	if string(content) != string(isoContent) {
		t.Error("downloaded ISO content doesn't match")
	}
}

func TestDownloadDiskImage_WithFirmware(t *testing.T) {
	isoContent := []byte("fake ISO content")
	fwContent := []byte("fake firmware content")

	fake := newFakeK8sClient()
	fake.diskImages["test-iso"] = &k8s.DiskImage{
		Name:     "test-iso",
		ISO:      "http://example.com/images/test.iso",
		Firmware: "http://example.com/firmware/initrd.gz",
	}

	tmpDir, err := os.MkdirTemp("", "diskimage-mock-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	mockHTTP := &fakeHTTPDoer{
		doFunc: func(req *http.Request) (*http.Response, error) {
			switch req.URL.Path {
			case "/images/test.iso":
				return httpResponseBytes(200, isoContent), nil
			case "/firmware/initrd.gz":
				return httpResponseBytes(200, fwContent), nil
			default:
				return httpResponse(404, "not found", -1), nil
			}
		},
	}

	ctrl := &Controller{
		k8sClient:   fake,
		httpClient:  mockHTTP,
		isoBasePath: tmpDir,
	}

	di := fake.diskImages["test-iso"]
	ctrl.downloadDiskImage(context.Background(), di)

	status, ok := fake.getDiskImageStatus("test-iso")
	if !ok {
		t.Fatal("expected DiskImage status to be updated")
	}
	if status.Phase != "Complete" {
		t.Fatalf("expected phase Complete, got %q (message: %s)", status.Phase, status.Message)
	}
	if status.Progress != 100 {
		t.Errorf("expected progress 100, got %d", status.Progress)
	}
	if status.Firmware == nil {
		t.Fatal("expected Firmware verification status")
	}
	if status.Firmware.FileSizeMatch != "verified" {
		t.Errorf("expected Firmware FileSizeMatch=verified, got %s", status.Firmware.FileSizeMatch)
	}
	if status.Firmware.DigestSha256 != "not_found" {
		t.Errorf("expected Firmware DigestSha256=not_found (no checksum served), got %s", status.Firmware.DigestSha256)
	}
	if status.Firmware.DigestSha512 != "not_found" {
		t.Errorf("expected Firmware DigestSha512=not_found (no checksum served), got %s", status.Firmware.DigestSha512)
	}

	// Verify both files were written
	isoPath := filepath.Join(tmpDir, "test-iso", "test.iso")
	if _, err := os.Stat(isoPath); err != nil {
		t.Errorf("expected ISO file to exist: %v", err)
	}
	fwPath := filepath.Join(tmpDir, "test-iso", "firmware", "initrd.gz")
	fwData, err := os.ReadFile(fwPath)
	if err != nil {
		t.Fatalf("failed to read firmware file: %v", err)
	}
	if string(fwData) != string(fwContent) {
		t.Error("firmware content doesn't match")
	}
}

func TestDownloadDiskImage_HTTPError(t *testing.T) {
	fake := newFakeK8sClient()
	fake.diskImages["test-iso"] = &k8s.DiskImage{
		Name: "test-iso",
		ISO:  "http://example.com/images/test.iso",
	}

	tmpDir, err := os.MkdirTemp("", "diskimage-mock-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	mockHTTP := &fakeHTTPDoer{
		doFunc: func(req *http.Request) (*http.Response, error) {
			return httpResponse(500, "internal server error", -1), nil
		},
	}

	ctrl := &Controller{
		k8sClient:   fake,
		httpClient:  mockHTTP,
		isoBasePath: tmpDir,
	}

	di := fake.diskImages["test-iso"]
	ctrl.downloadDiskImage(context.Background(), di)

	status, ok := fake.getDiskImageStatus("test-iso")
	if !ok {
		t.Fatal("expected DiskImage status to be updated")
	}
	if status.Phase != "Failed" {
		t.Errorf("expected phase Failed, got %q", status.Phase)
	}
	if !strings.Contains(status.Message, "ISO download failed") {
		t.Errorf("expected message about ISO download failure, got %q", status.Message)
	}
}

func TestDownloadDiskImage_FirmwareFailsAfterISOSuccess(t *testing.T) {
	isoContent := []byte("fake ISO content")

	fake := newFakeK8sClient()
	fake.diskImages["test-iso"] = &k8s.DiskImage{
		Name:     "test-iso",
		ISO:      "http://example.com/images/test.iso",
		Firmware: "http://example.com/firmware/initrd.gz",
	}

	tmpDir, err := os.MkdirTemp("", "diskimage-mock-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	mockHTTP := &fakeHTTPDoer{
		doFunc: func(req *http.Request) (*http.Response, error) {
			switch req.URL.Path {
			case "/images/test.iso":
				return httpResponseBytes(200, isoContent), nil
			case "/firmware/initrd.gz":
				return httpResponse(500, "internal server error", -1), nil
			default:
				return httpResponse(404, "not found", -1), nil
			}
		},
	}

	ctrl := &Controller{
		k8sClient:   fake,
		httpClient:  mockHTTP,
		isoBasePath: tmpDir,
	}

	di := fake.diskImages["test-iso"]
	ctrl.downloadDiskImage(context.Background(), di)

	status, ok := fake.getDiskImageStatus("test-iso")
	if !ok {
		t.Fatal("expected DiskImage status to be updated")
	}
	if status.Phase != "Failed" {
		t.Errorf("expected phase Failed, got %q", status.Phase)
	}
	if !strings.Contains(status.Message, "Firmware download failed") {
		t.Errorf("expected message about firmware failure, got %q", status.Message)
	}

	// ISO file should still exist (downloaded before firmware failed)
	isoPath := filepath.Join(tmpDir, "test-iso", "test.iso")
	if _, err := os.Stat(isoPath); err != nil {
		t.Errorf("expected ISO file to exist after firmware failure: %v", err)
	}
}

func TestDownloadDiskImage_ConnectionError(t *testing.T) {
	fake := newFakeK8sClient()
	fake.diskImages["test-iso"] = &k8s.DiskImage{
		Name: "test-iso",
		ISO:  "http://example.com/images/test.iso",
	}

	tmpDir, err := os.MkdirTemp("", "diskimage-mock-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	mockHTTP := &fakeHTTPDoer{
		doFunc: func(req *http.Request) (*http.Response, error) {
			return nil, fmt.Errorf("connection refused")
		},
	}

	ctrl := &Controller{
		k8sClient:   fake,
		httpClient:  mockHTTP,
		isoBasePath: tmpDir,
	}

	di := fake.diskImages["test-iso"]
	ctrl.downloadDiskImage(context.Background(), di)

	status, ok := fake.getDiskImageStatus("test-iso")
	if !ok {
		t.Fatal("expected DiskImage status to be updated")
	}
	if status.Phase != "Failed" {
		t.Errorf("expected phase Failed, got %q", status.Phase)
	}
	if !strings.Contains(status.Message, "ISO download failed") {
		t.Errorf("expected message about ISO download failure, got %q", status.Message)
	}
}

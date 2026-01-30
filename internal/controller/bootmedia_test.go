package controller

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
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

func TestChecksumKey(t *testing.T) {
	tests := []struct {
		name        string
		fileURL     string
		checksumURL string
		expected    string
	}{
		{
			name:        "same directory",
			fileURL:     "https://cdimage.debian.org/cdimage/firmware/trixie/current/firmware.cpio.gz",
			checksumURL: "https://cdimage.debian.org/cdimage/firmware/trixie/current/SHA256SUMS",
			expected:    "firmware.cpio.gz",
		},
		{
			name:        "subdirectory",
			fileURL:     "https://deb.debian.org/debian/dists/trixie/main/installer-amd64/current/images/netboot/debian-installer/amd64/linux",
			checksumURL: "https://deb.debian.org/debian/dists/trixie/main/installer-amd64/current/images/SHA256SUMS",
			expected:    "netboot/debian-installer/amd64/linux",
		},
		{
			name:        "subdirectory initrd",
			fileURL:     "https://deb.debian.org/debian/dists/bookworm/main/installer-amd64/current/images/netboot/debian-installer/amd64/initrd.gz",
			checksumURL: "https://deb.debian.org/debian/dists/bookworm/main/installer-amd64/current/images/SHA256SUMS",
			expected:    "netboot/debian-installer/amd64/initrd.gz",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := checksumKey(tt.fileURL, tt.checksumURL)
			if key != tt.expected {
				t.Errorf("checksumKey(%q, %q) = %q, want %q",
					tt.fileURL, tt.checksumURL, key, tt.expected)
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

func TestReconcileBootMedia_InitializePending(t *testing.T) {
	fake := newFakeK8sClient()
	fake.bootMedias["test-bm"] = &k8s.BootMedia{
		Name: "test-bm",
		Files: []k8s.BootMediaFile{
			{URL: "http://example.com/images/linux"},
		},
	}

	ctrl := &Controller{k8sClient: fake, httpClient: http.DefaultClient}
	bm := fake.bootMedias["test-bm"]
	ctrl.reconcileBootMedia(context.Background(), bm)

	status, ok := fake.getBootMediaStatus("test-bm")
	if !ok {
		t.Fatal("expected BootMedia status to be updated")
	}
	if status.Phase != "Pending" {
		t.Errorf("expected phase Pending, got %q", status.Phase)
	}
	if status.Message != "Waiting for download" {
		t.Errorf("expected message 'Waiting for download', got %q", status.Message)
	}
}

func TestReconcileBootMedia_CompleteIsNoop(t *testing.T) {
	fake := newFakeK8sClient()
	fake.bootMedias["test-bm"] = &k8s.BootMedia{
		Name:   "test-bm",
		Status: k8s.BootMediaStatus{Phase: "Complete"},
	}

	ctrl := &Controller{k8sClient: fake, httpClient: http.DefaultClient}
	bm, _ := fake.GetBootMedia(context.Background(), "test-bm")
	ctrl.reconcileBootMedia(context.Background(), bm)

	if _, ok := fake.getBootMediaStatus("test-bm"); ok {
		t.Error("expected no status update for Complete BootMedia")
	}
}

func TestReconcileBootMedia_FailedIsNoop(t *testing.T) {
	fake := newFakeK8sClient()
	fake.bootMedias["test-bm"] = &k8s.BootMedia{
		Name:   "test-bm",
		Status: k8s.BootMediaStatus{Phase: "Failed"},
	}

	ctrl := &Controller{k8sClient: fake, httpClient: http.DefaultClient}
	bm, _ := fake.GetBootMedia(context.Background(), "test-bm")
	ctrl.reconcileBootMedia(context.Background(), bm)

	if _, ok := fake.getBootMediaStatus("test-bm"); ok {
		t.Error("expected no status update for Failed BootMedia")
	}
}

func TestDownloadBootMedia_NoFilesBasePath(t *testing.T) {
	fake := newFakeK8sClient()
	fake.bootMedias["test-bm"] = &k8s.BootMedia{
		Name: "test-bm",
		Files: []k8s.BootMediaFile{
			{URL: "http://example.com/images/linux"},
		},
	}

	ctrl := &Controller{
		k8sClient:   fake,
		httpClient:  http.DefaultClient,
		filesBasePath: "", // not configured
	}

	bm := fake.bootMedias["test-bm"]
	ctrl.downloadBootMedia(context.Background(), bm)

	status, ok := fake.getBootMediaStatus("test-bm")
	if !ok {
		t.Fatal("expected BootMedia status to be updated")
	}
	if status.Phase != "Failed" {
		t.Errorf("expected phase Failed, got %q", status.Phase)
	}
	if !strings.Contains(status.Message, "filesBasePath not configured") {
		t.Errorf("expected message about filesBasePath, got %q", status.Message)
	}
}

func TestDownloadBootMedia_InvalidURL(t *testing.T) {
	fake := newFakeK8sClient()
	fake.bootMedias["test-bm"] = &k8s.BootMedia{
		Name: "test-bm",
		Files: []k8s.BootMediaFile{
			{URL: "http://example.com/"}, // no filename
		},
	}

	tmpDir, err := os.MkdirTemp("", "bootmedia-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	ctrl := &Controller{
		k8sClient:     fake,
		httpClient:    http.DefaultClient,
		filesBasePath: tmpDir,
	}

	bm := fake.bootMedias["test-bm"]
	ctrl.downloadBootMedia(context.Background(), bm)

	status, ok := fake.getBootMediaStatus("test-bm")
	if !ok {
		t.Fatal("expected BootMedia status to be updated")
	}
	if status.Phase != "Failed" {
		t.Errorf("expected phase Failed, got %q", status.Phase)
	}
	if !strings.Contains(status.Message, "Invalid URL") {
		t.Errorf("expected message about invalid URL, got %q", status.Message)
	}
}

func TestDownloadBootMedia_SuccessfulDownload(t *testing.T) {
	fileContent := []byte("fake kernel content")
	sha256Hash := sha256.New()
	sha256Hash.Write(fileContent)
	expectedSha256 := fmt.Sprintf("%x", sha256Hash.Sum(nil))

	fake := newFakeK8sClient()
	fake.bootMedias["test-bm"] = &k8s.BootMedia{
		Name: "test-bm",
		Files: []k8s.BootMediaFile{
			{URL: "http://example.com/images/linux"},
		},
	}

	tmpDir, err := os.MkdirTemp("", "bootmedia-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	mockHTTP := &fakeHTTPDoer{
		doFunc: func(req *http.Request) (*http.Response, error) {
			if req.URL.Path == "/images/linux" {
				return httpResponseBytes(200, fileContent), nil
			}
			return httpResponse(404, "not found", -1), nil
		},
	}

	ctrl := &Controller{
		k8sClient:     fake,
		httpClient:    mockHTTP,
		filesBasePath: tmpDir,
	}

	bm := fake.bootMedias["test-bm"]
	ctrl.downloadBootMedia(context.Background(), bm)

	status, ok := fake.getBootMediaStatus("test-bm")
	if !ok {
		t.Fatal("expected BootMedia status to be updated")
	}
	if status.Phase != "Complete" {
		t.Fatalf("expected phase Complete, got %q (message: %s)", status.Phase, status.Message)
	}
	if len(status.Files) != 1 {
		t.Fatalf("expected 1 file status, got %d", len(status.Files))
	}
	if status.Files[0].SHA256 != expectedSha256 {
		t.Errorf("expected SHA256 %s, got %s", expectedSha256, status.Files[0].SHA256)
	}

	// Verify file was written to disk
	filePath := filepath.Join(tmpDir, "test-bm", "linux")
	content, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("failed to read downloaded file: %v", err)
	}
	if string(content) != string(fileContent) {
		t.Error("downloaded content doesn't match")
	}
}

func TestDownloadBootMedia_HTTPError(t *testing.T) {
	fake := newFakeK8sClient()
	fake.bootMedias["test-bm"] = &k8s.BootMedia{
		Name: "test-bm",
		Files: []k8s.BootMediaFile{
			{URL: "http://example.com/images/linux"},
		},
	}

	tmpDir, err := os.MkdirTemp("", "bootmedia-test")
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
		k8sClient:     fake,
		httpClient:    mockHTTP,
		filesBasePath: tmpDir,
	}

	bm := fake.bootMedias["test-bm"]
	ctrl.downloadBootMedia(context.Background(), bm)

	status, ok := fake.getBootMediaStatus("test-bm")
	if !ok {
		t.Fatal("expected BootMedia status to be updated")
	}
	if status.Phase != "Failed" {
		t.Errorf("expected phase Failed, got %q", status.Phase)
	}
	if !strings.Contains(status.Message, "Failed to download") {
		t.Errorf("expected message about download failure, got %q", status.Message)
	}
}

func TestDownloadBootMedia_ConnectionError(t *testing.T) {
	fake := newFakeK8sClient()
	fake.bootMedias["test-bm"] = &k8s.BootMedia{
		Name: "test-bm",
		Files: []k8s.BootMediaFile{
			{URL: "http://example.com/images/linux"},
		},
	}

	tmpDir, err := os.MkdirTemp("", "bootmedia-test")
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
		k8sClient:     fake,
		httpClient:    mockHTTP,
		filesBasePath: tmpDir,
	}

	bm := fake.bootMedias["test-bm"]
	ctrl.downloadBootMedia(context.Background(), bm)

	status, ok := fake.getBootMediaStatus("test-bm")
	if !ok {
		t.Fatal("expected BootMedia status to be updated")
	}
	if status.Phase != "Failed" {
		t.Errorf("expected phase Failed, got %q", status.Phase)
	}
	if !strings.Contains(status.Message, "Failed to download") {
		t.Errorf("expected message about download failure, got %q", status.Message)
	}
}

func TestDownloadBootMedia_CombinedFile(t *testing.T) {
	kernelContent := []byte("kernel-data")
	initrdContent := []byte("initrd-data")

	fake := newFakeK8sClient()
	fake.bootMedias["test-bm"] = &k8s.BootMedia{
		Name: "test-bm",
		Files: []k8s.BootMediaFile{
			{URL: "http://example.com/linux"},
			{URL: "http://example.com/initrd.gz"},
		},
		CombinedFiles: []k8s.CombinedFile{
			{Name: "combined-initrd.gz", Sources: []string{"initrd.gz", "linux"}},
		},
	}

	tmpDir, err := os.MkdirTemp("", "bootmedia-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	mockHTTP := &fakeHTTPDoer{
		doFunc: func(req *http.Request) (*http.Response, error) {
			switch req.URL.Path {
			case "/linux":
				return httpResponseBytes(200, kernelContent), nil
			case "/initrd.gz":
				return httpResponseBytes(200, initrdContent), nil
			default:
				return httpResponse(404, "not found", -1), nil
			}
		},
	}

	ctrl := &Controller{
		k8sClient:     fake,
		httpClient:    mockHTTP,
		filesBasePath: tmpDir,
	}

	bm := fake.bootMedias["test-bm"]
	ctrl.downloadBootMedia(context.Background(), bm)

	status, ok := fake.getBootMediaStatus("test-bm")
	if !ok {
		t.Fatal("expected BootMedia status to be updated")
	}
	if status.Phase != "Complete" {
		t.Fatalf("expected phase Complete, got %q (message: %s)", status.Phase, status.Message)
	}
	if len(status.Files) != 2 {
		t.Fatalf("expected 2 file statuses, got %d", len(status.Files))
	}
	if len(status.CombinedFiles) != 1 {
		t.Fatalf("expected 1 combined file status, got %d", len(status.CombinedFiles))
	}
	if status.CombinedFiles[0].Phase != "Complete" {
		t.Errorf("expected combined file phase Complete, got %q", status.CombinedFiles[0].Phase)
	}

	// Verify combined file content
	combinedPath := filepath.Join(tmpDir, "test-bm", "combined-initrd.gz")
	combinedContent, err := os.ReadFile(combinedPath)
	if err != nil {
		t.Fatalf("failed to read combined file: %v", err)
	}
	expectedCombined := string(initrdContent) + string(kernelContent)
	if string(combinedContent) != expectedCombined {
		t.Errorf("combined content mismatch: got %q, want %q", string(combinedContent), expectedCombined)
	}
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

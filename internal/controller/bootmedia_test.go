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
		Name:   "test-bm",
		Kernel: &k8s.BootMediaFileRef{URL: "http://example.com/images/linux"},
		Initrd: &k8s.BootMediaFileRef{URL: "http://example.com/images/initrd.gz"},
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
		Name:   "test-bm",
		Kernel: &k8s.BootMediaFileRef{URL: "http://example.com/images/linux"},
		Initrd: &k8s.BootMediaFileRef{URL: "http://example.com/images/initrd.gz"},
	}

	ctrl := &Controller{
		k8sClient:     fake,
		httpClient:    http.DefaultClient,
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

func TestDownloadBootMedia_ValidationError(t *testing.T) {
	fake := newFakeK8sClient()
	fake.bootMedias["test-bm"] = &k8s.BootMedia{
		Name:   "test-bm",
		Kernel: &k8s.BootMediaFileRef{URL: "http://example.com/linux"},
		// Missing Initrd - should fail validation
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
	if !strings.Contains(status.Message, "Invalid spec") {
		t.Errorf("expected message about invalid spec, got %q", status.Message)
	}
}

func TestDownloadBootMedia_DirectNoFirmware(t *testing.T) {
	kernelContent := []byte("fake kernel content")
	initrdContent := []byte("fake initrd content")
	kernelHash := sha256.New()
	kernelHash.Write(kernelContent)
	expectedKernelSha := fmt.Sprintf("%x", kernelHash.Sum(nil))

	fake := newFakeK8sClient()
	fake.bootMedias["test-bm"] = &k8s.BootMedia{
		Name:   "test-bm",
		Kernel: &k8s.BootMediaFileRef{URL: "http://example.com/images/linux"},
		Initrd: &k8s.BootMediaFileRef{URL: "http://example.com/images/initrd.gz"},
	}

	tmpDir, err := os.MkdirTemp("", "bootmedia-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	mockHTTP := &fakeHTTPDoer{
		doFunc: func(req *http.Request) (*http.Response, error) {
			switch req.URL.Path {
			case "/images/linux":
				return httpResponseBytes(200, kernelContent), nil
			case "/images/initrd.gz":
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
	if status.Kernel == nil || status.Kernel.SHA256 != expectedKernelSha {
		t.Errorf("expected kernel SHA256 %s, got %+v", expectedKernelSha, status.Kernel)
	}
	if status.Initrd == nil || status.Initrd.Phase != "Complete" {
		t.Errorf("expected initrd Complete, got %+v", status.Initrd)
	}

	// Verify files written to disk (no firmware = flat layout)
	kernelPath := filepath.Join(tmpDir, "test-bm", "linux")
	content, err := os.ReadFile(kernelPath)
	if err != nil {
		t.Fatalf("failed to read kernel file: %v", err)
	}
	if string(content) != string(kernelContent) {
		t.Error("kernel content doesn't match")
	}

	initrdPath := filepath.Join(tmpDir, "test-bm", "initrd.gz")
	content, err = os.ReadFile(initrdPath)
	if err != nil {
		t.Fatalf("failed to read initrd file: %v", err)
	}
	if string(content) != string(initrdContent) {
		t.Error("initrd content doesn't match")
	}
}

func TestDownloadBootMedia_DirectWithFirmware(t *testing.T) {
	kernelContent := []byte("kernel-data")
	initrdContent := []byte("initrd-data")
	firmwareContent := []byte("firmware-data")

	fake := newFakeK8sClient()
	fake.bootMedias["test-bm"] = &k8s.BootMedia{
		Name:     "test-bm",
		Kernel:   &k8s.BootMediaFileRef{URL: "http://example.com/linux"},
		Initrd:   &k8s.BootMediaFileRef{URL: "http://example.com/initrd.gz"},
		Firmware: &k8s.BootMediaFileRef{URL: "http://example.com/firmware.cpio.gz"},
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
			case "/firmware.cpio.gz":
				return httpResponseBytes(200, firmwareContent), nil
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

	// Verify directory layout with firmware
	// Kernel at top level
	kernelPath := filepath.Join(tmpDir, "test-bm", "linux")
	if _, err := os.Stat(kernelPath); err != nil {
		t.Fatalf("kernel not found at %s: %v", kernelPath, err)
	}

	// Initrd in no-firmware subdir
	noFwInitrd := filepath.Join(tmpDir, "test-bm", "no-firmware", "initrd.gz")
	content, err := os.ReadFile(noFwInitrd)
	if err != nil {
		t.Fatalf("no-firmware initrd not found: %v", err)
	}
	if string(content) != string(initrdContent) {
		t.Error("no-firmware initrd content mismatch")
	}

	// Firmware-combined initrd in with-firmware subdir
	withFwInitrd := filepath.Join(tmpDir, "test-bm", "with-firmware", "initrd.gz")
	content, err = os.ReadFile(withFwInitrd)
	if err != nil {
		t.Fatalf("with-firmware initrd not found: %v", err)
	}
	expectedCombined := string(initrdContent) + string(firmwareContent)
	if string(content) != expectedCombined {
		t.Errorf("with-firmware initrd content mismatch: got %q, want %q", string(content), expectedCombined)
	}

	// Check status fields
	if status.Firmware == nil || status.Firmware.Phase != "Complete" {
		t.Errorf("expected firmware Complete, got %+v", status.Firmware)
	}
	if status.FirmwareInitrd == nil || status.FirmwareInitrd.Phase != "Complete" {
		t.Errorf("expected firmwareInitrd Complete, got %+v", status.FirmwareInitrd)
	}
}

func TestDownloadBootMedia_HTTPError(t *testing.T) {
	fake := newFakeK8sClient()
	fake.bootMedias["test-bm"] = &k8s.BootMedia{
		Name:   "test-bm",
		Kernel: &k8s.BootMediaFileRef{URL: "http://example.com/images/linux"},
		Initrd: &k8s.BootMediaFileRef{URL: "http://example.com/images/initrd.gz"},
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
		Name:   "test-bm",
		Kernel: &k8s.BootMediaFileRef{URL: "http://example.com/images/linux"},
		Initrd: &k8s.BootMediaFileRef{URL: "http://example.com/images/initrd.gz"},
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

func TestWriteFileAtomic(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "bootmedia-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	data := []byte("test content")
	destPath := filepath.Join(tmpDir, "subdir", "file.txt")

	sha, err := writeFileAtomic(destPath, data)
	if err != nil {
		t.Fatalf("writeFileAtomic failed: %v", err)
	}

	// Verify file content
	content, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("failed to read file: %v", err)
	}
	if string(content) != string(data) {
		t.Error("content mismatch")
	}

	// Verify SHA256
	h := sha256.Sum256(data)
	expectedSha := fmt.Sprintf("%x", h[:])
	if sha != expectedSha {
		t.Errorf("SHA256 = %q, want %q", sha, expectedSha)
	}
}

func TestConcatenateFiles(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "bootmedia-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create source files
	src1 := filepath.Join(tmpDir, "file1")
	src2 := filepath.Join(tmpDir, "file2")
	os.WriteFile(src1, []byte("hello"), 0o644)
	os.WriteFile(src2, []byte("world"), 0o644)

	destPath := filepath.Join(tmpDir, "output", "combined")
	sha, err := concatenateFiles(destPath, src1, src2)
	if err != nil {
		t.Fatalf("concatenateFiles failed: %v", err)
	}

	// Verify content
	content, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("failed to read file: %v", err)
	}
	if string(content) != "helloworld" {
		t.Errorf("content = %q, want %q", string(content), "helloworld")
	}

	// Verify SHA256
	h := sha256.Sum256([]byte("helloworld"))
	expectedSha := fmt.Sprintf("%x", h[:])
	if sha != expectedSha {
		t.Errorf("SHA256 = %q, want %q", sha, expectedSha)
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

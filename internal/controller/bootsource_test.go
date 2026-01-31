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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestReconcileBootSource_InitializePending(t *testing.T) {
	bm := &k8s.BootSource{
		ObjectMeta: metav1.ObjectMeta{Name: "test-bm", Namespace: "default"},
		Spec: k8s.BootSourceSpec{
			Kernel: &k8s.BootSourceFileRef{URL: "http://example.com/vmlinuz"},
			Initrd: &k8s.BootSourceFileRef{URL: "http://example.com/initrd.gz"},
		},
	}
	k := newTestK8sClient(bm)

	ctrl := &Controller{k8sClient: k, httpClient: http.DefaultClient}
	ctrl.reconcileBootSource(context.Background(), bm)

	// After reconcile, status should be updated to Pending
	var updated k8s.BootSource
	if err := k.Get(context.Background(), k.Key("test-bm"), &updated); err != nil {
		t.Fatalf("failed to get BootSource: %v", err)
	}
	if updated.Status.Phase != "Pending" {
		t.Errorf("expected phase Pending, got %q", updated.Status.Phase)
	}
	if updated.Status.Message != "Waiting for download" {
		t.Errorf("expected message 'Waiting for download', got %q", updated.Status.Message)
	}
}

func TestReconcileBootSource_CompleteIsNoop(t *testing.T) {
	bm := &k8s.BootSource{
		ObjectMeta: metav1.ObjectMeta{Name: "test-bm", Namespace: "default"},
		Spec: k8s.BootSourceSpec{
			Kernel: &k8s.BootSourceFileRef{URL: "http://example.com/vmlinuz"},
			Initrd: &k8s.BootSourceFileRef{URL: "http://example.com/initrd.gz"},
		},
		Status: k8s.BootSourceStatus{Phase: "Complete", Message: "All files downloaded"},
	}
	k := newTestK8sClient(bm)

	ctrl := &Controller{k8sClient: k, httpClient: http.DefaultClient}
	ctrl.reconcileBootSource(context.Background(), bm)

	// Status should remain Complete
	var updated k8s.BootSource
	if err := k.Get(context.Background(), k.Key("test-bm"), &updated); err != nil {
		t.Fatalf("failed to get BootSource: %v", err)
	}
	if updated.Status.Phase != "Complete" {
		t.Errorf("expected phase to remain Complete, got %q", updated.Status.Phase)
	}
}

func TestReconcileBootSource_FailedIsNoop(t *testing.T) {
	bm := &k8s.BootSource{
		ObjectMeta: metav1.ObjectMeta{Name: "test-bm", Namespace: "default"},
		Spec: k8s.BootSourceSpec{
			Kernel: &k8s.BootSourceFileRef{URL: "http://example.com/vmlinuz"},
			Initrd: &k8s.BootSourceFileRef{URL: "http://example.com/initrd.gz"},
		},
		Status: k8s.BootSourceStatus{Phase: "Failed", Message: "Download error"},
	}
	k := newTestK8sClient(bm)

	ctrl := &Controller{k8sClient: k, httpClient: http.DefaultClient}
	ctrl.reconcileBootSource(context.Background(), bm)

	// Status should remain Failed
	var updated k8s.BootSource
	if err := k.Get(context.Background(), k.Key("test-bm"), &updated); err != nil {
		t.Fatalf("failed to get BootSource: %v", err)
	}
	if updated.Status.Phase != "Failed" {
		t.Errorf("expected phase to remain Failed, got %q", updated.Status.Phase)
	}
}

func TestDownloadBootSource_NoFilesBasePath(t *testing.T) {
	bm := &k8s.BootSource{
		ObjectMeta: metav1.ObjectMeta{Name: "test-bm", Namespace: "default"},
		Spec: k8s.BootSourceSpec{
			Kernel: &k8s.BootSourceFileRef{URL: "http://example.com/vmlinuz"},
			Initrd: &k8s.BootSourceFileRef{URL: "http://example.com/initrd.gz"},
		},
	}
	k := newTestK8sClient(bm)

	ctrl := &Controller{
		k8sClient:      k,
		httpClient:    http.DefaultClient,
		filesBasePath: "", // not configured
	}

	ctrl.downloadBootSource(context.Background(), bm)

	var updated k8s.BootSource
	if err := k.Get(context.Background(), k.Key("test-bm"), &updated); err != nil {
		t.Fatalf("failed to get BootSource: %v", err)
	}
	if updated.Status.Phase != "Failed" {
		t.Errorf("expected phase Failed, got %q", updated.Status.Phase)
	}
	if !strings.Contains(updated.Status.Message, "filesBasePath not configured") {
		t.Errorf("expected message about filesBasePath, got %q", updated.Status.Message)
	}
}

func TestDownloadBootSource_InvalidSpec_NoKernelOrISO(t *testing.T) {
	bm := &k8s.BootSource{
		ObjectMeta: metav1.ObjectMeta{Name: "test-bm", Namespace: "default"},
		Spec:       k8s.BootSourceSpec{}, // empty spec
	}
	k := newTestK8sClient(bm)

	tmpDir, err := os.MkdirTemp("", "bootsource-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	ctrl := &Controller{
		k8sClient:      k,
		httpClient:    http.DefaultClient,
		filesBasePath: tmpDir,
	}

	ctrl.downloadBootSource(context.Background(), bm)

	var updated k8s.BootSource
	if err := k.Get(context.Background(), k.Key("test-bm"), &updated); err != nil {
		t.Fatalf("failed to get BootSource: %v", err)
	}
	if updated.Status.Phase != "Failed" {
		t.Errorf("expected phase Failed, got %q", updated.Status.Phase)
	}
	if !strings.Contains(updated.Status.Message, "Invalid spec") {
		t.Errorf("expected message about invalid spec, got %q", updated.Status.Message)
	}
}

func TestDownloadBootSource_InvalidSpec_BothDirectAndISO(t *testing.T) {
	bm := &k8s.BootSource{
		ObjectMeta: metav1.ObjectMeta{Name: "test-bm", Namespace: "default"},
		Spec: k8s.BootSourceSpec{
			Kernel: &k8s.BootSourceFileRef{URL: "http://example.com/vmlinuz"},
			Initrd: &k8s.BootSourceFileRef{URL: "http://example.com/initrd.gz"},
			ISO: &k8s.BootSourceISO{
				URL:    "http://example.com/test.iso",
				Kernel: "/boot/vmlinuz",
				Initrd: "/boot/initrd.gz",
			},
		},
	}
	k := newTestK8sClient(bm)

	tmpDir, err := os.MkdirTemp("", "bootsource-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	ctrl := &Controller{
		k8sClient:      k,
		httpClient:    http.DefaultClient,
		filesBasePath: tmpDir,
	}

	ctrl.downloadBootSource(context.Background(), bm)

	var updated k8s.BootSource
	if err := k.Get(context.Background(), k.Key("test-bm"), &updated); err != nil {
		t.Fatalf("failed to get BootSource: %v", err)
	}
	if updated.Status.Phase != "Failed" {
		t.Errorf("expected phase Failed, got %q", updated.Status.Phase)
	}
	if !strings.Contains(updated.Status.Message, "Invalid spec") {
		t.Errorf("expected message about invalid spec, got %q", updated.Status.Message)
	}
}

func TestInitDownloadStatus_DirectMode(t *testing.T) {
	bm := &k8s.BootSource{
		ObjectMeta: metav1.ObjectMeta{Name: "test-bm", Namespace: "default"},
		Spec: k8s.BootSourceSpec{
			Kernel: &k8s.BootSourceFileRef{URL: "http://example.com/vmlinuz"},
			Initrd: &k8s.BootSourceFileRef{URL: "http://example.com/initrd.gz"},
		},
	}

	status := initDownloadStatus(bm)

	if status.Phase != "Downloading" {
		t.Errorf("expected phase Downloading, got %q", status.Phase)
	}
	if status.Message != "Starting downloads" {
		t.Errorf("expected message 'Starting downloads', got %q", status.Message)
	}
	if status.Kernel == nil {
		t.Fatal("expected Kernel status")
	}
	if status.Kernel.Name != "vmlinuz" {
		t.Errorf("expected Kernel name vmlinuz, got %q", status.Kernel.Name)
	}
	if status.Kernel.Phase != "Pending" {
		t.Errorf("expected Kernel phase Pending, got %q", status.Kernel.Phase)
	}
	if status.Initrd == nil {
		t.Fatal("expected Initrd status")
	}
	if status.Initrd.Name != "initrd.gz" {
		t.Errorf("expected Initrd name initrd.gz, got %q", status.Initrd.Name)
	}
	if status.Initrd.Phase != "Pending" {
		t.Errorf("expected Initrd phase Pending, got %q", status.Initrd.Phase)
	}
	if status.ISO != nil {
		t.Error("expected no ISO status in direct mode")
	}
	if status.Firmware != nil {
		t.Error("expected no Firmware status when firmware not specified")
	}
}

func TestInitDownloadStatus_DirectModeWithFirmware(t *testing.T) {
	bm := &k8s.BootSource{
		ObjectMeta: metav1.ObjectMeta{Name: "test-bm", Namespace: "default"},
		Spec: k8s.BootSourceSpec{
			Kernel:   &k8s.BootSourceFileRef{URL: "http://example.com/vmlinuz"},
			Initrd:   &k8s.BootSourceFileRef{URL: "http://example.com/initrd.gz"},
			Firmware: &k8s.BootSourceFileRef{URL: "http://example.com/firmware.cpio.gz"},
		},
	}

	status := initDownloadStatus(bm)

	if status.Firmware == nil {
		t.Fatal("expected Firmware status")
	}
	if status.Firmware.Name != "firmware.cpio.gz" {
		t.Errorf("expected Firmware name firmware.cpio.gz, got %q", status.Firmware.Name)
	}
	if status.Firmware.Phase != "Pending" {
		t.Errorf("expected Firmware phase Pending, got %q", status.Firmware.Phase)
	}
	if status.FirmwareInitrd == nil {
		t.Fatal("expected FirmwareInitrd status")
	}
	if status.FirmwareInitrd.Phase != "Pending" {
		t.Errorf("expected FirmwareInitrd phase Pending, got %q", status.FirmwareInitrd.Phase)
	}
}

func TestInitDownloadStatus_ISOMode(t *testing.T) {
	bm := &k8s.BootSource{
		ObjectMeta: metav1.ObjectMeta{Name: "test-bm", Namespace: "default"},
		Spec: k8s.BootSourceSpec{
			ISO: &k8s.BootSourceISO{
				URL:    "http://example.com/debian.iso",
				Kernel: "/install.amd/vmlinuz",
				Initrd: "/install.amd/initrd.gz",
			},
		},
	}

	status := initDownloadStatus(bm)

	if status.ISO == nil {
		t.Fatal("expected ISO status")
	}
	if status.ISO.Name != "debian.iso" {
		t.Errorf("expected ISO name debian.iso, got %q", status.ISO.Name)
	}
	if status.Kernel == nil {
		t.Fatal("expected Kernel status")
	}
	if status.Kernel.Name != "vmlinuz" {
		t.Errorf("expected Kernel name vmlinuz, got %q", status.Kernel.Name)
	}
	if status.Initrd == nil {
		t.Fatal("expected Initrd status")
	}
	if status.Initrd.Name != "initrd.gz" {
		t.Errorf("expected Initrd name initrd.gz, got %q", status.Initrd.Name)
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
			fileURL:     "http://example.com/images/vmlinuz",
			checksumURL: "http://example.com/images/SHA256SUMS",
			expected:    "vmlinuz",
		},
		{
			name:        "subdirectory",
			fileURL:     "http://example.com/images/netboot/amd64/linux",
			checksumURL: "http://example.com/images/SHA256SUMS",
			expected:    "netboot/amd64/linux",
		},
		{
			name:        "different directory structure",
			fileURL:     "http://example.com/other/vmlinuz",
			checksumURL: "http://example.com/images/SHA256SUMS",
			expected:    "vmlinuz", // falls back to basename
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := checksumKey(tt.fileURL, tt.checksumURL)
			if result != tt.expected {
				t.Errorf("checksumKey(%q, %q) = %q, want %q", tt.fileURL, tt.checksumURL, result, tt.expected)
			}
		})
	}
}

func TestComputeSHA256(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "sha256-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	content := []byte("test content for sha256")
	testFile := filepath.Join(tmpDir, "test.bin")
	if err := os.WriteFile(testFile, content, 0o644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	// Compute expected hash
	h := sha256.Sum256(content)
	expected := fmt.Sprintf("%x", h[:])

	got, err := computeSHA256(testFile)
	if err != nil {
		t.Fatalf("computeSHA256 failed: %v", err)
	}
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

func TestComputeSHA256_NonexistentFile(t *testing.T) {
	_, err := computeSHA256("/nonexistent/file.bin")
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestTruncHash(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"abcdef1234567890", "abcdef12..."},
		{"abcdefgh", "abcdefgh"},
		{"short", "short"},
		{"12345678", "12345678"},
		{"123456789", "12345678..."},
	}

	for _, tt := range tests {
		got := truncHash(tt.input)
		if got != tt.expected {
			t.Errorf("truncHash(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestWriteFileAtomic(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "writefile-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	data := []byte("test atomic write content")
	destPath := filepath.Join(tmpDir, "subdir", "output.bin")

	sha, err := writeFileAtomic(destPath, data)
	if err != nil {
		t.Fatalf("writeFileAtomic failed: %v", err)
	}

	// Verify file exists
	got, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("failed to read output file: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Error("file content doesn't match")
	}

	// Verify SHA256
	h := sha256.Sum256(data)
	expectedSHA := fmt.Sprintf("%x", h[:])
	if sha != expectedSHA {
		t.Errorf("expected sha %q, got %q", expectedSHA, sha)
	}

	// Verify temp file was cleaned up
	tmpPath := destPath + ".tmp"
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Error("expected temp file to be removed after atomic write")
	}
}

func TestConcatenateFiles(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "concat-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create source files
	src1 := filepath.Join(tmpDir, "file1.bin")
	src2 := filepath.Join(tmpDir, "file2.bin")
	if err := os.WriteFile(src1, []byte("hello "), 0o644); err != nil {
		t.Fatalf("failed to write source 1: %v", err)
	}
	if err := os.WriteFile(src2, []byte("world"), 0o644); err != nil {
		t.Fatalf("failed to write source 2: %v", err)
	}

	destPath := filepath.Join(tmpDir, "output", "combined.bin")
	sha, err := concatenateFiles(destPath, src1, src2)
	if err != nil {
		t.Fatalf("concatenateFiles failed: %v", err)
	}

	// Verify content
	got, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("failed to read output file: %v", err)
	}
	if string(got) != "hello world" {
		t.Errorf("expected 'hello world', got %q", string(got))
	}

	// Verify SHA256
	h := sha256.Sum256([]byte("hello world"))
	expectedSHA := fmt.Sprintf("%x", h[:])
	if sha != expectedSHA {
		t.Errorf("expected sha %q, got %q", expectedSHA, sha)
	}
}

func TestConcatenateFiles_MissingSource(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "concat-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	destPath := filepath.Join(tmpDir, "output.bin")
	_, err = concatenateFiles(destPath, filepath.Join(tmpDir, "nonexistent.bin"))
	if err == nil {
		t.Error("expected error for missing source file")
	}
}

func TestDownloadFile_SuccessfulDownload(t *testing.T) {
	content := []byte("test download content")

	mockHTTP := &fakeHTTPDoer{
		doFunc: func(req *http.Request) (*http.Response, error) {
			switch req.URL.Path {
			case "/vmlinuz":
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(bytes.NewReader(content)),
					Header:     make(http.Header),
				}, nil
			default:
				return &http.Response{
					StatusCode: http.StatusNotFound,
					Body:       io.NopCloser(strings.NewReader("not found")),
					Header:     make(http.Header),
				}, nil
			}
		},
	}

	tmpDir, err := os.MkdirTemp("", "download-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	ctrl := &Controller{httpClient: mockHTTP}
	destPath := filepath.Join(tmpDir, "vmlinuz")

	sha, err := ctrl.downloadFile(context.Background(), "http://example.com/vmlinuz", "", destPath)
	if err != nil {
		t.Fatalf("downloadFile failed: %v", err)
	}

	// Verify file content
	got, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("failed to read downloaded file: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Error("downloaded content doesn't match")
	}

	// Verify SHA
	h := sha256.Sum256(content)
	expectedSHA := fmt.Sprintf("%x", h[:])
	if sha != expectedSHA {
		t.Errorf("expected sha %q, got %q", expectedSHA, sha)
	}
}

func TestDownloadFile_HTTPError(t *testing.T) {
	mockHTTP := &fakeHTTPDoer{
		doFunc: func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusInternalServerError,
				Body:       io.NopCloser(strings.NewReader("server error")),
				Header:     make(http.Header),
			}, nil
		},
	}

	tmpDir, err := os.MkdirTemp("", "download-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	ctrl := &Controller{httpClient: mockHTTP}
	destPath := filepath.Join(tmpDir, "vmlinuz")

	_, err = ctrl.downloadFile(context.Background(), "http://example.com/vmlinuz", "", destPath)
	if err == nil {
		t.Error("expected error for HTTP 500")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("expected error to mention 500, got: %v", err)
	}
}

func TestDownloadFile_ConnectionError(t *testing.T) {
	mockHTTP := &fakeHTTPDoer{
		doFunc: func(req *http.Request) (*http.Response, error) {
			return nil, fmt.Errorf("connection refused")
		},
	}

	tmpDir, err := os.MkdirTemp("", "download-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	ctrl := &Controller{httpClient: mockHTTP}
	destPath := filepath.Join(tmpDir, "vmlinuz")

	_, err = ctrl.downloadFile(context.Background(), "http://example.com/vmlinuz", "", destPath)
	if err == nil {
		t.Error("expected error for connection failure")
	}
}

func TestDownloadFile_ExistingFileSkipsDownload(t *testing.T) {
	content := []byte("existing file content")

	// Compute sha256 of existing content
	h := sha256.Sum256(content)
	existingSHA := fmt.Sprintf("%x", h[:])

	downloadCalled := false
	mockHTTP := &fakeHTTPDoer{
		doFunc: func(req *http.Request) (*http.Response, error) {
			if req.URL.Path == "/vmlinuz" {
				downloadCalled = true
			}
			return &http.Response{
				StatusCode: http.StatusNotFound,
				Body:       io.NopCloser(strings.NewReader("not found")),
				Header:     make(http.Header),
			}, nil
		},
	}

	tmpDir, err := os.MkdirTemp("", "download-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	destPath := filepath.Join(tmpDir, "vmlinuz")
	if err := os.WriteFile(destPath, content, 0o644); err != nil {
		t.Fatalf("failed to write existing file: %v", err)
	}

	ctrl := &Controller{httpClient: mockHTTP}

	// No checksum URL -> existing file is accepted
	sha, err := ctrl.downloadFile(context.Background(), "http://example.com/vmlinuz", "", destPath)
	if err != nil {
		t.Fatalf("downloadFile failed: %v", err)
	}
	if sha != existingSHA {
		t.Errorf("expected existing sha %q, got %q", existingSHA, sha)
	}
	if downloadCalled {
		t.Error("expected download to be skipped for existing file without checksum URL")
	}
}

func TestDownloadFile_ChecksumVerification(t *testing.T) {
	content := []byte("file content with checksum")
	h := sha256.Sum256(content)
	expectedSHA := fmt.Sprintf("%x", h[:])

	checksumBody := fmt.Sprintf("%s  vmlinuz\n", expectedSHA)

	mockHTTP := &fakeHTTPDoer{
		doFunc: func(req *http.Request) (*http.Response, error) {
			switch req.URL.Path {
			case "/boot/vmlinuz":
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(bytes.NewReader(content)),
					Header:     make(http.Header),
				}, nil
			case "/boot/SHA256SUMS":
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(checksumBody)),
					Header:     make(http.Header),
				}, nil
			default:
				return &http.Response{
					StatusCode: http.StatusNotFound,
					Body:       io.NopCloser(strings.NewReader("not found")),
					Header:     make(http.Header),
				}, nil
			}
		},
	}

	tmpDir, err := os.MkdirTemp("", "download-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	ctrl := &Controller{httpClient: mockHTTP}
	destPath := filepath.Join(tmpDir, "vmlinuz")

	sha, err := ctrl.downloadFile(context.Background(), "http://example.com/boot/vmlinuz", "http://example.com/boot/SHA256SUMS", destPath)
	if err != nil {
		t.Fatalf("downloadFile failed: %v", err)
	}
	if sha != expectedSHA {
		t.Errorf("expected sha %q, got %q", expectedSHA, sha)
	}
}

func TestDownloadFile_ChecksumMismatch(t *testing.T) {
	content := []byte("file content")
	wrongChecksum := "0000000000000000000000000000000000000000000000000000000000000000"
	checksumBody := fmt.Sprintf("%s  vmlinuz\n", wrongChecksum)

	mockHTTP := &fakeHTTPDoer{
		doFunc: func(req *http.Request) (*http.Response, error) {
			switch req.URL.Path {
			case "/boot/vmlinuz":
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(bytes.NewReader(content)),
					Header:     make(http.Header),
				}, nil
			case "/boot/SHA256SUMS":
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(checksumBody)),
					Header:     make(http.Header),
				}, nil
			default:
				return &http.Response{
					StatusCode: http.StatusNotFound,
					Body:       io.NopCloser(strings.NewReader("not found")),
					Header:     make(http.Header),
				}, nil
			}
		},
	}

	tmpDir, err := os.MkdirTemp("", "download-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	ctrl := &Controller{httpClient: mockHTTP}
	destPath := filepath.Join(tmpDir, "vmlinuz")

	_, err = ctrl.downloadFile(context.Background(), "http://example.com/boot/vmlinuz", "http://example.com/boot/SHA256SUMS", destPath)
	if err == nil {
		t.Error("expected error for checksum mismatch")
	}
	if !strings.Contains(err.Error(), "checksum mismatch") {
		t.Errorf("expected checksum mismatch error, got: %v", err)
	}
}

func TestDownloadBootSourceDirect_SuccessfulDownload(t *testing.T) {
	kernelContent := []byte("kernel content")
	initrdContent := []byte("initrd content")

	kernelSHA := sha256.Sum256(kernelContent)
	initrdSHA := sha256.Sum256(initrdContent)

	bm := &k8s.BootSource{
		ObjectMeta: metav1.ObjectMeta{Name: "test-bm", Namespace: "default"},
		Spec: k8s.BootSourceSpec{
			Kernel: &k8s.BootSourceFileRef{URL: "http://example.com/boot/vmlinuz"},
			Initrd: &k8s.BootSourceFileRef{URL: "http://example.com/boot/initrd.gz"},
		},
	}
	k := newTestK8sClient(bm)

	tmpDir, err := os.MkdirTemp("", "bootsource-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	mockHTTP := &fakeHTTPDoer{
		doFunc: func(req *http.Request) (*http.Response, error) {
			switch req.URL.Path {
			case "/boot/vmlinuz":
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(bytes.NewReader(kernelContent)),
					Header:     make(http.Header),
				}, nil
			case "/boot/initrd.gz":
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(bytes.NewReader(initrdContent)),
					Header:     make(http.Header),
				}, nil
			default:
				return &http.Response{
					StatusCode: http.StatusNotFound,
					Body:       io.NopCloser(strings.NewReader("not found")),
					Header:     make(http.Header),
				}, nil
			}
		},
	}

	ctrl := &Controller{
		k8sClient:      k,
		httpClient:    mockHTTP,
		filesBasePath: tmpDir,
	}

	status := initDownloadStatus(bm)
	ctrl.downloadBootSourceDirect(context.Background(), bm, status, filepath.Join(tmpDir, bm.Name), false)

	if status.Phase != "Complete" {
		t.Errorf("expected phase Complete, got %q (message: %s)", status.Phase, status.Message)
	}
	if status.Kernel.Phase != "Complete" {
		t.Errorf("expected Kernel phase Complete, got %q", status.Kernel.Phase)
	}
	if status.Kernel.SHA256 != fmt.Sprintf("%x", kernelSHA[:]) {
		t.Errorf("kernel SHA mismatch")
	}
	if status.Initrd.Phase != "Complete" {
		t.Errorf("expected Initrd phase Complete, got %q", status.Initrd.Phase)
	}
	if status.Initrd.SHA256 != fmt.Sprintf("%x", initrdSHA[:]) {
		t.Errorf("initrd SHA mismatch")
	}

	// Verify files exist on disk
	kernelPath := filepath.Join(tmpDir, bm.Name, "vmlinuz")
	if _, err := os.Stat(kernelPath); err != nil {
		t.Errorf("expected kernel file to exist: %v", err)
	}
	initrdPath := filepath.Join(tmpDir, bm.Name, "initrd.gz")
	if _, err := os.Stat(initrdPath); err != nil {
		t.Errorf("expected initrd file to exist: %v", err)
	}
}

func TestDownloadBootSourceDirect_KernelDownloadFails(t *testing.T) {
	bm := &k8s.BootSource{
		ObjectMeta: metav1.ObjectMeta{Name: "test-bm", Namespace: "default"},
		Spec: k8s.BootSourceSpec{
			Kernel: &k8s.BootSourceFileRef{URL: "http://example.com/boot/vmlinuz"},
			Initrd: &k8s.BootSourceFileRef{URL: "http://example.com/boot/initrd.gz"},
		},
	}
	k := newTestK8sClient(bm)

	tmpDir, err := os.MkdirTemp("", "bootsource-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	mockHTTP := &fakeHTTPDoer{
		doFunc: func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusInternalServerError,
				Body:       io.NopCloser(strings.NewReader("server error")),
				Header:     make(http.Header),
			}, nil
		},
	}

	ctrl := &Controller{
		k8sClient:      k,
		httpClient:    mockHTTP,
		filesBasePath: tmpDir,
	}

	status := initDownloadStatus(bm)
	ctrl.downloadBootSourceDirect(context.Background(), bm, status, filepath.Join(tmpDir, bm.Name), false)

	if status.Phase != "Failed" {
		t.Errorf("expected phase Failed, got %q", status.Phase)
	}
	if !strings.Contains(status.Message, "kernel") {
		t.Errorf("expected message about kernel failure, got %q", status.Message)
	}
	if status.Kernel.Phase != "Failed" {
		t.Errorf("expected Kernel phase Failed, got %q", status.Kernel.Phase)
	}
}

func TestDownloadBootSourceDirect_InitrdDownloadFails(t *testing.T) {
	kernelContent := []byte("kernel content")

	bm := &k8s.BootSource{
		ObjectMeta: metav1.ObjectMeta{Name: "test-bm", Namespace: "default"},
		Spec: k8s.BootSourceSpec{
			Kernel: &k8s.BootSourceFileRef{URL: "http://example.com/boot/vmlinuz"},
			Initrd: &k8s.BootSourceFileRef{URL: "http://example.com/boot/initrd.gz"},
		},
	}
	k := newTestK8sClient(bm)

	tmpDir, err := os.MkdirTemp("", "bootsource-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	mockHTTP := &fakeHTTPDoer{
		doFunc: func(req *http.Request) (*http.Response, error) {
			switch req.URL.Path {
			case "/boot/vmlinuz":
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(bytes.NewReader(kernelContent)),
					Header:     make(http.Header),
				}, nil
			default:
				return &http.Response{
					StatusCode: http.StatusInternalServerError,
					Body:       io.NopCloser(strings.NewReader("server error")),
					Header:     make(http.Header),
				}, nil
			}
		},
	}

	ctrl := &Controller{
		k8sClient:      k,
		httpClient:    mockHTTP,
		filesBasePath: tmpDir,
	}

	status := initDownloadStatus(bm)
	ctrl.downloadBootSourceDirect(context.Background(), bm, status, filepath.Join(tmpDir, bm.Name), false)

	if status.Phase != "Failed" {
		t.Errorf("expected phase Failed, got %q", status.Phase)
	}
	if !strings.Contains(status.Message, "initrd") {
		t.Errorf("expected message about initrd failure, got %q", status.Message)
	}
	if status.Kernel.Phase != "Complete" {
		t.Errorf("expected Kernel phase Complete (downloaded before initrd failed), got %q", status.Kernel.Phase)
	}
	if status.Initrd.Phase != "Failed" {
		t.Errorf("expected Initrd phase Failed, got %q", status.Initrd.Phase)
	}
}

func TestDownloadBootSourceDirect_WithFirmwareSubdirectories(t *testing.T) {
	kernelContent := []byte("kernel content")
	initrdContent := []byte("initrd content")
	firmwareContent := []byte("firmware content")

	bm := &k8s.BootSource{
		ObjectMeta: metav1.ObjectMeta{Name: "test-bm", Namespace: "default"},
		Spec: k8s.BootSourceSpec{
			Kernel:   &k8s.BootSourceFileRef{URL: "http://example.com/boot/vmlinuz"},
			Initrd:   &k8s.BootSourceFileRef{URL: "http://example.com/boot/initrd.gz"},
			Firmware: &k8s.BootSourceFileRef{URL: "http://example.com/firmware/firmware.cpio.gz"},
		},
	}
	k := newTestK8sClient(bm)

	tmpDir, err := os.MkdirTemp("", "bootsource-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	mockHTTP := &fakeHTTPDoer{
		doFunc: func(req *http.Request) (*http.Response, error) {
			switch req.URL.Path {
			case "/boot/vmlinuz":
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(bytes.NewReader(kernelContent)),
					Header:     make(http.Header),
				}, nil
			case "/boot/initrd.gz":
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(bytes.NewReader(initrdContent)),
					Header:     make(http.Header),
				}, nil
			case "/firmware/firmware.cpio.gz":
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(bytes.NewReader(firmwareContent)),
					Header:     make(http.Header),
				}, nil
			default:
				return &http.Response{
					StatusCode: http.StatusNotFound,
					Body:       io.NopCloser(strings.NewReader("not found")),
					Header:     make(http.Header),
				}, nil
			}
		},
	}

	ctrl := &Controller{
		k8sClient:      k,
		httpClient:    mockHTTP,
		filesBasePath: tmpDir,
	}

	status := initDownloadStatus(bm)
	hasFirmware := bm.HasFirmware()
	ctrl.downloadBootSourceDirect(context.Background(), bm, status, filepath.Join(tmpDir, bm.Name), hasFirmware)

	if status.Phase != "Complete" {
		t.Errorf("expected phase Complete, got %q (message: %s)", status.Phase, status.Message)
	}

	// When firmware is present, initrd goes in no-firmware/ subdirectory
	noFwInitrdPath := filepath.Join(tmpDir, bm.Name, "no-firmware", "initrd.gz")
	if _, err := os.Stat(noFwInitrdPath); err != nil {
		t.Errorf("expected no-firmware/initrd.gz to exist: %v", err)
	}

	// Firmware concatenated initrd goes in with-firmware/ subdirectory
	withFwInitrdPath := filepath.Join(tmpDir, bm.Name, "with-firmware", "initrd.gz")
	if _, err := os.Stat(withFwInitrdPath); err != nil {
		t.Errorf("expected with-firmware/initrd.gz to exist: %v", err)
	}

	// Verify concatenated content is initrd + firmware
	combinedContent, err := os.ReadFile(withFwInitrdPath)
	if err != nil {
		t.Fatalf("failed to read with-firmware initrd: %v", err)
	}
	expectedCombined := append(initrdContent, firmwareContent...)
	if !bytes.Equal(combinedContent, expectedCombined) {
		t.Error("firmware-concatenated initrd content doesn't match expected")
	}
}

func TestFailBootSource(t *testing.T) {
	bm := &k8s.BootSource{
		ObjectMeta: metav1.ObjectMeta{Name: "test-bm", Namespace: "default"},
		Spec: k8s.BootSourceSpec{
			Kernel: &k8s.BootSourceFileRef{URL: "http://example.com/vmlinuz"},
			Initrd: &k8s.BootSourceFileRef{URL: "http://example.com/initrd.gz"},
		},
	}
	k := newTestK8sClient(bm)

	ctrl := &Controller{k8sClient: k}
	ctrl.failBootSource(context.Background(), "test-bm", "something went wrong")

	var updated k8s.BootSource
	if err := k.Get(context.Background(), k.Key("test-bm"), &updated); err != nil {
		t.Fatalf("failed to get BootSource: %v", err)
	}
	if updated.Status.Phase != "Failed" {
		t.Errorf("expected phase Failed, got %q", updated.Status.Phase)
	}
	if updated.Status.Message != "something went wrong" {
		t.Errorf("expected message 'something went wrong', got %q", updated.Status.Message)
	}
}

func TestFailBootSourceStatus(t *testing.T) {
	bm := &k8s.BootSource{
		ObjectMeta: metav1.ObjectMeta{Name: "test-bm", Namespace: "default"},
		Spec: k8s.BootSourceSpec{
			Kernel: &k8s.BootSourceFileRef{URL: "http://example.com/vmlinuz"},
			Initrd: &k8s.BootSourceFileRef{URL: "http://example.com/initrd.gz"},
		},
	}
	k := newTestK8sClient(bm)

	ctrl := &Controller{k8sClient: k}
	status := &k8s.BootSourceStatus{
		Phase:   "Downloading",
		Message: "In progress",
		Kernel:  &k8s.FileStatus{Name: "vmlinuz", Phase: "Downloading"},
	}

	ctrl.failBootSourceStatus(context.Background(), "test-bm", status, status.Kernel, "kernel download failed")

	if status.Phase != "Failed" {
		t.Errorf("expected overall phase Failed, got %q", status.Phase)
	}
	if status.Message != "kernel download failed" {
		t.Errorf("expected message 'kernel download failed', got %q", status.Message)
	}
	if status.Kernel.Phase != "Failed" {
		t.Errorf("expected Kernel phase Failed, got %q", status.Kernel.Phase)
	}
}

func TestReconcileBootSources_ListsAndReconciles(t *testing.T) {
	bm1 := &k8s.BootSource{
		ObjectMeta: metav1.ObjectMeta{Name: "bm-1", Namespace: "default"},
		Spec: k8s.BootSourceSpec{
			Kernel: &k8s.BootSourceFileRef{URL: "http://example.com/vmlinuz"},
			Initrd: &k8s.BootSourceFileRef{URL: "http://example.com/initrd.gz"},
		},
		// Empty status -> should get initialized to Pending
	}
	bm2 := &k8s.BootSource{
		ObjectMeta: metav1.ObjectMeta{Name: "bm-2", Namespace: "default"},
		Spec: k8s.BootSourceSpec{
			Kernel: &k8s.BootSourceFileRef{URL: "http://example.com/vmlinuz2"},
			Initrd: &k8s.BootSourceFileRef{URL: "http://example.com/initrd2.gz"},
		},
		Status: k8s.BootSourceStatus{Phase: "Complete", Message: "Done"},
	}
	k := newTestK8sClient(bm1, bm2)

	ctrl := &Controller{k8sClient: k, httpClient: http.DefaultClient}
	ctrl.reconcileBootSources(context.Background())

	// bm-1 should be initialized to Pending
	var updated1 k8s.BootSource
	if err := k.Get(context.Background(), k.Key("bm-1"), &updated1); err != nil {
		t.Fatalf("failed to get bm-1: %v", err)
	}
	if updated1.Status.Phase != "Pending" {
		t.Errorf("expected bm-1 phase Pending, got %q", updated1.Status.Phase)
	}

	// bm-2 should remain Complete
	var updated2 k8s.BootSource
	if err := k.Get(context.Background(), k.Key("bm-2"), &updated2); err != nil {
		t.Fatalf("failed to get bm-2: %v", err)
	}
	if updated2.Status.Phase != "Complete" {
		t.Errorf("expected bm-2 phase to remain Complete, got %q", updated2.Status.Phase)
	}
}

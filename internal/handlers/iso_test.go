package handlers

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/isoboot/isoboot/internal/controllerclient"
)

// mockISOClient implements ISOClient for testing.
type mockISOClient struct {
	getBootTarget func(ctx context.Context, name string) (*controllerclient.BootTargetInfo, error)
}

func (m *mockISOClient) GetBootTarget(ctx context.Context, name string) (*controllerclient.BootTargetInfo, error) {
	return m.getBootTarget(ctx, name)
}

func TestValidDiskImageRef(t *testing.T) {
	tests := []struct {
		name  string
		input string
		valid bool
	}{
		{"simple name", "debian-13", true},
		{"with version", "debian-13.1", true},
		{"with underscore", "my_image", true},
		{"alphanumeric", "image123", true},
		{"multiple dots", "debian-13.1.2", true},
		{"path traversal", "..", false},
		{"path traversal in name", "foo..bar", false},
		{"leading dot", ".hidden", false},
		{"trailing dot", "name.", false},
		{"forward slash", "foo/bar", false},
		{"backslash", "foo\\bar", false},
		{"empty", "", false},
		{"space", "foo bar", false},
		{"special char", "foo@bar", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := validDiskImageRef.MatchString(tt.input)
			if got != tt.valid {
				t.Errorf("validDiskImageRef.MatchString(%q) = %v, want %v", tt.input, got, tt.valid)
			}
		})
	}
}

func TestServeISOContent_InvalidPath(t *testing.T) {
	mock := &mockISOClient{}
	h := NewISOHandler("/tmp/iso", mock)

	req := httptest.NewRequest("GET", "/iso/content/only-two-parts", nil)
	w := httptest.NewRecorder()

	h.ServeISOContent(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestServeISOContent_BootTargetNotFound(t *testing.T) {
	mock := &mockISOClient{
		getBootTarget: func(ctx context.Context, name string) (*controllerclient.BootTargetInfo, error) {
			return nil, fmt.Errorf("boottarget %s: %w", name, controllerclient.ErrNotFound)
		},
	}
	h := NewISOHandler("/tmp/iso", mock)

	req := httptest.NewRequest("GET", "/iso/content/missing-target/mini.iso/linux", nil)
	w := httptest.NewRecorder()

	h.ServeISOContent(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestServeISOContent_InvalidDiskImageRef(t *testing.T) {
	mock := &mockISOClient{
		getBootTarget: func(ctx context.Context, name string) (*controllerclient.BootTargetInfo, error) {
			return &controllerclient.BootTargetInfo{
				DiskImage: "../etc/passwd",
			}, nil
		},
	}
	h := NewISOHandler("/tmp/iso", mock)

	req := httptest.NewRequest("GET", "/iso/content/bad-target/mini.iso/linux", nil)
	w := httptest.NewRecorder()

	h.ServeISOContent(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestServeISOContent_GRPCError(t *testing.T) {
	mock := &mockISOClient{
		getBootTarget: func(ctx context.Context, name string) (*controllerclient.BootTargetInfo, error) {
			return nil, fmt.Errorf("grpc call: connection refused")
		},
	}
	h := NewISOHandler("/tmp/iso", mock)

	req := httptest.NewRequest("GET", "/iso/content/target/mini.iso/linux", nil)
	w := httptest.NewRecorder()

	h.ServeISOContent(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", w.Code)
	}
}

func TestIsPrintableASCII(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"normal filename", "ubuntu-24.04.iso", true},
		{"with spaces", "my file.iso", true},
		{"tilde", "file~1.iso", true},
		{"empty string", "", false},
		{"contains newline", "file\n.iso", false},
		{"contains carriage return", "file\r.iso", false},
		{"contains null", "file\x00.iso", false},
		{"contains tab", "file\t.iso", false},
		{"contains DEL", "file\x7f.iso", false},
		{"non-ASCII unicode", "file\xc3\xa9.iso", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isPrintableASCII(tt.input)
			if got != tt.want {
				t.Errorf("isPrintableASCII(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestServeISODownload_InvalidPath(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		wantStatus int
	}{
		{"missing diskImageFile", "/iso/download/ubuntu-24", http.StatusBadRequest},
		{"missing bootTarget", "/iso/download/", http.StatusBadRequest},
		{"empty path", "/iso/download", http.StatusBadRequest},
		{"only slash", "/iso/download//", http.StatusBadRequest},
	}

	// Create handler with nil controller - these tests fail before gRPC calls
	h := &ISOHandler{basePath: "/tmp"}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", tt.path, nil)
			w := httptest.NewRecorder()
			h.ServeISODownload(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("path %q: got status %d, want %d", tt.path, w.Code, tt.wantStatus)
			}
		})
	}
}

func TestServeISODownload_DiskImageFileValidation(t *testing.T) {
	tests := []struct {
		name          string
		diskImageFile string
		wantStatus    int
	}{
		// Invalid diskImageFile - rejected before gRPC call
		{"dot only", ".", http.StatusBadRequest},
		{"contains slash", "foo/bar.iso", http.StatusBadRequest},
		{"contains backslash", "foo\\bar.iso", http.StatusBadRequest},
	}

	// Create handler with nil controller - these tests fail at diskImageFile validation
	h := &ISOHandler{basePath: "/tmp"}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Construct valid path format with the test diskImageFile
			path := "/iso/download/ubuntu-24/" + tt.diskImageFile
			req := httptest.NewRequest("GET", path, nil)
			w := httptest.NewRecorder()
			h.ServeISODownload(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("diskImageFile %q: got status %d, want %d", tt.diskImageFile, w.Code, tt.wantStatus)
			}
		})
	}
}

func TestServeISODownload_MethodNotAllowed(t *testing.T) {
	h := &ISOHandler{basePath: "/tmp"}

	req := httptest.NewRequest("POST", "/iso/download/ubuntu-24/ubuntu.iso", nil)
	w := httptest.NewRecorder()
	h.ServeISODownload(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
	if allow := w.Header().Get("Allow"); allow != "GET, HEAD" {
		t.Errorf("expected Allow: GET, HEAD, got %q", allow)
	}
}

func TestServeISODownload_BootTargetNotFound(t *testing.T) {
	mock := &mockISOClient{
		getBootTarget: func(ctx context.Context, name string) (*controllerclient.BootTargetInfo, error) {
			return nil, fmt.Errorf("boottarget %s: %w", name, controllerclient.ErrNotFound)
		},
	}
	h := NewISOHandler("/tmp/iso", mock)

	req := httptest.NewRequest("GET", "/iso/download/missing-target/ubuntu.iso", nil)
	w := httptest.NewRecorder()
	h.ServeISODownload(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestShouldMergeFirmware(t *testing.T) {
	tests := []struct {
		name                string
		requestedFile       string
		includeFirmwarePath string
		want                bool
	}{
		{
			name:                "exact match with leading slash",
			requestedFile:       "initrd.gz",
			includeFirmwarePath: "/initrd.gz",
			want:                true,
		},
		{
			name:                "exact match without leading slash in config",
			requestedFile:       "initrd.gz",
			includeFirmwarePath: "initrd.gz",
			want:                true,
		},
		{
			name:                "nested path match",
			requestedFile:       "boot/initrd.img",
			includeFirmwarePath: "/boot/initrd.img",
			want:                true,
		},
		{
			name:                "no match - different file",
			requestedFile:       "vmlinuz",
			includeFirmwarePath: "/initrd.gz",
			want:                false,
		},
		{
			name:                "no match - empty includeFirmwarePath disables merging",
			requestedFile:       "initrd.gz",
			includeFirmwarePath: "",
			want:                false,
		},
		{
			name:                "no match - partial path",
			requestedFile:       "initrd.gz.bak",
			includeFirmwarePath: "/initrd.gz",
			want:                false,
		},
		{
			name:                "no match - prefix only",
			requestedFile:       "initrd",
			includeFirmwarePath: "/initrd.gz",
			want:                false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldMergeFirmware(tt.requestedFile, tt.includeFirmwarePath)
			if got != tt.want {
				t.Errorf("shouldMergeFirmware(%q, %q) = %v, want %v",
					tt.requestedFile, tt.includeFirmwarePath, got, tt.want)
			}
		})
	}
}

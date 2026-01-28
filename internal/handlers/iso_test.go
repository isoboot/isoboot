package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

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

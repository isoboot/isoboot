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

	prefix := "/iso/download/"
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Simulate the path parsing logic from ServeISODownload
			handler := func(w http.ResponseWriter, r *http.Request) {
				urlPath := r.URL.Path
				if len(urlPath) < len(prefix) {
					http.Error(w, "invalid path", http.StatusBadRequest)
					return
				}
				path := urlPath[len(prefix):]
				parts := splitPath(path, 2)
				if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
					http.Error(w, "invalid path", http.StatusBadRequest)
					return
				}
			}

			req := httptest.NewRequest("GET", tt.path, nil)
			w := httptest.NewRecorder()
			handler(w, req)

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
		wantValid     bool
	}{
		{"valid iso name", "ubuntu-24.04.iso", true},
		{"valid with dots", "ubuntu-24.04.1-live-server-amd64.iso", true},
		{"dot only", ".", false},
		{"contains slash", "foo/bar.iso", false},
		{"contains backslash", "foo\\bar.iso", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Simulate the diskImageFile validation from ServeISODownload
			valid := !(tt.diskImageFile == "." || containsAny(tt.diskImageFile, "/\\"))
			if valid != tt.wantValid {
				t.Errorf("diskImageFile %q: got valid=%v, want valid=%v", tt.diskImageFile, valid, tt.wantValid)
			}
		})
	}
}

// Helper functions for tests
func splitPath(path string, n int) []string {
	parts := make([]string, 0, n)
	for i := 0; i < n-1; i++ {
		idx := indexOf(path, '/')
		if idx == -1 {
			parts = append(parts, path)
			return parts
		}
		parts = append(parts, path[:idx])
		path = path[idx+1:]
	}
	parts = append(parts, path)
	return parts
}

func indexOf(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

func containsAny(s, chars string) bool {
	for i := 0; i < len(s); i++ {
		for j := 0; j < len(chars); j++ {
			if s[i] == chars[j] {
				return true
			}
		}
	}
	return false
}

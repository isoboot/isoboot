package urlutil

import "testing"

func TestFilenameFromURL(t *testing.T) {
	tests := []struct {
		name     string
		rawURL   string
		expected string
	}{
		{"simple", "https://example.com/images/vmlinuz", "vmlinuz"},
		{"nested", "https://example.com/a/b/c/initrd.img", "initrd.img"},
		{"root", "https://example.com/", "artifact"},
		{"no path", "https://example.com", "artifact"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FilenameFromURL(tt.rawURL)
			if got != tt.expected {
				t.Errorf("FilenameFromURL(%q) = %q, want %q", tt.rawURL, got, tt.expected)
			}
		})
	}
}

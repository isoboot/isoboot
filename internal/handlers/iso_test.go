package handlers

import "testing"

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

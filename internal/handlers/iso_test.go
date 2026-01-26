package handlers

import "testing"

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

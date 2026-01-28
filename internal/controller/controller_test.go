package controller

import (
	"strings"
	"testing"

	"github.com/isoboot/isoboot/internal/k8s"
)

// TestCheckDiskImageStatus tests the DiskImage status checking logic
func TestCheckDiskImageStatus(t *testing.T) {
	tests := []struct {
		name          string
		diskImage     *k8s.DiskImage
		expectReady   bool
		expectMsgPart string
	}{
		{
			name: "Complete DiskImage is ready",
			diskImage: &k8s.DiskImage{
				Name: "debian-13",
				Status: k8s.DiskImageStatus{
					Phase: "Complete",
				},
			},
			expectReady:   true,
			expectMsgPart: "",
		},
		{
			name: "Failed DiskImage returns error message",
			diskImage: &k8s.DiskImage{
				Name: "failed-image",
				Status: k8s.DiskImageStatus{
					Phase:   "Failed",
					Message: "HTTP 404",
				},
			},
			expectReady:   false,
			expectMsgPart: "failed: HTTP 404",
		},
		{
			name: "Downloading DiskImage shows progress",
			diskImage: &k8s.DiskImage{
				Name: "downloading-image",
				Status: k8s.DiskImageStatus{
					Phase:    "Downloading",
					Progress: 50,
				},
			},
			expectReady:   false,
			expectMsgPart: "downloading (50%)",
		},
		{
			name: "Pending DiskImage",
			diskImage: &k8s.DiskImage{
				Name: "pending-image",
				Status: k8s.DiskImageStatus{
					Phase: "Pending",
				},
			},
			expectReady:   false,
			expectMsgPart: "pending",
		},
		{
			name: "Empty phase treated as pending",
			diskImage: &k8s.DiskImage{
				Name:   "new-image",
				Status: k8s.DiskImageStatus{},
			},
			expectReady:   false,
			expectMsgPart: "pending",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ready, msg := checkDiskImageStatus(tt.diskImage)
			if ready != tt.expectReady {
				t.Errorf("expected ready=%v, got ready=%v", tt.expectReady, ready)
			}
			if tt.expectMsgPart != "" && !strings.Contains(msg, tt.expectMsgPart) {
				t.Errorf("expected message to contain %q, got %q", tt.expectMsgPart, msg)
			}
		})
	}
}

func TestValidMachineId(t *testing.T) {
	tests := []struct {
		name  string
		input string
		valid bool
	}{
		{"valid 32 hex lowercase", "0123456789abcdef0123456789abcdef", true},
		{"uppercase rejected", "0123456789ABCDEF0123456789ABCDEF", false},
		{"mixed case rejected", "0123456789AbCdEf0123456789aBcDeF", false},
		{"too short 31 chars", "0123456789abcdef0123456789abcde", false},
		{"too long 33 chars", "0123456789abcdef0123456789abcdef0", false},
		{"contains non-hex g", "0123456789abcdefg123456789abcdef", false},
		{"contains dash", "01234567-89ab-cdef-0123-456789abcdef", false},
		{"empty string", "", false},
		{"spaces", "0123456789abcdef 123456789abcdef", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := validMachineId.MatchString(tt.input)
			if got != tt.valid {
				t.Errorf("validMachineId.MatchString(%q) = %v, want %v", tt.input, got, tt.valid)
			}
		})
	}
}

package controller

import (
	"context"
	"encoding/base64"
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

func TestRenderTemplate_BasicVariables(t *testing.T) {
	ctrl := &Controller{
		host: "192.168.1.100",
		port: "8080",
	}

	provision := &k8s.Provision{
		Name: "test-provision",
		Spec: k8s.ProvisionSpec{
			MachineRef:    "vm125",
			BootTargetRef: "debian-13",
		},
	}

	templateContent := `Host: {{ .Host }}
Port: {{ .Port }}
Hostname: {{ .Hostname }}
BootTargetRef: {{ .Target }}`

	ctx := context.Background()
	result, err := ctrl.RenderTemplate(ctx, provision, templateContent)
	if err != nil {
		t.Fatalf("RenderTemplate failed: %v", err)
	}

	expected := `Host: 192.168.1.100
Port: 8080
Hostname: vm125
BootTargetRef: debian-13`

	if result != expected {
		t.Errorf("Expected:\n%s\n\nGot:\n%s", expected, result)
	}
}

func TestRenderTemplate_MissingKey(t *testing.T) {
	ctrl := &Controller{
		host: "192.168.1.100",
		port: "8080",
	}

	provision := &k8s.Provision{
		Name: "test-provision",
		Spec: k8s.ProvisionSpec{
			MachineRef:    "vm125",
			BootTargetRef: "debian-13",
		},
	}

	// Template with undefined variable
	templateContent := `Value: {{ .UndefinedVar }}`

	ctx := context.Background()
	_, err := ctrl.RenderTemplate(ctx, provision, templateContent)
	if err == nil {
		t.Error("Expected error for missing key, got nil")
	}
}

func TestRenderTemplate_InvalidSyntax(t *testing.T) {
	ctrl := &Controller{
		host: "192.168.1.100",
		port: "8080",
	}

	provision := &k8s.Provision{
		Name: "test-provision",
		Spec: k8s.ProvisionSpec{
			MachineRef:    "vm125",
			BootTargetRef: "debian-13",
		},
	}

	// Invalid template syntax
	templateContent := `{{ .Host `

	ctx := context.Background()
	_, err := ctrl.RenderTemplate(ctx, provision, templateContent)
	if err == nil {
		t.Error("Expected error for invalid syntax, got nil")
	}
}

func TestRenderTemplate_PreseedExample(t *testing.T) {
	ctrl := &Controller{
		host: "192.168.1.100",
		port: "8080",
	}

	provision := &k8s.Provision{
		Name: "test-provision",
		Spec: k8s.ProvisionSpec{
			MachineRef:    "vm125",
			BootTargetRef: "debian-13",
		},
	}

	templateContent := `d-i debian-installer/locale string en_US
d-i netcfg/get_hostname string {{ .Hostname }}
d-i preseed/late_command string curl http://{{ .Host }}:{{ .Port }}/api/deploy/{{ .Hostname }}/complete -X POST`

	ctx := context.Background()
	result, err := ctrl.RenderTemplate(ctx, provision, templateContent)
	if err != nil {
		t.Fatalf("RenderTemplate failed: %v", err)
	}

	expected := `d-i debian-installer/locale string en_US
d-i netcfg/get_hostname string vm125
d-i preseed/late_command string curl http://192.168.1.100:8080/api/deploy/vm125/complete -X POST`

	if result != expected {
		t.Errorf("Expected:\n%s\n\nGot:\n%s", expected, result)
	}
}

func TestRenderTemplate_B64EncInTemplate(t *testing.T) {
	ctrl := &Controller{
		host: "192.168.1.100",
		port: "8080",
	}

	provision := &k8s.Provision{
		Name: "test-provision",
		Spec: k8s.ProvisionSpec{
			MachineRef:    "vm125",
			BootTargetRef: "debian-13",
		},
	}

	// Test b64enc with system variable
	templateContent := `encoded: {{ .Hostname | b64enc }}`

	ctx := context.Background()
	result, err := ctrl.RenderTemplate(ctx, provision, templateContent)
	if err != nil {
		t.Fatalf("RenderTemplate failed: %v", err)
	}

	expected := "encoded: " + base64.StdEncoding.EncodeToString([]byte("vm125"))
	if result != expected {
		t.Errorf("Expected: %s\nGot: %s", expected, result)
	}
}

func TestRenderTemplate_HasKey(t *testing.T) {
	ctrl := &Controller{
		host: "192.168.1.100",
		port: "8080",
	}

	provision := &k8s.Provision{
		Name: "test-provision",
		Spec: k8s.ProvisionSpec{
			MachineRef:    "vm125",
			BootTargetRef: "debian-13",
		},
	}

	// Test hasKey with existing and non-existing keys
	templateContent := `{{ if hasKey . "Host" }}host={{ .Host }}{{ end }}|{{ if hasKey . "NonExistent" }}found{{ else }}not-found{{ end }}`

	ctx := context.Background()
	result, err := ctrl.RenderTemplate(ctx, provision, templateContent)
	if err != nil {
		t.Fatalf("RenderTemplate failed: %v", err)
	}

	expected := "host=192.168.1.100|not-found"
	if result != expected {
		t.Errorf("Expected: %s\nGot: %s", expected, result)
	}
}

func TestValidMachineId(t *testing.T) {
	tests := []struct {
		name  string
		input string
		valid bool
	}{
		{"valid 32 hex lowercase", "0123456789abcdef0123456789abcdef", true},
		{"valid 32 hex uppercase", "0123456789ABCDEF0123456789ABCDEF", true},
		{"valid 32 hex mixed case", "0123456789AbCdEf0123456789aBcDeF", true},
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

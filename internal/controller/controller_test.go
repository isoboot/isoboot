package controller

import (
	"context"
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

	deploy := &k8s.Deploy{
		Name: "test-deploy",
		Spec: k8s.DeploySpec{
			MachineRef:    "vm125",
			BootTargetRef: "debian-13",
		},
	}

	templateContent := `Host: {{ .Host }}
Port: {{ .Port }}
Hostname: {{ .Hostname }}
BootTargetRef: {{ .Target }}`

	ctx := context.Background()
	result, err := ctrl.RenderTemplate(ctx, deploy, templateContent)
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

	deploy := &k8s.Deploy{
		Name: "test-deploy",
		Spec: k8s.DeploySpec{
			MachineRef:    "vm125",
			BootTargetRef: "debian-13",
		},
	}

	// Template with undefined variable
	templateContent := `Value: {{ .UndefinedVar }}`

	ctx := context.Background()
	_, err := ctrl.RenderTemplate(ctx, deploy, templateContent)
	if err == nil {
		t.Error("Expected error for missing key, got nil")
	}
}

func TestRenderTemplate_InvalidSyntax(t *testing.T) {
	ctrl := &Controller{
		host: "192.168.1.100",
		port: "8080",
	}

	deploy := &k8s.Deploy{
		Name: "test-deploy",
		Spec: k8s.DeploySpec{
			MachineRef:    "vm125",
			BootTargetRef: "debian-13",
		},
	}

	// Invalid template syntax
	templateContent := `{{ .Host `

	ctx := context.Background()
	_, err := ctrl.RenderTemplate(ctx, deploy, templateContent)
	if err == nil {
		t.Error("Expected error for invalid syntax, got nil")
	}
}

func TestRenderTemplate_PreseedExample(t *testing.T) {
	ctrl := &Controller{
		host: "192.168.1.100",
		port: "8080",
	}

	deploy := &k8s.Deploy{
		Name: "test-deploy",
		Spec: k8s.DeploySpec{
			MachineRef:    "vm125",
			BootTargetRef: "debian-13",
		},
	}

	templateContent := `d-i debian-installer/locale string en_US
d-i netcfg/get_hostname string {{ .Hostname }}
d-i preseed/late_command string curl http://{{ .Host }}:{{ .Port }}/api/deploy/{{ .Hostname }}/complete -X POST`

	ctx := context.Background()
	result, err := ctrl.RenderTemplate(ctx, deploy, templateContent)
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

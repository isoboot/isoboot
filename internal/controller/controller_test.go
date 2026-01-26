package controller

import (
	"context"
	"testing"

	"github.com/isoboot/isoboot/internal/k8s"
)

func TestRenderTemplate_BasicVariables(t *testing.T) {
	ctrl := &Controller{
		host: "192.168.1.100",
		port: "8080",
	}

	deploy := &k8s.Deploy{
		Name: "test-deploy",
		Spec: k8s.DeploySpec{
			MachineRef: "vm125",
			BootTargetRef: "debian-13",
		},
	}

	templateContent := `Host: {{ .Host }}
Port: {{ .Port }}
Hostname: {{ .Hostname }}
Target: {{ .Target }}`

	ctx := context.Background()
	result, err := ctrl.RenderTemplate(ctx, deploy, templateContent)
	if err != nil {
		t.Fatalf("RenderTemplate failed: %v", err)
	}

	expected := `Host: 192.168.1.100
Port: 8080
Hostname: vm125
Target: debian-13`

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
			MachineRef: "vm125",
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
			MachineRef: "vm125",
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
			MachineRef: "vm125",
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

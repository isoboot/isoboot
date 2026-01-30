package controller

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/isoboot/isoboot/internal/k8s"
)

// TestCheckBootMediaStatus tests the BootMedia status checking logic
func TestCheckBootMediaStatus(t *testing.T) {
	tests := []struct {
		name          string
		bootMedia     *k8s.BootMedia
		expectReady   bool
		expectMsgPart string
	}{
		{
			name: "Complete BootMedia is ready",
			bootMedia: &k8s.BootMedia{
				Name: "debian-13",
				Status: k8s.BootMediaStatus{
					Phase: "Complete",
				},
			},
			expectReady:   true,
			expectMsgPart: "",
		},
		{
			name: "Failed BootMedia returns error message",
			bootMedia: &k8s.BootMedia{
				Name: "failed-media",
				Status: k8s.BootMediaStatus{
					Phase:   "Failed",
					Message: "HTTP 404",
				},
			},
			expectReady:   false,
			expectMsgPart: "failed: HTTP 404",
		},
		{
			name: "Downloading BootMedia",
			bootMedia: &k8s.BootMedia{
				Name: "downloading-media",
				Status: k8s.BootMediaStatus{
					Phase: "Downloading",
				},
			},
			expectReady:   false,
			expectMsgPart: "downloading",
		},
		{
			name: "Pending BootMedia",
			bootMedia: &k8s.BootMedia{
				Name: "pending-media",
				Status: k8s.BootMediaStatus{
					Phase: "Pending",
				},
			},
			expectReady:   false,
			expectMsgPart: "pending",
		},
		{
			name: "Empty phase treated as pending",
			bootMedia: &k8s.BootMedia{
				Name:   "new-media",
				Status: k8s.BootMediaStatus{},
			},
			expectReady:   false,
			expectMsgPart: "pending",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ready, msg := checkBootMediaStatus(tt.bootMedia)
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

func TestReconcileProvision_InitializePending(t *testing.T) {
	fake := newFakeK8sClient()
	fake.provisions["prov-1"] = &k8s.Provision{
		Name: "prov-1",
		Spec: k8s.ProvisionSpec{
			MachineRef:    "vm-01",
			BootTargetRef: "debian-13",
		},
		Status: k8s.ProvisionStatus{Phase: ""},
	}
	fake.machines["vm-01"] = &k8s.Machine{Name: "vm-01", MAC: "aa-bb-cc-dd-ee-ff"}
	fake.bootTargets["debian-13"] = &k8s.BootTarget{
		Name:         "debian-13",
		BootMediaRef: "debian-13",
		Template:     "#!ipxe\n",
	}
	fake.bootMedias["debian-13"] = &k8s.BootMedia{
		Name:   "debian-13",
		Status: k8s.BootMediaStatus{Phase: "Complete"},
	}

	ctrl := New(fake)
	ctrl.reconcileProvision(context.Background(), fake.provisions["prov-1"])

	s, ok := fake.getProvisionStatus("prov-1")
	if !ok {
		t.Fatal("expected provision status to be set")
	}
	if s.Phase != "Pending" {
		t.Errorf("expected phase Pending, got %q", s.Phase)
	}
}

func TestReconcileProvision_ConfigError_MissingMachine(t *testing.T) {
	fake := newFakeK8sClient()
	fake.provisions["prov-1"] = &k8s.Provision{
		Name: "prov-1",
		Spec: k8s.ProvisionSpec{
			MachineRef:    "missing-machine",
			BootTargetRef: "debian-13",
		},
		Status: k8s.ProvisionStatus{Phase: "Pending"},
	}

	ctrl := New(fake)
	ctrl.reconcileProvision(context.Background(), fake.provisions["prov-1"])

	s, ok := fake.getProvisionStatus("prov-1")
	if !ok {
		t.Fatal("expected provision status to be set")
	}
	if s.Phase != "ConfigError" {
		t.Errorf("expected phase ConfigError, got %q", s.Phase)
	}
	if !strings.Contains(s.Message, "Machine") {
		t.Errorf("expected message about Machine, got %q", s.Message)
	}
}

func TestReconcileProvision_ConfigError_MissingBootTarget(t *testing.T) {
	fake := newFakeK8sClient()
	fake.provisions["prov-1"] = &k8s.Provision{
		Name: "prov-1",
		Spec: k8s.ProvisionSpec{
			MachineRef:    "vm-01",
			BootTargetRef: "missing-bt",
		},
		Status: k8s.ProvisionStatus{Phase: "Pending"},
	}
	fake.machines["vm-01"] = &k8s.Machine{Name: "vm-01", MAC: "aa-bb-cc-dd-ee-ff"}

	ctrl := New(fake)
	ctrl.reconcileProvision(context.Background(), fake.provisions["prov-1"])

	s, _ := fake.getProvisionStatus("prov-1")
	if s.Phase != "ConfigError" {
		t.Errorf("expected phase ConfigError, got %q", s.Phase)
	}
	if !strings.Contains(s.Message, "BootTarget") {
		t.Errorf("expected message about BootTarget, got %q", s.Message)
	}
}

func TestReconcileProvision_ConfigError_MissingBootMedia(t *testing.T) {
	fake := newFakeK8sClient()
	fake.provisions["prov-1"] = &k8s.Provision{
		Name: "prov-1",
		Spec: k8s.ProvisionSpec{
			MachineRef:    "vm-01",
			BootTargetRef: "debian-13",
		},
		Status: k8s.ProvisionStatus{Phase: "Pending"},
	}
	fake.machines["vm-01"] = &k8s.Machine{Name: "vm-01", MAC: "aa-bb-cc-dd-ee-ff"}
	fake.bootTargets["debian-13"] = &k8s.BootTarget{
		Name:         "debian-13",
		BootMediaRef: "missing-bm",
	}

	ctrl := New(fake)
	ctrl.reconcileProvision(context.Background(), fake.provisions["prov-1"])

	s, _ := fake.getProvisionStatus("prov-1")
	if s.Phase != "ConfigError" {
		t.Errorf("expected phase ConfigError, got %q", s.Phase)
	}
	if !strings.Contains(s.Message, "BootMedia") {
		t.Errorf("expected message about BootMedia, got %q", s.Message)
	}
}

func TestReconcileProvision_ConfigError_InvalidMachineId(t *testing.T) {
	fake := newFakeK8sClient()
	fake.provisions["prov-1"] = &k8s.Provision{
		Name: "prov-1",
		Spec: k8s.ProvisionSpec{
			MachineRef:    "vm-01",
			BootTargetRef: "debian-13",
			MachineId:     "INVALID-UPPERCASE",
		},
		Status: k8s.ProvisionStatus{Phase: "Pending"},
	}
	fake.machines["vm-01"] = &k8s.Machine{Name: "vm-01", MAC: "aa-bb-cc-dd-ee-ff"}
	fake.bootTargets["debian-13"] = &k8s.BootTarget{
		Name:         "debian-13",
		BootMediaRef: "debian-13",
	}
	fake.bootMedias["debian-13"] = &k8s.BootMedia{
		Name:   "debian-13",
		Status: k8s.BootMediaStatus{Phase: "Complete"},
	}

	ctrl := New(fake)
	ctrl.reconcileProvision(context.Background(), fake.provisions["prov-1"])

	s, _ := fake.getProvisionStatus("prov-1")
	if s.Phase != "ConfigError" {
		t.Errorf("expected phase ConfigError, got %q", s.Phase)
	}
	if !strings.Contains(s.Message, "machineId") {
		t.Errorf("expected message about machineId, got %q", s.Message)
	}
}

func TestReconcileProvision_ConfigError_MissingConfigMap(t *testing.T) {
	fake := newFakeK8sClient()
	fake.provisions["prov-1"] = &k8s.Provision{
		Name: "prov-1",
		Spec: k8s.ProvisionSpec{
			MachineRef:    "vm-01",
			BootTargetRef: "debian-13",
			ConfigMaps:    []string{"missing-cm"},
		},
		Status: k8s.ProvisionStatus{Phase: "Pending"},
	}
	fake.machines["vm-01"] = &k8s.Machine{Name: "vm-01", MAC: "aa-bb-cc-dd-ee-ff"}
	fake.bootTargets["debian-13"] = &k8s.BootTarget{
		Name:         "debian-13",
		BootMediaRef: "debian-13",
	}
	fake.bootMedias["debian-13"] = &k8s.BootMedia{
		Name:   "debian-13",
		Status: k8s.BootMediaStatus{Phase: "Complete"},
	}

	ctrl := New(fake)
	ctrl.reconcileProvision(context.Background(), fake.provisions["prov-1"])

	s, _ := fake.getProvisionStatus("prov-1")
	if s.Phase != "ConfigError" {
		t.Errorf("expected phase ConfigError, got %q", s.Phase)
	}
	if !strings.Contains(s.Message, "ConfigMap") {
		t.Errorf("expected message about ConfigMap, got %q", s.Message)
	}
}

func TestReconcileProvision_ConfigError_MissingSecret(t *testing.T) {
	fake := newFakeK8sClient()
	fake.provisions["prov-1"] = &k8s.Provision{
		Name: "prov-1",
		Spec: k8s.ProvisionSpec{
			MachineRef:    "vm-01",
			BootTargetRef: "debian-13",
			Secrets:       []string{"missing-secret"},
		},
		Status: k8s.ProvisionStatus{Phase: "Pending"},
	}
	fake.machines["vm-01"] = &k8s.Machine{Name: "vm-01", MAC: "aa-bb-cc-dd-ee-ff"}
	fake.bootTargets["debian-13"] = &k8s.BootTarget{
		Name:         "debian-13",
		BootMediaRef: "debian-13",
	}
	fake.bootMedias["debian-13"] = &k8s.BootMedia{
		Name:   "debian-13",
		Status: k8s.BootMediaStatus{Phase: "Complete"},
	}

	ctrl := New(fake)
	ctrl.reconcileProvision(context.Background(), fake.provisions["prov-1"])

	s, _ := fake.getProvisionStatus("prov-1")
	if s.Phase != "ConfigError" {
		t.Errorf("expected phase ConfigError, got %q", s.Phase)
	}
	if !strings.Contains(s.Message, "Secret") {
		t.Errorf("expected message about Secret, got %q", s.Message)
	}
}

func TestReconcileProvision_WaitingForBootMedia(t *testing.T) {
	fake := newFakeK8sClient()
	fake.provisions["prov-1"] = &k8s.Provision{
		Name: "prov-1",
		Spec: k8s.ProvisionSpec{
			MachineRef:    "vm-01",
			BootTargetRef: "debian-13",
		},
		Status: k8s.ProvisionStatus{Phase: "Pending"},
	}
	fake.machines["vm-01"] = &k8s.Machine{Name: "vm-01", MAC: "aa-bb-cc-dd-ee-ff"}
	fake.bootTargets["debian-13"] = &k8s.BootTarget{
		Name:         "debian-13",
		BootMediaRef: "debian-13",
	}
	fake.bootMedias["debian-13"] = &k8s.BootMedia{
		Name:   "debian-13",
		Status: k8s.BootMediaStatus{Phase: "Downloading"},
	}

	ctrl := New(fake)
	ctrl.reconcileProvision(context.Background(), fake.provisions["prov-1"])

	s, _ := fake.getProvisionStatus("prov-1")
	if s.Phase != "WaitingForBootMedia" {
		t.Errorf("expected phase WaitingForBootMedia, got %q", s.Phase)
	}
}

func TestReconcileProvision_ConfigErrorRecovery(t *testing.T) {
	fake := newFakeK8sClient()
	fake.provisions["prov-1"] = &k8s.Provision{
		Name: "prov-1",
		Spec: k8s.ProvisionSpec{
			MachineRef:    "vm-01",
			BootTargetRef: "debian-13",
		},
		Status: k8s.ProvisionStatus{Phase: "ConfigError", Message: "old error"},
	}
	fake.machines["vm-01"] = &k8s.Machine{Name: "vm-01", MAC: "aa-bb-cc-dd-ee-ff"}
	fake.bootTargets["debian-13"] = &k8s.BootTarget{
		Name:         "debian-13",
		BootMediaRef: "debian-13",
	}
	fake.bootMedias["debian-13"] = &k8s.BootMedia{
		Name:   "debian-13",
		Status: k8s.BootMediaStatus{Phase: "Complete"},
	}

	ctrl := New(fake)
	ctrl.reconcileProvision(context.Background(), fake.provisions["prov-1"])

	s, _ := fake.getProvisionStatus("prov-1")
	if s.Phase != "Pending" {
		t.Errorf("expected recovery to Pending, got %q", s.Phase)
	}
}

func TestReconcileProvision_TimeoutInProgress(t *testing.T) {
	fake := newFakeK8sClient()
	fake.provisions["prov-1"] = &k8s.Provision{
		Name: "prov-1",
		Spec: k8s.ProvisionSpec{
			MachineRef:    "vm-01",
			BootTargetRef: "debian-13",
		},
		Status: k8s.ProvisionStatus{
			Phase:       "InProgress",
			LastUpdated: time.Now().Add(-31 * time.Minute),
		},
	}
	fake.machines["vm-01"] = &k8s.Machine{Name: "vm-01", MAC: "aa-bb-cc-dd-ee-ff"}
	fake.bootTargets["debian-13"] = &k8s.BootTarget{
		Name:         "debian-13",
		BootMediaRef: "debian-13",
	}
	fake.bootMedias["debian-13"] = &k8s.BootMedia{
		Name:   "debian-13",
		Status: k8s.BootMediaStatus{Phase: "Complete"},
	}

	ctrl := New(fake)
	ctrl.reconcileProvision(context.Background(), fake.provisions["prov-1"])

	s, _ := fake.getProvisionStatus("prov-1")
	if s.Phase != "Failed" {
		t.Errorf("expected phase Failed (timeout), got %q", s.Phase)
	}
	if !strings.Contains(s.Message, "Timed out") {
		t.Errorf("expected timeout message, got %q", s.Message)
	}
}

func TestReconcileProvision_InProgressNotTimedOut(t *testing.T) {
	fake := newFakeK8sClient()
	fake.provisions["prov-1"] = &k8s.Provision{
		Name: "prov-1",
		Spec: k8s.ProvisionSpec{
			MachineRef:    "vm-01",
			BootTargetRef: "debian-13",
		},
		Status: k8s.ProvisionStatus{
			Phase:       "InProgress",
			LastUpdated: time.Now().Add(-5 * time.Minute),
		},
	}
	fake.machines["vm-01"] = &k8s.Machine{Name: "vm-01", MAC: "aa-bb-cc-dd-ee-ff"}
	fake.bootTargets["debian-13"] = &k8s.BootTarget{
		Name:         "debian-13",
		BootMediaRef: "debian-13",
	}
	fake.bootMedias["debian-13"] = &k8s.BootMedia{
		Name:   "debian-13",
		Status: k8s.BootMediaStatus{Phase: "Complete"},
	}

	ctrl := New(fake)
	ctrl.reconcileProvision(context.Background(), fake.provisions["prov-1"])

	// Should NOT have updated the status (still within timeout)
	if _, ok := fake.getProvisionStatus("prov-1"); ok {
		t.Error("expected no status update for non-timed-out InProgress provision")
	}
}

func TestReconcileProvision_CompleteIsNoop(t *testing.T) {
	fake := newFakeK8sClient()
	fake.provisions["prov-1"] = &k8s.Provision{
		Name: "prov-1",
		Spec: k8s.ProvisionSpec{
			MachineRef:    "vm-01",
			BootTargetRef: "debian-13",
		},
		Status: k8s.ProvisionStatus{Phase: "Complete"},
	}
	fake.machines["vm-01"] = &k8s.Machine{Name: "vm-01", MAC: "aa-bb-cc-dd-ee-ff"}
	fake.bootTargets["debian-13"] = &k8s.BootTarget{
		Name:         "debian-13",
		BootMediaRef: "debian-13",
	}
	fake.bootMedias["debian-13"] = &k8s.BootMedia{
		Name:   "debian-13",
		Status: k8s.BootMediaStatus{Phase: "Complete"},
	}

	ctrl := New(fake)
	ctrl.reconcileProvision(context.Background(), fake.provisions["prov-1"])

	// Complete provisions should not trigger any status update
	if _, ok := fake.getProvisionStatus("prov-1"); ok {
		t.Error("expected no status update for Complete provision")
	}
}

func TestValidateProvisionRefs_AllValid(t *testing.T) {
	fake := newFakeK8sClient()
	fake.machines["vm-01"] = &k8s.Machine{Name: "vm-01", MAC: "aa-bb-cc-dd-ee-ff"}
	fake.bootTargets["debian-13"] = &k8s.BootTarget{
		Name:         "debian-13",
		BootMediaRef: "debian-13",
	}
	fake.bootMedias["debian-13"] = &k8s.BootMedia{
		Name:   "debian-13",
		Status: k8s.BootMediaStatus{Phase: "Complete"},
	}
	fake.responseTemplates["preseed"] = &k8s.ResponseTemplate{Name: "preseed", Files: map[string]string{"preseed.cfg": "content"}}
	fake.configMaps["net-cfg"] = newConfigMap("net-cfg", map[string]string{"gateway": "10.0.0.1"})
	fake.secrets["ssh-keys"] = newSecret("ssh-keys", map[string][]byte{"key": []byte("data")})

	ctrl := New(fake)
	provision := &k8s.Provision{
		Name: "prov-1",
		Spec: k8s.ProvisionSpec{
			MachineRef:          "vm-01",
			BootTargetRef:       "debian-13",
			ResponseTemplateRef: "preseed",
			ConfigMaps:          []string{"net-cfg"},
			Secrets:             []string{"ssh-keys"},
		},
	}

	err := ctrl.validateProvisionRefs(context.Background(), provision)
	if err != nil {
		t.Errorf("expected no error for valid refs, got %v", err)
	}
}

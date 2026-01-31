package controller

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/isoboot/isoboot/internal/k8s"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
				ObjectMeta: metav1.ObjectMeta{Name: "debian-13"},
				Status:     k8s.BootMediaStatus{Phase: "Complete"},
			},
			expectReady:   true,
			expectMsgPart: "",
		},
		{
			name: "Failed BootMedia returns error message",
			bootMedia: &k8s.BootMedia{
				ObjectMeta: metav1.ObjectMeta{Name: "failed-media"},
				Status:     k8s.BootMediaStatus{Phase: "Failed", Message: "HTTP 404"},
			},
			expectReady:   false,
			expectMsgPart: "failed: HTTP 404",
		},
		{
			name: "Downloading BootMedia",
			bootMedia: &k8s.BootMedia{
				ObjectMeta: metav1.ObjectMeta{Name: "downloading-media"},
				Status:     k8s.BootMediaStatus{Phase: "Downloading"},
			},
			expectReady:   false,
			expectMsgPart: "downloading",
		},
		{
			name: "Pending BootMedia",
			bootMedia: &k8s.BootMedia{
				ObjectMeta: metav1.ObjectMeta{Name: "pending-media"},
				Status:     k8s.BootMediaStatus{Phase: "Pending"},
			},
			expectReady:   false,
			expectMsgPart: "pending",
		},
		{
			name: "Empty phase treated as pending",
			bootMedia: &k8s.BootMedia{
				ObjectMeta: metav1.ObjectMeta{Name: "new-media"},
				Status:     k8s.BootMediaStatus{},
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
	ctx := context.Background()
	provision := &k8s.Provision{
		ObjectMeta: metav1.ObjectMeta{Name: "prov-1", Namespace: "default"},
		Spec: k8s.ProvisionSpec{
			MachineRef:    "vm-01",
			BootTargetRef: "debian-13",
		},
	}
	k := newTestK8sClient(
		provision,
		&k8s.Machine{
			ObjectMeta: metav1.ObjectMeta{Name: "vm-01", Namespace: "default"},
			Spec:       k8s.MachineSpec{MAC: "aa-bb-cc-dd-ee-ff"},
		},
		&k8s.BootTarget{
			ObjectMeta: metav1.ObjectMeta{Name: "debian-13", Namespace: "default"},
			Spec:       k8s.BootTargetSpec{BootMediaRef: "debian-iso", Template: "#!ipxe\n"},
		},
		&k8s.BootMedia{
			ObjectMeta: metav1.ObjectMeta{Name: "debian-iso", Namespace: "default"},
			Status:     k8s.BootMediaStatus{Phase: "Complete"},
		},
	)

	ctrl := New(k)
	ctrl.reconcileProvision(ctx, provision)

	var updated k8s.Provision
	if err := k.Get(ctx, k.Key("prov-1"), &updated); err != nil {
		t.Fatalf("failed to get provision: %v", err)
	}
	if updated.Status.Phase != "Pending" {
		t.Errorf("expected phase Pending, got %q", updated.Status.Phase)
	}
}

func TestReconcileProvision_ConfigError_MissingMachine(t *testing.T) {
	ctx := context.Background()
	provision := &k8s.Provision{
		ObjectMeta: metav1.ObjectMeta{Name: "prov-1", Namespace: "default"},
		Spec: k8s.ProvisionSpec{
			MachineRef:    "missing-machine",
			BootTargetRef: "debian-13",
		},
		Status: k8s.ProvisionStatus{Phase: "Pending"},
	}
	k := newTestK8sClient(provision)

	ctrl := New(k)
	ctrl.reconcileProvision(ctx, provision)

	var updated k8s.Provision
	if err := k.Get(ctx, k.Key("prov-1"), &updated); err != nil {
		t.Fatalf("failed to get provision: %v", err)
	}
	if updated.Status.Phase != "ConfigError" {
		t.Errorf("expected phase ConfigError, got %q", updated.Status.Phase)
	}
	if !strings.Contains(updated.Status.Message, "Machine") {
		t.Errorf("expected message about Machine, got %q", updated.Status.Message)
	}
}

func TestReconcileProvision_ConfigError_MissingBootTarget(t *testing.T) {
	ctx := context.Background()
	provision := &k8s.Provision{
		ObjectMeta: metav1.ObjectMeta{Name: "prov-1", Namespace: "default"},
		Spec: k8s.ProvisionSpec{
			MachineRef:    "vm-01",
			BootTargetRef: "missing-bt",
		},
		Status: k8s.ProvisionStatus{Phase: "Pending"},
	}
	k := newTestK8sClient(
		provision,
		&k8s.Machine{
			ObjectMeta: metav1.ObjectMeta{Name: "vm-01", Namespace: "default"},
			Spec:       k8s.MachineSpec{MAC: "aa-bb-cc-dd-ee-ff"},
		},
	)

	ctrl := New(k)
	ctrl.reconcileProvision(ctx, provision)

	var updated k8s.Provision
	if err := k.Get(ctx, k.Key("prov-1"), &updated); err != nil {
		t.Fatalf("failed to get provision: %v", err)
	}
	if updated.Status.Phase != "ConfigError" {
		t.Errorf("expected phase ConfigError, got %q", updated.Status.Phase)
	}
	if !strings.Contains(updated.Status.Message, "BootTarget") {
		t.Errorf("expected message about BootTarget, got %q", updated.Status.Message)
	}
}

func TestReconcileProvision_ConfigError_MissingBootMedia(t *testing.T) {
	ctx := context.Background()
	provision := &k8s.Provision{
		ObjectMeta: metav1.ObjectMeta{Name: "prov-1", Namespace: "default"},
		Spec: k8s.ProvisionSpec{
			MachineRef:    "vm-01",
			BootTargetRef: "debian-13",
		},
		Status: k8s.ProvisionStatus{Phase: "Pending"},
	}
	k := newTestK8sClient(
		provision,
		&k8s.Machine{
			ObjectMeta: metav1.ObjectMeta{Name: "vm-01", Namespace: "default"},
			Spec:       k8s.MachineSpec{MAC: "aa-bb-cc-dd-ee-ff"},
		},
		&k8s.BootTarget{
			ObjectMeta: metav1.ObjectMeta{Name: "debian-13", Namespace: "default"},
			Spec:       k8s.BootTargetSpec{BootMediaRef: "missing-bm"},
		},
	)

	ctrl := New(k)
	ctrl.reconcileProvision(ctx, provision)

	var updated k8s.Provision
	if err := k.Get(ctx, k.Key("prov-1"), &updated); err != nil {
		t.Fatalf("failed to get provision: %v", err)
	}
	if updated.Status.Phase != "ConfigError" {
		t.Errorf("expected phase ConfigError, got %q", updated.Status.Phase)
	}
	if !strings.Contains(updated.Status.Message, "BootMedia") {
		t.Errorf("expected message about BootMedia, got %q", updated.Status.Message)
	}
}

func TestReconcileProvision_ConfigError_InvalidMachineId(t *testing.T) {
	ctx := context.Background()
	provision := &k8s.Provision{
		ObjectMeta: metav1.ObjectMeta{Name: "prov-1", Namespace: "default"},
		Spec: k8s.ProvisionSpec{
			MachineRef:    "vm-01",
			BootTargetRef: "debian-13",
			MachineId:     "INVALID-UPPERCASE",
		},
		Status: k8s.ProvisionStatus{Phase: "Pending"},
	}
	k := newTestK8sClient(
		provision,
		&k8s.Machine{
			ObjectMeta: metav1.ObjectMeta{Name: "vm-01", Namespace: "default"},
			Spec:       k8s.MachineSpec{MAC: "aa-bb-cc-dd-ee-ff"},
		},
		&k8s.BootTarget{
			ObjectMeta: metav1.ObjectMeta{Name: "debian-13", Namespace: "default"},
			Spec:       k8s.BootTargetSpec{BootMediaRef: "debian-iso"},
		},
		&k8s.BootMedia{
			ObjectMeta: metav1.ObjectMeta{Name: "debian-iso", Namespace: "default"},
			Status:     k8s.BootMediaStatus{Phase: "Complete"},
		},
	)

	ctrl := New(k)
	ctrl.reconcileProvision(ctx, provision)

	var updated k8s.Provision
	if err := k.Get(ctx, k.Key("prov-1"), &updated); err != nil {
		t.Fatalf("failed to get provision: %v", err)
	}
	if updated.Status.Phase != "ConfigError" {
		t.Errorf("expected phase ConfigError, got %q", updated.Status.Phase)
	}
	if !strings.Contains(updated.Status.Message, "machineId") {
		t.Errorf("expected message about machineId, got %q", updated.Status.Message)
	}
}

func TestReconcileProvision_ConfigError_MissingConfigMap(t *testing.T) {
	ctx := context.Background()
	provision := &k8s.Provision{
		ObjectMeta: metav1.ObjectMeta{Name: "prov-1", Namespace: "default"},
		Spec: k8s.ProvisionSpec{
			MachineRef:    "vm-01",
			BootTargetRef: "debian-13",
			ConfigMaps:    []string{"missing-cm"},
		},
		Status: k8s.ProvisionStatus{Phase: "Pending"},
	}
	k := newTestK8sClient(
		provision,
		&k8s.Machine{
			ObjectMeta: metav1.ObjectMeta{Name: "vm-01", Namespace: "default"},
			Spec:       k8s.MachineSpec{MAC: "aa-bb-cc-dd-ee-ff"},
		},
		&k8s.BootTarget{
			ObjectMeta: metav1.ObjectMeta{Name: "debian-13", Namespace: "default"},
			Spec:       k8s.BootTargetSpec{BootMediaRef: "debian-iso"},
		},
		&k8s.BootMedia{
			ObjectMeta: metav1.ObjectMeta{Name: "debian-iso", Namespace: "default"},
			Status:     k8s.BootMediaStatus{Phase: "Complete"},
		},
	)

	ctrl := New(k)
	ctrl.reconcileProvision(ctx, provision)

	var updated k8s.Provision
	if err := k.Get(ctx, k.Key("prov-1"), &updated); err != nil {
		t.Fatalf("failed to get provision: %v", err)
	}
	if updated.Status.Phase != "ConfigError" {
		t.Errorf("expected phase ConfigError, got %q", updated.Status.Phase)
	}
	if !strings.Contains(updated.Status.Message, "ConfigMap") {
		t.Errorf("expected message about ConfigMap, got %q", updated.Status.Message)
	}
}

func TestReconcileProvision_ConfigError_MissingSecret(t *testing.T) {
	ctx := context.Background()
	provision := &k8s.Provision{
		ObjectMeta: metav1.ObjectMeta{Name: "prov-1", Namespace: "default"},
		Spec: k8s.ProvisionSpec{
			MachineRef:    "vm-01",
			BootTargetRef: "debian-13",
			Secrets:       []string{"missing-secret"},
		},
		Status: k8s.ProvisionStatus{Phase: "Pending"},
	}
	k := newTestK8sClient(
		provision,
		&k8s.Machine{
			ObjectMeta: metav1.ObjectMeta{Name: "vm-01", Namespace: "default"},
			Spec:       k8s.MachineSpec{MAC: "aa-bb-cc-dd-ee-ff"},
		},
		&k8s.BootTarget{
			ObjectMeta: metav1.ObjectMeta{Name: "debian-13", Namespace: "default"},
			Spec:       k8s.BootTargetSpec{BootMediaRef: "debian-iso"},
		},
		&k8s.BootMedia{
			ObjectMeta: metav1.ObjectMeta{Name: "debian-iso", Namespace: "default"},
			Status:     k8s.BootMediaStatus{Phase: "Complete"},
		},
	)

	ctrl := New(k)
	ctrl.reconcileProvision(ctx, provision)

	var updated k8s.Provision
	if err := k.Get(ctx, k.Key("prov-1"), &updated); err != nil {
		t.Fatalf("failed to get provision: %v", err)
	}
	if updated.Status.Phase != "ConfigError" {
		t.Errorf("expected phase ConfigError, got %q", updated.Status.Phase)
	}
	if !strings.Contains(updated.Status.Message, "Secret") {
		t.Errorf("expected message about Secret, got %q", updated.Status.Message)
	}
}

func TestReconcileProvision_WaitingForBootMedia(t *testing.T) {
	ctx := context.Background()
	provision := &k8s.Provision{
		ObjectMeta: metav1.ObjectMeta{Name: "prov-1", Namespace: "default"},
		Spec: k8s.ProvisionSpec{
			MachineRef:    "vm-01",
			BootTargetRef: "debian-13",
		},
		Status: k8s.ProvisionStatus{Phase: "Pending"},
	}
	k := newTestK8sClient(
		provision,
		&k8s.Machine{
			ObjectMeta: metav1.ObjectMeta{Name: "vm-01", Namespace: "default"},
			Spec:       k8s.MachineSpec{MAC: "aa-bb-cc-dd-ee-ff"},
		},
		&k8s.BootTarget{
			ObjectMeta: metav1.ObjectMeta{Name: "debian-13", Namespace: "default"},
			Spec:       k8s.BootTargetSpec{BootMediaRef: "debian-iso"},
		},
		&k8s.BootMedia{
			ObjectMeta: metav1.ObjectMeta{Name: "debian-iso", Namespace: "default"},
			Status:     k8s.BootMediaStatus{Phase: "Downloading"},
		},
	)

	ctrl := New(k)
	ctrl.reconcileProvision(ctx, provision)

	var updated k8s.Provision
	if err := k.Get(ctx, k.Key("prov-1"), &updated); err != nil {
		t.Fatalf("failed to get provision: %v", err)
	}
	if updated.Status.Phase != "WaitingForBootMedia" {
		t.Errorf("expected phase WaitingForBootMedia, got %q", updated.Status.Phase)
	}
}

func TestReconcileProvision_ConfigErrorRecovery(t *testing.T) {
	ctx := context.Background()
	provision := &k8s.Provision{
		ObjectMeta: metav1.ObjectMeta{Name: "prov-1", Namespace: "default"},
		Spec: k8s.ProvisionSpec{
			MachineRef:    "vm-01",
			BootTargetRef: "debian-13",
		},
		Status: k8s.ProvisionStatus{Phase: "ConfigError", Message: "old error"},
	}
	k := newTestK8sClient(
		provision,
		&k8s.Machine{
			ObjectMeta: metav1.ObjectMeta{Name: "vm-01", Namespace: "default"},
			Spec:       k8s.MachineSpec{MAC: "aa-bb-cc-dd-ee-ff"},
		},
		&k8s.BootTarget{
			ObjectMeta: metav1.ObjectMeta{Name: "debian-13", Namespace: "default"},
			Spec:       k8s.BootTargetSpec{BootMediaRef: "debian-iso"},
		},
		&k8s.BootMedia{
			ObjectMeta: metav1.ObjectMeta{Name: "debian-iso", Namespace: "default"},
			Status:     k8s.BootMediaStatus{Phase: "Complete"},
		},
	)

	ctrl := New(k)
	ctrl.reconcileProvision(ctx, provision)

	var updated k8s.Provision
	if err := k.Get(ctx, k.Key("prov-1"), &updated); err != nil {
		t.Fatalf("failed to get provision: %v", err)
	}
	if updated.Status.Phase != "Pending" {
		t.Errorf("expected recovery to Pending, got %q", updated.Status.Phase)
	}
}

func TestReconcileProvision_TimeoutInProgress(t *testing.T) {
	ctx := context.Background()
	provision := &k8s.Provision{
		ObjectMeta: metav1.ObjectMeta{Name: "prov-1", Namespace: "default"},
		Spec: k8s.ProvisionSpec{
			MachineRef:    "vm-01",
			BootTargetRef: "debian-13",
		},
		Status: k8s.ProvisionStatus{
			Phase:       "InProgress",
			LastUpdated: metav1.NewTime(time.Now().Add(-31 * time.Minute)),
		},
	}
	k := newTestK8sClient(
		provision,
		&k8s.Machine{
			ObjectMeta: metav1.ObjectMeta{Name: "vm-01", Namespace: "default"},
			Spec:       k8s.MachineSpec{MAC: "aa-bb-cc-dd-ee-ff"},
		},
		&k8s.BootTarget{
			ObjectMeta: metav1.ObjectMeta{Name: "debian-13", Namespace: "default"},
			Spec:       k8s.BootTargetSpec{BootMediaRef: "debian-iso"},
		},
		&k8s.BootMedia{
			ObjectMeta: metav1.ObjectMeta{Name: "debian-iso", Namespace: "default"},
			Status:     k8s.BootMediaStatus{Phase: "Complete"},
		},
	)

	ctrl := New(k)
	ctrl.reconcileProvision(ctx, provision)

	var updated k8s.Provision
	if err := k.Get(ctx, k.Key("prov-1"), &updated); err != nil {
		t.Fatalf("failed to get provision: %v", err)
	}
	if updated.Status.Phase != "Failed" {
		t.Errorf("expected phase Failed (timeout), got %q", updated.Status.Phase)
	}
	if !strings.Contains(updated.Status.Message, "Timed out") {
		t.Errorf("expected timeout message, got %q", updated.Status.Message)
	}
}

func TestReconcileProvision_InProgressNotTimedOut(t *testing.T) {
	ctx := context.Background()
	provision := &k8s.Provision{
		ObjectMeta: metav1.ObjectMeta{Name: "prov-1", Namespace: "default"},
		Spec: k8s.ProvisionSpec{
			MachineRef:    "vm-01",
			BootTargetRef: "debian-13",
		},
		Status: k8s.ProvisionStatus{
			Phase:       "InProgress",
			LastUpdated: metav1.NewTime(time.Now().Add(-5 * time.Minute)),
		},
	}
	k := newTestK8sClient(
		provision,
		&k8s.Machine{
			ObjectMeta: metav1.ObjectMeta{Name: "vm-01", Namespace: "default"},
			Spec:       k8s.MachineSpec{MAC: "aa-bb-cc-dd-ee-ff"},
		},
		&k8s.BootTarget{
			ObjectMeta: metav1.ObjectMeta{Name: "debian-13", Namespace: "default"},
			Spec:       k8s.BootTargetSpec{BootMediaRef: "debian-iso"},
		},
		&k8s.BootMedia{
			ObjectMeta: metav1.ObjectMeta{Name: "debian-iso", Namespace: "default"},
			Status:     k8s.BootMediaStatus{Phase: "Complete"},
		},
	)

	ctrl := New(k)
	ctrl.reconcileProvision(ctx, provision)

	var updated k8s.Provision
	if err := k.Get(ctx, k.Key("prov-1"), &updated); err != nil {
		t.Fatalf("failed to get provision: %v", err)
	}
	if updated.Status.Phase != "InProgress" {
		t.Errorf("expected phase to remain InProgress, got %q", updated.Status.Phase)
	}
}

func TestReconcileProvision_CompleteIsNoop(t *testing.T) {
	ctx := context.Background()
	provision := &k8s.Provision{
		ObjectMeta: metav1.ObjectMeta{Name: "prov-1", Namespace: "default"},
		Spec: k8s.ProvisionSpec{
			MachineRef:    "vm-01",
			BootTargetRef: "debian-13",
		},
		Status: k8s.ProvisionStatus{Phase: "Complete"},
	}
	k := newTestK8sClient(
		provision,
		&k8s.Machine{
			ObjectMeta: metav1.ObjectMeta{Name: "vm-01", Namespace: "default"},
			Spec:       k8s.MachineSpec{MAC: "aa-bb-cc-dd-ee-ff"},
		},
		&k8s.BootTarget{
			ObjectMeta: metav1.ObjectMeta{Name: "debian-13", Namespace: "default"},
			Spec:       k8s.BootTargetSpec{BootMediaRef: "debian-iso"},
		},
		&k8s.BootMedia{
			ObjectMeta: metav1.ObjectMeta{Name: "debian-iso", Namespace: "default"},
			Status:     k8s.BootMediaStatus{Phase: "Complete"},
		},
	)

	ctrl := New(k)
	ctrl.reconcileProvision(ctx, provision)

	var updated k8s.Provision
	if err := k.Get(ctx, k.Key("prov-1"), &updated); err != nil {
		t.Fatalf("failed to get provision: %v", err)
	}
	if updated.Status.Phase != "Complete" {
		t.Errorf("expected phase to remain Complete, got %q", updated.Status.Phase)
	}
}

func TestValidateProvisionRefs_AllValid(t *testing.T) {
	ctx := context.Background()
	k := newTestK8sClient(
		&k8s.Machine{
			ObjectMeta: metav1.ObjectMeta{Name: "vm-01", Namespace: "default"},
			Spec:       k8s.MachineSpec{MAC: "aa-bb-cc-dd-ee-ff"},
		},
		&k8s.BootTarget{
			ObjectMeta: metav1.ObjectMeta{Name: "debian-13", Namespace: "default"},
			Spec:       k8s.BootTargetSpec{BootMediaRef: "debian-iso"},
		},
		&k8s.BootMedia{
			ObjectMeta: metav1.ObjectMeta{Name: "debian-iso", Namespace: "default"},
			Status:     k8s.BootMediaStatus{Phase: "Complete"},
		},
		&k8s.ResponseTemplate{
			ObjectMeta: metav1.ObjectMeta{Name: "preseed", Namespace: "default"},
			Spec:       k8s.ResponseTemplateSpec{Files: map[string]string{"preseed.cfg": "content"}},
		},
		newConfigMap("net-cfg", map[string]string{"gateway": "10.0.0.1"}),
		newSecret("ssh-keys", map[string][]byte{"key": []byte("data")}),
	)

	ctrl := New(k)
	provision := &k8s.Provision{
		ObjectMeta: metav1.ObjectMeta{Name: "prov-1", Namespace: "default"},
		Spec: k8s.ProvisionSpec{
			MachineRef:          "vm-01",
			BootTargetRef:       "debian-13",
			ResponseTemplateRef: "preseed",
			ConfigMaps:          []string{"net-cfg"},
			Secrets:             []string{"ssh-keys"},
		},
	}

	err := ctrl.validateProvisionRefs(ctx, provision)
	if err != nil {
		t.Errorf("expected no error for valid refs, got %v", err)
	}
}

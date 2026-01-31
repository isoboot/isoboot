package k8s

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func testScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = corev1.AddToScheme(s)
	_ = AddToScheme(s)
	return s
}

func TestNormalizeMAC(t *testing.T) {
	tests := []struct {
		name     string
		mac      string
		expected string
	}{
		{"dash-separated lowercase", "aa-bb-cc-dd-ee-ff", "aa-bb-cc-dd-ee-ff"},
		{"dash-separated uppercase", "AA-BB-CC-DD-EE-FF", "aa-bb-cc-dd-ee-ff"},
		{"dash-separated mixed case", "Aa-Bb-Cc-Dd-Ee-Ff", "aa-bb-cc-dd-ee-ff"},
		{"colon-separated rejected", "aa:bb:cc:dd:ee:ff", ""},
		{"empty string", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalizeMAC(tt.mac)
			if result != tt.expected {
				t.Errorf("normalizeMAC(%q) = %q, want %q", tt.mac, result, tt.expected)
			}
		})
	}
}

func TestBootMedia_KernelFilename(t *testing.T) {
	tests := []struct {
		name     string
		bm       *BootMedia
		expected string
	}{
		{
			name:     "from kernel URL",
			bm:       &BootMedia{Spec: BootMediaSpec{Kernel: &BootMediaFileRef{URL: "http://example.com/path/linux"}}},
			expected: "linux",
		},
		{
			name: "from ISO path",
			bm: &BootMedia{Spec: BootMediaSpec{ISO: &BootMediaISO{
				URL: "http://example.com/debian.iso", Kernel: "/install.amd/vmlinuz", Initrd: "/install.amd/initrd.gz",
			}}},
			expected: "vmlinuz",
		},
		{
			name:     "empty",
			bm:       &BootMedia{},
			expected: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.bm.KernelFilename()
			if got != tt.expected {
				t.Errorf("KernelFilename() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestBootMedia_InitrdFilename(t *testing.T) {
	tests := []struct {
		name     string
		bm       *BootMedia
		expected string
	}{
		{
			name:     "from initrd URL",
			bm:       &BootMedia{Spec: BootMediaSpec{Initrd: &BootMediaFileRef{URL: "http://example.com/path/initrd.gz"}}},
			expected: "initrd.gz",
		},
		{
			name: "from ISO path",
			bm: &BootMedia{Spec: BootMediaSpec{ISO: &BootMediaISO{
				URL: "http://example.com/debian.iso", Kernel: "/install.amd/vmlinuz", Initrd: "/install.amd/initrd.gz",
			}}},
			expected: "initrd.gz",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.bm.InitrdFilename()
			if got != tt.expected {
				t.Errorf("InitrdFilename() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestBootMedia_Validate(t *testing.T) {
	tests := []struct {
		name      string
		bm        *BootMedia
		expectErr string
	}{
		{
			name: "valid direct",
			bm: &BootMedia{Spec: BootMediaSpec{
				Kernel: &BootMediaFileRef{URL: "http://example.com/linux"},
				Initrd: &BootMediaFileRef{URL: "http://example.com/initrd.gz"},
			}},
		},
		{
			name: "valid ISO",
			bm: &BootMedia{Spec: BootMediaSpec{
				ISO: &BootMediaISO{
					URL:    "http://example.com/debian.iso",
					Kernel: "/install.amd/vmlinuz",
					Initrd: "/install.amd/initrd.gz",
				},
			}},
		},
		{
			name: "both set",
			bm: &BootMedia{Spec: BootMediaSpec{
				Kernel: &BootMediaFileRef{URL: "http://example.com/linux"},
				Initrd: &BootMediaFileRef{URL: "http://example.com/initrd.gz"},
				ISO:    &BootMediaISO{URL: "http://example.com/debian.iso", Kernel: "/k", Initrd: "/i"},
			}},
			expectErr: "cannot specify both",
		},
		{
			name:      "neither set",
			bm:        &BootMedia{},
			expectErr: "must specify either",
		},
		{
			name: "kernel only",
			bm: &BootMedia{Spec: BootMediaSpec{
				Kernel: &BootMediaFileRef{URL: "http://example.com/linux"},
			}},
			expectErr: "kernel requires initrd",
		},
		{
			name: "initrd only",
			bm: &BootMedia{Spec: BootMediaSpec{
				Initrd: &BootMediaFileRef{URL: "http://example.com/initrd.gz"},
			}},
			expectErr: "initrd requires kernel",
		},
		{
			name: "duplicate basenames",
			bm: &BootMedia{Spec: BootMediaSpec{
				Kernel: &BootMediaFileRef{URL: "http://example.com/path1/file"},
				Initrd: &BootMediaFileRef{URL: "http://example.com/path2/file"},
			}},
			expectErr: "duplicate basename",
		},
		{
			name: "ISO missing kernel path",
			bm: &BootMedia{Spec: BootMediaSpec{
				ISO: &BootMediaISO{
					URL:    "http://example.com/debian.iso",
					Initrd: "/install.amd/initrd.gz",
				},
			}},
			expectErr: "iso.kernel is required",
		},
		{
			name: "ISO missing initrd path",
			bm: &BootMedia{Spec: BootMediaSpec{
				ISO: &BootMediaISO{
					URL:    "http://example.com/debian.iso",
					Kernel: "/install.amd/vmlinuz",
				},
			}},
			expectErr: "iso.initrd is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.bm.Validate()
			if tt.expectErr == "" {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.expectErr) {
				t.Errorf("error %q does not contain %q", err.Error(), tt.expectErr)
			}
		})
	}
}

func TestFilenameFromURL(t *testing.T) {
	tests := []struct {
		name        string
		url         string
		expected    string
		expectError bool
	}{
		{"normal URL", "http://example.com/path/to/file.iso", "file.iso", false},
		{"root path", "http://example.com/", "", true},
		{"no path", "http://example.com", "", true},
		{"with query", "http://example.com/file.iso?token=abc", "file.iso", false},
		{"path traversal", "http://example.com/path/..", "", true},
		{"path traversal with slash", "http://example.com/path/../", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := FilenameFromURL(tt.url)
			if tt.expectError {
				if err == nil {
					t.Errorf("expected error, got %q", got)
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				if got != tt.expected {
					t.Errorf("got %q, want %q", got, tt.expected)
				}
			}
		})
	}
}

func TestProvisionSpec_GetBootTargetRef(t *testing.T) {
	tests := []struct {
		name     string
		spec     ProvisionSpec
		expected string
	}{
		{
			name:     "new field",
			spec:     ProvisionSpec{BootTargetRef: "debian-13"},
			expected: "debian-13",
		},
		{
			name:     "legacy field",
			spec:     ProvisionSpec{Target: "debian-12"},
			expected: "debian-12",
		},
		{
			name:     "new takes precedence",
			spec:     ProvisionSpec{BootTargetRef: "new", Target: "old"},
			expected: "new",
		},
		{
			name:     "both empty",
			spec:     ProvisionSpec{},
			expected: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.spec.GetBootTargetRef()
			if got != tt.expected {
				t.Errorf("GetBootTargetRef() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestFindMachineByMAC(t *testing.T) {
	ctx := context.Background()
	cl := fake.NewClientBuilder().
		WithScheme(testScheme()).
		WithObjects(
			&Machine{
				ObjectMeta: metav1.ObjectMeta{Name: "vm-01", Namespace: "default"},
				Spec:       MachineSpec{MAC: "aa-bb-cc-dd-ee-ff"},
			},
			&Machine{
				ObjectMeta: metav1.ObjectMeta{Name: "vm-02", Namespace: "default"},
				Spec:       MachineSpec{MAC: "11-22-33-44-55-66"},
			},
		).Build()
	k := NewClientFromClient(cl, "default")

	// Found
	m, err := k.FindMachineByMAC(ctx, "AA-BB-CC-DD-EE-FF")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m == nil || m.Name != "vm-01" {
		t.Errorf("expected vm-01, got %v", m)
	}

	// Not found
	m, err = k.FindMachineByMAC(ctx, "ff-ff-ff-ff-ff-ff")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m != nil {
		t.Errorf("expected nil, got %v", m)
	}

	// Colon format rejected
	m, err = k.FindMachineByMAC(ctx, "aa:bb:cc:dd:ee:ff")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m != nil {
		t.Errorf("expected nil for colon format, got %v", m)
	}
}

func TestFindProvisionByMAC(t *testing.T) {
	ctx := context.Background()
	cl := fake.NewClientBuilder().
		WithScheme(testScheme()).
		WithObjects(
			&Machine{
				ObjectMeta: metav1.ObjectMeta{Name: "vm-01", Namespace: "default"},
				Spec:       MachineSpec{MAC: "aa-bb-cc-dd-ee-ff"},
			},
			&Provision{
				ObjectMeta: metav1.ObjectMeta{Name: "prov-1", Namespace: "default"},
				Spec:       ProvisionSpec{MachineRef: "vm-01", BootTargetRef: "debian-13"},
				Status:     ProvisionStatus{Phase: "Pending"},
			},
		).
		WithStatusSubresource(&Provision{}).
		Build()
	k := NewClientFromClient(cl, "default")

	// Found with matching phase
	p, err := k.FindProvisionByMAC(ctx, "aa-bb-cc-dd-ee-ff", "Pending")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p == nil || p.Name != "prov-1" {
		t.Errorf("expected prov-1, got %v", p)
	}

	// Not found with wrong phase
	p, err = k.FindProvisionByMAC(ctx, "aa-bb-cc-dd-ee-ff", "InProgress")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p != nil {
		t.Errorf("expected nil for wrong phase, got %v", p)
	}

	// Found with empty phase (any)
	p, err = k.FindProvisionByMAC(ctx, "aa-bb-cc-dd-ee-ff", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p == nil || p.Name != "prov-1" {
		t.Errorf("expected prov-1, got %v", p)
	}
}

func TestFindProvisionByHostname(t *testing.T) {
	ctx := context.Background()
	cl := fake.NewClientBuilder().
		WithScheme(testScheme()).
		WithObjects(
			&Provision{
				ObjectMeta: metav1.ObjectMeta{Name: "prov-1", Namespace: "default"},
				Spec:       ProvisionSpec{MachineRef: "vm-01", BootTargetRef: "debian-13"},
				Status:     ProvisionStatus{Phase: "Pending"},
			},
		).
		WithStatusSubresource(&Provision{}).
		Build()
	k := NewClientFromClient(cl, "default")

	// Found
	p, err := k.FindProvisionByHostname(ctx, "vm-01", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p == nil || p.Name != "prov-1" {
		t.Errorf("expected prov-1, got %v", p)
	}

	// Not found
	p, err = k.FindProvisionByHostname(ctx, "vm-99", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p != nil {
		t.Errorf("expected nil, got %v", p)
	}
}

func TestListProvisionsByMachine(t *testing.T) {
	ctx := context.Background()
	cl := fake.NewClientBuilder().
		WithScheme(testScheme()).
		WithObjects(
			&Provision{
				ObjectMeta: metav1.ObjectMeta{Name: "prov-1", Namespace: "default"},
				Spec:       ProvisionSpec{MachineRef: "vm-01", BootTargetRef: "debian-13"},
			},
			&Provision{
				ObjectMeta: metav1.ObjectMeta{Name: "prov-2", Namespace: "default"},
				Spec:       ProvisionSpec{MachineRef: "vm-02", BootTargetRef: "debian-13"},
			},
			&Provision{
				ObjectMeta: metav1.ObjectMeta{Name: "prov-3", Namespace: "default"},
				Spec:       ProvisionSpec{MachineRef: "vm-01", BootTargetRef: "debian-12"},
			},
		).Build()
	k := NewClientFromClient(cl, "default")

	result, err := k.ListProvisionsByMachine(ctx, "vm-01")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2, got %d", len(result))
	}

	// No matches
	result, err = k.ListProvisionsByMachine(ctx, "vm-99")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("expected 0, got %d", len(result))
	}
}

func TestUpdateProvisionStatus(t *testing.T) {
	ctx := context.Background()
	cl := fake.NewClientBuilder().
		WithScheme(testScheme()).
		WithObjects(
			&Provision{
				ObjectMeta: metav1.ObjectMeta{Name: "prov-1", Namespace: "default"},
				Spec:       ProvisionSpec{MachineRef: "vm-01", BootTargetRef: "debian-13"},
			},
		).
		WithStatusSubresource(&Provision{}).
		Build()
	k := NewClientFromClient(cl, "default")

	err := k.UpdateProvisionStatus(ctx, "prov-1", "InProgress", "Boot started", "10.0.0.5")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var updated Provision
	if err := cl.Get(ctx, k.Key("prov-1"), &updated); err != nil {
		t.Fatalf("failed to get updated provision: %v", err)
	}
	if updated.Status.Phase != "InProgress" {
		t.Errorf("expected phase InProgress, got %q", updated.Status.Phase)
	}
	if updated.Status.IP != "10.0.0.5" {
		t.Errorf("expected IP 10.0.0.5, got %q", updated.Status.IP)
	}
	if updated.Status.LastUpdated.IsZero() {
		t.Error("expected LastUpdated to be set")
	}
}

func TestUpdateProvisionStatus_PreservesIP(t *testing.T) {
	ctx := context.Background()
	cl := fake.NewClientBuilder().
		WithScheme(testScheme()).
		WithObjects(
			&Provision{
				ObjectMeta: metav1.ObjectMeta{Name: "prov-1", Namespace: "default"},
				Spec:       ProvisionSpec{MachineRef: "vm-01", BootTargetRef: "debian-13"},
				Status:     ProvisionStatus{Phase: "InProgress", IP: "10.0.0.5"},
			},
		).
		WithStatusSubresource(&Provision{}).
		Build()
	k := NewClientFromClient(cl, "default")

	// Update with empty IP should preserve existing
	err := k.UpdateProvisionStatus(ctx, "prov-1", "Complete", "Done", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var updated Provision
	if err := cl.Get(ctx, k.Key("prov-1"), &updated); err != nil {
		t.Fatalf("failed to get updated provision: %v", err)
	}
	if updated.Status.IP != "10.0.0.5" {
		t.Errorf("expected IP preserved as 10.0.0.5, got %q", updated.Status.IP)
	}
}

func TestUpdateBootMediaStatus(t *testing.T) {
	ctx := context.Background()
	cl := fake.NewClientBuilder().
		WithScheme(testScheme()).
		WithObjects(
			&BootMedia{
				ObjectMeta: metav1.ObjectMeta{Name: "debian-13", Namespace: "default"},
				Spec: BootMediaSpec{
					Kernel: &BootMediaFileRef{URL: "http://example.com/linux"},
					Initrd: &BootMediaFileRef{URL: "http://example.com/initrd.gz"},
				},
			},
		).
		WithStatusSubresource(&BootMedia{}).
		Build()
	k := NewClientFromClient(cl, "default")

	status := &BootMediaStatus{
		Phase:   "Complete",
		Message: "All done",
		Kernel:  &FileStatus{Name: "linux", Phase: "Complete", SHA256: "abc123"},
	}
	err := k.UpdateBootMediaStatus(ctx, "debian-13", status)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var updated BootMedia
	if err := cl.Get(ctx, k.Key("debian-13"), &updated); err != nil {
		t.Fatalf("failed to get updated bootmedia: %v", err)
	}
	if updated.Status.Phase != "Complete" {
		t.Errorf("expected phase Complete, got %q", updated.Status.Phase)
	}
	if updated.Status.Kernel == nil || updated.Status.Kernel.SHA256 != "abc123" {
		t.Errorf("unexpected kernel status: %+v", updated.Status.Kernel)
	}
}

func TestUpdateBootMediaStatus_NilStatus(t *testing.T) {
	k := NewClientFromClient(fake.NewClientBuilder().WithScheme(testScheme()).Build(), "default")
	err := k.UpdateBootMediaStatus(context.Background(), "test", nil)
	if err == nil {
		t.Error("expected error for nil status")
	}
}

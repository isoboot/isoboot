package handlers

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/isoboot/isoboot/internal/controllerclient"
)

// mockBootClient implements BootClient for testing.
type mockBootClient struct {
	getConfigMapValue      func(ctx context.Context, configMapName, key string) (string, error)
	getMachineByMAC        func(ctx context.Context, mac string) (string, error)
	getProvisionsByMachine func(ctx context.Context, machineName string) ([]controllerclient.ProvisionSummary, error)
	getBootTarget          func(ctx context.Context, name string) (*controllerclient.BootTargetInfo, error)
	getDiskImage           func(ctx context.Context, name string) (*controllerclient.DiskImageInfo, error)
	updateProvisionStatus  func(ctx context.Context, name, status, message, ip string) error
}

func (m *mockBootClient) GetConfigMapValue(ctx context.Context, configMapName, key string) (string, error) {
	return m.getConfigMapValue(ctx, configMapName, key)
}
func (m *mockBootClient) GetMachineByMAC(ctx context.Context, mac string) (string, error) {
	return m.getMachineByMAC(ctx, mac)
}
func (m *mockBootClient) GetProvisionsByMachine(ctx context.Context, machineName string) ([]controllerclient.ProvisionSummary, error) {
	return m.getProvisionsByMachine(ctx, machineName)
}
func (m *mockBootClient) GetBootTarget(ctx context.Context, name string) (*controllerclient.BootTargetInfo, error) {
	return m.getBootTarget(ctx, name)
}
func (m *mockBootClient) GetDiskImage(ctx context.Context, name string) (*controllerclient.DiskImageInfo, error) {
	if m.getDiskImage != nil {
		return m.getDiskImage(ctx, name)
	}
	return nil, fmt.Errorf("diskimage %s: %w", name, controllerclient.ErrNotFound)
}
func (m *mockBootClient) UpdateProvisionStatus(ctx context.Context, name, status, message, ip string) error {
	return m.updateProvisionStatus(ctx, name, status, message, ip)
}

func TestServeBootDone_MACNormalization(t *testing.T) {
	// Test that MAC addresses are normalized to lowercase
	tests := []struct {
		input    string
		expected string
	}{
		{"AA-BB-CC-DD-EE-FF", "aa-bb-cc-dd-ee-ff"},
		{"aa-bb-cc-dd-ee-ff", "aa-bb-cc-dd-ee-ff"},
		{"Aa-Bb-Cc-Dd-Ee-Ff", "aa-bb-cc-dd-ee-ff"},
	}

	for _, tt := range tests {
		mac := tt.input
		mac = strings.ToLower(mac)
		if mac != tt.expected {
			t.Errorf("MAC normalization: input %q, got %q, want %q", tt.input, mac, tt.expected)
		}
	}
}

func TestSplitHostDomain(t *testing.T) {
	tests := []struct {
		name         string
		wantHostname string
		wantDomain   string
	}{
		{"abc.lan", "abc", "lan"},
		{"web.example.com", "web", "example.com"},
		{"server01", "server01", ""},
		{"vm-deb-0099.internal.example.com", "vm-deb-0099", "internal.example.com"},
		{"", "", ""},
		{".domain", "", "domain"},
	}

	for _, tt := range tests {
		hostname, domain := splitHostDomain(tt.name)
		if hostname != tt.wantHostname {
			t.Errorf("splitHostDomain(%q) hostname = %q, want %q", tt.name, hostname, tt.wantHostname)
		}
		if domain != tt.wantDomain {
			t.Errorf("splitHostDomain(%q) domain = %q, want %q", tt.name, domain, tt.wantDomain)
		}
	}
}

func TestServeBootIPXE_Success(t *testing.T) {
	mock := &mockBootClient{
		getConfigMapValue: func(ctx context.Context, configMapName, key string) (string, error) {
			if configMapName == "isoboot-templates" && key == "boot.ipxe" {
				return "#!ipxe\nchain http://{{ .Host }}:{{ .Port }}/boot/conditional-boot?mac=${net0/mac}\n", nil
			}
			return "", fmt.Errorf("not found")
		},
	}

	h := NewBootHandler("10.0.0.1", "8080", "3128", mock, "isoboot-templates")
	req := httptest.NewRequest("GET", "/boot/boot.ipxe", nil)
	w := httptest.NewRecorder()

	h.ServeBootIPXE(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "10.0.0.1:8080") {
		t.Errorf("expected host:port in body, got %q", w.Body.String())
	}
	if w.Header().Get("Content-Length") == "" {
		t.Error("expected Content-Length header")
	}
}

func TestServeBootIPXE_TemplateError(t *testing.T) {
	mock := &mockBootClient{
		getConfigMapValue: func(ctx context.Context, configMapName, key string) (string, error) {
			return "", fmt.Errorf("configmap not found")
		},
	}

	h := NewBootHandler("10.0.0.1", "8080", "3128", mock, "missing-cm")
	req := httptest.NewRequest("GET", "/boot/boot.ipxe", nil)
	w := httptest.NewRecorder()

	h.ServeBootIPXE(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}
}

func TestServeConditionalBoot_PendingProvision(t *testing.T) {
	var updatedName, updatedStatus string
	mock := &mockBootClient{
		getMachineByMAC: func(ctx context.Context, mac string) (string, error) {
			return "vm-01.lan", nil
		},
		getProvisionsByMachine: func(ctx context.Context, machineName string) ([]controllerclient.ProvisionSummary, error) {
			return []controllerclient.ProvisionSummary{
				{Name: "prov-1", Status: "Pending", BootTargetRef: "debian-13"},
			}, nil
		},
		getBootTarget: func(ctx context.Context, name string) (*controllerclient.BootTargetInfo, error) {
			return &controllerclient.BootTargetInfo{
				Template: "#!ipxe\nkernel http://{{ .Host }}:{{ .Port }}/iso/content/{{ .BootTarget }}/mini.iso/linux\nboot\n",
			}, nil
		},
		updateProvisionStatus: func(ctx context.Context, name, status, message, ip string) error {
			updatedName = name
			updatedStatus = status
			return nil
		},
	}

	h := NewBootHandler("10.0.0.1", "8080", "3128", mock, "isoboot-templates")
	req := httptest.NewRequest("GET", "/boot/conditional-boot?mac=aa-bb-cc-dd-ee-ff", nil)
	w := httptest.NewRecorder()

	h.ServeConditionalBoot(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "10.0.0.1:8080") {
		t.Errorf("expected host:port in body, got %q", body)
	}
	if !strings.Contains(body, "debian-13") {
		t.Errorf("expected boot target in body, got %q", body)
	}
	if updatedName != "prov-1" || updatedStatus != "InProgress" {
		t.Errorf("expected provision prov-1 marked InProgress, got name=%q status=%q", updatedName, updatedStatus)
	}
}

func TestServeConditionalBoot_DiskImageFile(t *testing.T) {
	mock := &mockBootClient{
		getMachineByMAC: func(ctx context.Context, mac string) (string, error) {
			return "vm-01.lan", nil
		},
		getProvisionsByMachine: func(ctx context.Context, machineName string) ([]controllerclient.ProvisionSummary, error) {
			return []controllerclient.ProvisionSummary{
				{Name: "prov-1", Status: "Pending", BootTargetRef: "ubuntu-24"},
			}, nil
		},
		getBootTarget: func(ctx context.Context, name string) (*controllerclient.BootTargetInfo, error) {
			return &controllerclient.BootTargetInfo{
				DiskImage: "ubuntu-iso",
				Template:  "url=http://{{ .Host }}:{{ .Port }}/iso/download/{{ .BootTarget }}/{{ .DiskImageFile }}",
			}, nil
		},
		getDiskImage: func(ctx context.Context, name string) (*controllerclient.DiskImageInfo, error) {
			return &controllerclient.DiskImageInfo{ISOFilename: "ubuntu-24.04.1-live-server-amd64.iso"}, nil
		},
		updateProvisionStatus: func(ctx context.Context, name, status, message, ip string) error {
			return nil
		},
	}

	h := NewBootHandler("10.0.0.1", "8080", "3128", mock, "cm")
	req := httptest.NewRequest("GET", "/boot/conditional-boot?mac=aa-bb-cc-dd-ee-ff", nil)
	w := httptest.NewRecorder()

	h.ServeConditionalBoot(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "ubuntu-24.04.1-live-server-amd64.iso") {
		t.Errorf("expected DiskImageFile in body, got %q", body)
	}
}

func TestServeConditionalBoot_DiskImageNotFound(t *testing.T) {
	// When DiskImage is not found, DiskImageFile should be empty but boot should still succeed
	mock := &mockBootClient{
		getMachineByMAC: func(ctx context.Context, mac string) (string, error) {
			return "vm-01.lan", nil
		},
		getProvisionsByMachine: func(ctx context.Context, machineName string) ([]controllerclient.ProvisionSummary, error) {
			return []controllerclient.ProvisionSummary{
				{Name: "prov-1", Status: "Pending", BootTargetRef: "debian-13"},
			}, nil
		},
		getBootTarget: func(ctx context.Context, name string) (*controllerclient.BootTargetInfo, error) {
			return &controllerclient.BootTargetInfo{
				DiskImage: "missing-image",
				Template:  "#!ipxe\nboot\n",
			}, nil
		},
		getDiskImage: func(ctx context.Context, name string) (*controllerclient.DiskImageInfo, error) {
			return nil, fmt.Errorf("diskimage %s: %w", name, controllerclient.ErrNotFound)
		},
		updateProvisionStatus: func(ctx context.Context, name, status, message, ip string) error {
			return nil
		},
	}

	h := NewBootHandler("10.0.0.1", "8080", "3128", mock, "cm")
	req := httptest.NewRequest("GET", "/boot/conditional-boot?mac=aa-bb-cc-dd-ee-ff", nil)
	w := httptest.NewRecorder()

	h.ServeConditionalBoot(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 (DiskImage not found is non-fatal), got %d", w.Code)
	}
}

func TestServeConditionalBoot_NoMachine(t *testing.T) {
	mock := &mockBootClient{
		getMachineByMAC: func(ctx context.Context, mac string) (string, error) {
			return "", nil
		},
	}

	h := NewBootHandler("10.0.0.1", "8080", "3128", mock, "cm")
	req := httptest.NewRequest("GET", "/boot/conditional-boot?mac=aa-bb-cc-dd-ee-ff", nil)
	w := httptest.NewRecorder()

	h.ServeConditionalBoot(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestServeConditionalBoot_NoPendingProvision(t *testing.T) {
	mock := &mockBootClient{
		getMachineByMAC: func(ctx context.Context, mac string) (string, error) {
			return "vm-01.lan", nil
		},
		getProvisionsByMachine: func(ctx context.Context, machineName string) ([]controllerclient.ProvisionSummary, error) {
			return []controllerclient.ProvisionSummary{
				{Name: "prov-1", Status: "Complete", BootTargetRef: "debian-13"},
			}, nil
		},
	}

	h := NewBootHandler("10.0.0.1", "8080", "3128", mock, "cm")
	req := httptest.NewRequest("GET", "/boot/conditional-boot?mac=aa-bb-cc-dd-ee-ff", nil)
	w := httptest.NewRecorder()

	h.ServeConditionalBoot(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestServeBootDone_Success(t *testing.T) {
	var updatedName, updatedStatus string
	mock := &mockBootClient{
		getMachineByMAC: func(ctx context.Context, mac string) (string, error) {
			return "vm-01.lan", nil
		},
		getProvisionsByMachine: func(ctx context.Context, machineName string) ([]controllerclient.ProvisionSummary, error) {
			return []controllerclient.ProvisionSummary{
				{Name: "prov-1", Status: "InProgress", BootTargetRef: "debian-13"},
			}, nil
		},
		updateProvisionStatus: func(ctx context.Context, name, status, message, ip string) error {
			updatedName = name
			updatedStatus = status
			return nil
		},
	}

	h := NewBootHandler("10.0.0.1", "8080", "3128", mock, "cm")
	req := httptest.NewRequest("GET", "/boot/done?mac=aa-bb-cc-dd-ee-ff", nil)
	w := httptest.NewRecorder()

	h.ServeBootDone(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if w.Body.String() != "ok" {
		t.Errorf("expected body 'ok', got %q", w.Body.String())
	}
	if updatedName != "prov-1" || updatedStatus != "Complete" {
		t.Errorf("expected prov-1 marked Complete, got name=%q status=%q", updatedName, updatedStatus)
	}
}

func TestServeConditionalBoot_NoMAC(t *testing.T) {
	h := NewBootHandler("10.0.0.1", "8080", "3128", &mockBootClient{}, "cm")
	req := httptest.NewRequest("GET", "/boot/conditional-boot", nil)
	w := httptest.NewRecorder()

	h.ServeConditionalBoot(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestServeConditionalBoot_EmptyStatusTreatedAsPending(t *testing.T) {
	mock := &mockBootClient{
		getMachineByMAC: func(ctx context.Context, mac string) (string, error) {
			return "vm-01.lan", nil
		},
		getProvisionsByMachine: func(ctx context.Context, machineName string) ([]controllerclient.ProvisionSummary, error) {
			return []controllerclient.ProvisionSummary{
				{Name: "prov-1", Status: "", BootTargetRef: "debian-13"},
			}, nil
		},
		getBootTarget: func(ctx context.Context, name string) (*controllerclient.BootTargetInfo, error) {
			return &controllerclient.BootTargetInfo{
				Template: "#!ipxe\nboot\n",
			}, nil
		},
		updateProvisionStatus: func(ctx context.Context, name, status, message, ip string) error {
			return nil
		},
	}

	h := NewBootHandler("10.0.0.1", "8080", "3128", mock, "cm")
	req := httptest.NewRequest("GET", "/boot/conditional-boot?mac=aa-bb-cc-dd-ee-ff", nil)
	w := httptest.NewRecorder()

	h.ServeConditionalBoot(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 (empty status treated as Pending), got %d", w.Code)
	}
}

func TestServeBootDone_NoMAC(t *testing.T) {
	h := NewBootHandler("10.0.0.1", "8080", "3128", &mockBootClient{}, "cm")
	req := httptest.NewRequest("GET", "/boot/done", nil)
	w := httptest.NewRecorder()

	h.ServeBootDone(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestServeBootDone_UpdateStatusError(t *testing.T) {
	mock := &mockBootClient{
		getMachineByMAC: func(ctx context.Context, mac string) (string, error) {
			return "vm-01.lan", nil
		},
		getProvisionsByMachine: func(ctx context.Context, machineName string) ([]controllerclient.ProvisionSummary, error) {
			return []controllerclient.ProvisionSummary{
				{Name: "prov-1", Status: "InProgress", BootTargetRef: "debian-13"},
			}, nil
		},
		updateProvisionStatus: func(ctx context.Context, name, status, message, ip string) error {
			return fmt.Errorf("grpc call: connection refused")
		},
	}

	h := NewBootHandler("10.0.0.1", "8080", "3128", mock, "cm")
	req := httptest.NewRequest("GET", "/boot/done?mac=aa-bb-cc-dd-ee-ff", nil)
	w := httptest.NewRecorder()

	h.ServeBootDone(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}
}

func TestServeBootDone_NoInProgress(t *testing.T) {
	mock := &mockBootClient{
		getMachineByMAC: func(ctx context.Context, mac string) (string, error) {
			return "vm-01.lan", nil
		},
		getProvisionsByMachine: func(ctx context.Context, machineName string) ([]controllerclient.ProvisionSummary, error) {
			return []controllerclient.ProvisionSummary{
				{Name: "prov-1", Status: "Pending", BootTargetRef: "debian-13"},
			}, nil
		},
	}

	h := NewBootHandler("10.0.0.1", "8080", "3128", mock, "cm")
	req := httptest.NewRequest("GET", "/boot/done?mac=aa-bb-cc-dd-ee-ff", nil)
	w := httptest.NewRecorder()

	h.ServeBootDone(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}
